// Admin and service-token levers: user-data purge, product-reference count,
// resnapshot, entry rematch, platform and region normalization.

package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// PurgeUserData is the collection leg of account deletion.
func (h *Handlers) PurgeUserData(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if err := h.store.PurgeUserData(r.Context(), userID); err != nil {
		h.internalError(w, r, "purge", "purge failed", err)
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	w.WriteHeader(http.StatusNoContent)
}

// CountProductReferences is the admin delete's safety read: only this service
// can see entries, so the bff asks here across all users before deleting a product.
func (h *Handlers) CountProductReferences(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	if !h.requireAdmin(w, r) {
		return
	}
	n, err := h.store.CountEntriesByProduct(r.Context(), productId)
	if err != nil {
		h.internalError(w, r, "count_product_refs", "count failed", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		EntryCount int64 `json:"entry_count"`
	}{n})
}

// InternalResnapshot recomputes every game-backed entry's region-picked
// release date, localized presentation trio, and credit arrays from the
// product. Admin-or-service gated; idempotent, writing only when a field differs.
func (h *Handlers) InternalResnapshot(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	bearer := bearerToken(r)
	refs, err := h.store.ListGameBackedRefs(r.Context())
	if err != nil {
		h.internalError(w, r, "resnapshot_list", "list failed", err)
		return
	}
	byProduct := make(map[uuid.UUID][]store.GameEntryRef)
	for _, ref := range refs {
		byProduct[ref.ProductID] = append(byProduct[ref.ProductID], ref)
	}
	var seen, failed, updated int
	for pid, group := range byProduct {
		seen++
		prod, err := h.enrichment.GetProduct(r.Context(), bearer, pid)
		if err != nil {
			failed++
			h.logger.WarnContext(r.Context(), "resnapshot: product fetch failed", "product", pid, "err", err)
			continue
		}
		// Credits are game identity, not region-scoped: one derive serves the whole group.
		devs, pubs := pickCredits(prod)
		for _, ref := range group {
			pick := pickReleaseDate(prod.Igdb, ref.Region)
			name, translit, cover := pickLocalization(prod.Igdb, ref.Region)
			if datesEqual(pick, ref.FirstReleaseDate) &&
				strPtrEqual(name, ref.LocalizedName) &&
				strPtrEqual(translit, ref.LocalizedNameTranslit) &&
				strPtrEqual(cover, ref.LocalizedCoverURL) &&
				strSlicesEqual(devs, ref.Developers) &&
				strSlicesEqual(pubs, ref.Publishers) {
				continue
			}
			if err := h.store.SetSnapshotFields(r.Context(), ref.EntryID, pick, name, translit, cover, devs, pubs); err != nil {
				h.logger.WarnContext(r.Context(), "resnapshot: entry update failed", "entry", ref.EntryID, "err", err)
				continue
			}
			updated++
		}
	}
	h.logger.InfoContext(r.Context(), "resnapshot complete",
		"products_seen", seen, "products_failed", failed, "entries_updated", updated)
	writeJSON(w, http.StatusOK, map[string]int{
		"products_seen": seen, "products_failed": failed, "entries_updated": updated,
	})
}

// rematchBudget bounds a detached entry rematch (well beyond a full
// day-one backfill at the provider's polite request rate).
const rematchBudget = 30 * time.Minute

// InternalRematchEntries re-resolves auto-priced game-backed entries onto their
// region-correct sibling member, one resolve per (game, platform, region)
// triple; class-compatible and user-picked matches are never swept. Idempotent
// backfill for entries predating region-aware matching. httpkit.TriggerDetached
// admits one run at a time (409 rematch_in_progress), else 202 and a 30-minute
// detached run at the provider's 1 req/s rate (past httpkit's 30s write timeout).
// Repoints log entry and old->new product ids; counts land in the completion
// log and rematch.* metrics, not the response.
func (h *Handlers) InternalRematchEntries(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	// Captured before detaching: the request is not safe to read once this handler returns.
	bearer := bearerToken(r)
	started := httpkit.TriggerDetached(w, r, httpkit.TriggerDetachedOptions{
		Guard:          &h.rematching,
		ConflictCode:   "rematch_in_progress",
		ConflictDetail: "an entry rematch is already running",
		Started: func() {
			// The started line pairs with "rematch-entries complete": a
			// start with no completion inside the budget marks a hung run.
			h.logger.InfoContext(r.Context(), "entry rematch started")
		},
		Budget:   rematchBudget,
		Logger:   h.logger,
		PanicMsg: "entry rematch panicked",
		Run:      func(ctx context.Context) { h.runRematch(ctx, bearer) },
	})
	if !started {
		return
	}
	writeJSON(w, http.StatusAccepted, api.RematchAccepted{Status: "started"})
}

// runRematch is the entry rematch's sweep body: ctx is the detached, budget-bound
// context; bearer was captured from the trigger request before the goroutine started.
func (h *Handlers) runRematch(ctx context.Context, bearer string) {
	start := time.Now()
	defer func() { h.recordRematchDuration(ctx, time.Since(start).Seconds()) }()

	refs, err := h.store.ListAutoGameRematchRefs(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "rematch-entries: list failed", "err", err)
		return
	}
	type triple struct {
		game, platform int64
		region         string
	}
	byTriple := make(map[triple][]store.RematchEntryRef)
	for _, ref := range refs {
		k := triple{ref.IGDBGameID, ref.PlatformIGDBID, ref.Region}
		byTriple[k] = append(byTriple[k], ref)
	}
	// One member fetch per distinct product across the whole run;
	// members repeat heavily across triples of the same family.
	products := make(map[uuid.UUID]enrichapi.Product)
	member := func(id uuid.UUID) (enrichapi.Product, error) {
		if p, ok := products[id]; ok {
			return p, nil
		}
		p, err := h.enrichment.GetProduct(ctx, bearer, id)
		if err != nil {
			return enrichapi.Product{}, err
		}
		products[id] = p
		return p, nil
	}
	var seen, failed, repointed int
	for k, group := range byTriple {
		seen++
		var pending []store.RematchEntryRef
		fetchFailed := false
		for _, ref := range group {
			prod, err := member(ref.ProductID)
			if err != nil {
				h.logger.WarnContext(ctx, "rematch-entries: member fetch failed", "product", ref.ProductID, "err", err)
				fetchFailed = true
				break
			}
			if !regionCorrectMember(&prod, ref.Region) {
				pending = append(pending, ref)
			}
		}
		if fetchFailed {
			failed++
			h.countRematchTriple(ctx, "failed")
			continue
		}
		if len(pending) == 0 {
			h.countRematchTriple(ctx, "ok")
			continue
		}
		resolved, err := h.enrichment.Resolve(ctx, bearer, enrichapi.ResolveRequest{
			Type: "game", IgdbGameId: &k.game, PlatformIgdbId: &k.platform, Region: &k.region,
		})
		if err != nil {
			failed++
			h.countRematchTriple(ctx, "failed")
			h.logger.WarnContext(ctx, "rematch-entries: resolve failed",
				"game", k.game, "platform", k.platform, "region", k.region, "err", err)
			continue
		}
		for _, ref := range pending {
			if resolved.Id == ref.ProductID {
				continue
			}
			d := pickReleaseDate(resolved.Igdb, ref.Region)
			name, translit, cover := pickLocalization(resolved.Igdb, ref.Region)
			devs, pubs := pickCredits(resolved)
			if err := h.store.RepointEntry(ctx, ref.EntryID, resolved.Id, d, name, translit, cover, devs, pubs); err != nil {
				h.logger.WarnContext(ctx, "rematch-entries: repoint failed", "entry", ref.EntryID, "err", err)
				continue
			}
			h.logger.InfoContext(ctx, "rematch-entries: repointed",
				"entry", ref.EntryID, "from", ref.ProductID, "to", resolved.Id, "region", k.region)
			repointed++
			h.countRematchRepoint(ctx)
		}
		h.countRematchTriple(ctx, "ok")
	}
	h.logger.InfoContext(ctx, "rematch-entries complete",
		"triples_seen", seen, "triples_failed", failed, "entries_repointed", repointed)
}

// InternalNormalizePlatforms canonicalizes free-text custom-entry platforms:
// entries with platform_name but no platform_igdb_id match exact-or-alias
// (never fuzzy) against the enrichment platform catalog and get stamped.
// Re-runnable; admin-or-service gated (the nightly job runs it alongside
// normalize-regions and the entry rematch).
//
// Offline test: kubectl port-forward svc/collection 8085:8080, mint a token via
// POST :8082/oauth/dev/token (task grant-fixture-admin), then POST
// :8085/internal/normalize-platforms with it. Answers {"scanned":N,"normalized":N,"skipped":N}.
func (h *Handlers) InternalNormalizePlatforms(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	// Reads bearerToken directly, not caller(): a service token's subject
	// (svc:<name>) is not a uuid and would trip caller's internalError branch.
	bearer := bearerToken(r)
	platforms, err := h.enrichment.ListPlatforms(r.Context(), bearer)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the platform catalog cannot be reached")
		return
	}
	type canon struct {
		igdbID int64
		name   string
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	byKey := make(map[string]canon, len(platforms)*2)
	for _, p := range platforms {
		byKey[norm(p.Name)] = canon{p.IGDBID, p.Name}
		for _, a := range p.Aliases {
			if _, taken := byKey[norm(a)]; !taken {
				byKey[norm(a)] = canon{p.IGDBID, p.Name}
			}
		}
	}
	refs, err := h.store.ListNameOnlyPlatformEntries(r.Context())
	if err != nil {
		h.internalError(w, r, "normalize_platforms_list", "list failed", err)
		return
	}
	var normalized, skipped int
	for _, ref := range refs {
		c, matched := byKey[norm(ref.PlatformName)]
		if !matched {
			skipped++
			h.countNormalizePlatformsRow(r.Context(), "skipped")
			continue
		}
		if err := h.store.SetEntryPlatformIdentity(r.Context(), ref.EntryID, c.igdbID, c.name); err != nil {
			h.logger.WarnContext(r.Context(), "normalize: entry update failed", "entry", ref.EntryID, "err", err)
			h.countNormalizePlatformsRow(r.Context(), "failed")
			continue
		}
		normalized++
		h.countNormalizePlatformsRow(r.Context(), "normalized")
	}
	h.logger.InfoContext(r.Context(), "normalize-platforms complete",
		"scanned", len(refs), "normalized", normalized, "skipped", skipped)
	writeJSON(w, http.StatusOK, map[string]int{
		"scanned": len(refs), "normalized": normalized, "skipped": skipped,
	})
}

// InternalNormalizeRegions promotes free-text entry regions outside
// regionkit.KnownRegions via exact-or-synonym fold against
// regionkit.RegionSynonyms, never fuzzy; unreviewed strings stay as typed.
// A custom entry gets a plain region write; a game-backed entry also re-picks
// its release date and localized snapshot via a fresh GetProduct fetch. An
// enrichment outage skips that row for the next run rather than failing the
// sweep (no whole-run 502). Re-runnable; "normalized" counts an error-free
// write, not a confirmed row change (RowsAffected check skipped). Admin-or-service
// gated; the nightly job runs this ahead of the entry rematch.
//
// Offline test: kubectl port-forward svc/collection 8085:8080, mint a token via
// POST :8082/oauth/dev/token (task grant-fixture-admin), then POST
// :8085/internal/normalize-regions with it. Answers {"scanned":N,"normalized":N,"skipped":N}.
func (h *Handlers) InternalNormalizeRegions(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	// Reads bearerToken directly rather than calling caller(): see the
	// same note on InternalNormalizePlatforms above.
	bearer := bearerToken(r)
	known := make([]string, 0, len(regionkit.KnownRegions))
	for k := range regionkit.KnownRegions {
		known = append(known, k)
	}
	folds := regionkit.RegionFoldMap()
	refs, err := h.store.ListOpenRegionEntries(r.Context(), known)
	if err != nil {
		h.internalError(w, r, "normalize_regions_list", "list failed", err)
		return
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	var normalized, skipped int
	for _, ref := range refs {
		canon, matched := folds[norm(ref.Region)]
		if !matched {
			skipped++
			h.countNormalizeRegionsRow(r.Context(), "skipped")
			continue
		}
		if ref.ProductID != nil && ref.IGDBGameID != nil {
			product, err := h.enrichment.GetProduct(r.Context(), bearer, *ref.ProductID)
			if err != nil {
				h.logger.WarnContext(r.Context(), "normalize regions: product fetch failed",
					"entry", ref.EntryID, "err", err)
				skipped++
				h.countNormalizeRegionsRow(r.Context(), "failed")
				continue
			}
			d := pickReleaseDate(product.Igdb, canon)
			name, translit, cover := pickLocalization(product.Igdb, canon)
			if err := h.store.PromoteEntryRegionSnapshot(r.Context(), ref.EntryID, canon, d, name, translit, cover); err != nil {
				h.logger.WarnContext(r.Context(), "normalize regions: entry update failed", "entry", ref.EntryID, "err", err)
				h.countNormalizeRegionsRow(r.Context(), "failed")
				continue
			}
		} else if err := h.store.PromoteEntryRegion(r.Context(), ref.EntryID, canon); err != nil {
			h.logger.WarnContext(r.Context(), "normalize regions: entry update failed", "entry", ref.EntryID, "err", err)
			h.countNormalizeRegionsRow(r.Context(), "failed")
			continue
		}
		h.logger.InfoContext(r.Context(), "normalize regions: promoted",
			"entry", ref.EntryID, "from", ref.Region, "to", canon)
		normalized++
		h.countNormalizeRegionsRow(r.Context(), "normalized")
	}
	h.logger.InfoContext(r.Context(), "normalize-regions complete",
		"scanned", len(refs), "normalized", normalized, "skipped", skipped)
	writeJSON(w, http.StatusOK, map[string]int{
		"scanned": len(refs), "normalized": normalized, "skipped": skipped,
	})
}

func strSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
