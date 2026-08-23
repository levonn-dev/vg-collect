// Admin and service-token levers: user-data purge, the product-
// reference count, resnapshot, the entry rematch, and platform and
// region normalization.

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
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
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
		h.internalError(w, r, "purge failed", err)
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	w.WriteHeader(http.StatusNoContent)
}

// CountProductReferences is the admin delete's safety read: only this
// service can see entries, so the bff asks here - across all users -
// before asking enrichment to delete a product.
func (h *Handlers) CountProductReferences(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	n, err := h.store.CountEntriesByProduct(r.Context(), productId)
	if err != nil {
		h.internalError(w, r, "count failed", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		EntryCount int64 `json:"entry_count"`
	}{n})
}

// InternalResnapshot recomputes every game-backed entry's
// product-derived snapshot fields: the region-picked release date,
// the localized presentation trio (name, transliteration, cover
// url), and the credit arrays (developers, publishers).
// Contract-described and served by the generated mux behind the
// blanket JWT middleware, admin-or-service-gated in the handler
// (same guard as the entry rematch); the caller's bearer rides the
// enrichment hops. Idempotent and re-runnable - rows are written only
// when a recomputed field differs from what is stored.
func (h *Handlers) InternalResnapshot(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	bearer := bearerToken(r)
	refs, err := h.store.ListGameBackedRefs(r.Context())
	if err != nil {
		h.internalError(w, r, "list failed", err)
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
		// Credits are game identity, not region-scoped: one derive
		// serves every entry in the product's group.
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

// InternalRematchEntries is the entry rematch: it re-resolves
// auto-priced game-backed entries with their region so each points
// at its region-correct sibling member (a JP copy's price is a
// different listing). Grouped by (game, platform, region) - one
// resolve per triple - with the console-class guard applied per
// entry, so class-compatible members skip, and user-picked matches
// (match_provenance) are never swept at all. Idempotent and
// re-runnable; this rematch is the backfill for entries that predate
// region-aware matching. Contract-described and served by the
// generated mux behind the blanket JWT middleware, admin-or-service-
// gated in the handler; the caller's bearer rides the enrichment
// hops, captured before the run detaches. Triggering goes through
// httpkit.TriggerDetached: one run at a time (409 rematch_in_progress
// on conflict, mirrors the catalog refresh's guard), 202 immediately,
// sweep detached on its own 30-minute budget - a day-one backfill
// resolves at the provider's polite rate (1 req/s), which outlives
// httpkit's 30s write timeout on exactly the runs that matter. Each
// successful repoint logs entry and old->new product ids - the audit
// trail that keeps a run reviewable and hand-reversible; the
// completion log and the rematch.* metrics carry the run's three
// counts, not the 202 response.
func (h *Handlers) InternalRematchEntries(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	// Captured here, before detaching: the goroutine reuses it for the
	// enrichment hops, and the request itself is not safe to read from
	// once this handler has returned.
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

// runRematch is the entry rematch's sweep body, detached from its
// trigger request by InternalRematchEntries: ctx is the detached,
// budget-bound context and bearer was captured from the trigger
// request before the goroutine started.
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

// InternalNormalizePlatforms canonicalizes free-text custom-entry
// platforms: every entry with a platform_name but no platform_igdb_id
// is matched (case-insensitive, trimmed, exact-or-alias - never fuzzy,
// so nothing is silently misfiled) against the enrichment platform
// catalog and stamped with the canonical igdb id + name. Re-runnable:
// stamped rows leave the selection set. Guard: admin role or service
// token, since the nightly job runs this alongside normalize-regions
// and the entry rematch; the mass write stays reviewable through its
// counts and logs. The caller's bearer rides the enrichment hop.
//
// Contract-described like the other internal levers; the bff relays
// POST /api/admin/normalize-platforms to this endpoint via the Admin page
// button, and the gateway publishes the relay path. For offline testing
// against the collection service directly, with the dev stack up and the
// admin fixture role already granted (task grant-fixture-admin):
//
//	kubectl -n vgkeep port-forward svc/collection 8085:8080 &
//	TOKEN=$(curl -s -X POST http://localhost:8082/oauth/dev/token \
//	  -H 'Content-Type: application/json' -d '{"user":"admin"}' \
//	  | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
//	curl -s -X POST http://localhost:8085/internal/normalize-platforms \
//	  -H "Authorization: Bearer $TOKEN"
//
// Answers {"scanned":N,"normalized":N,"skipped":N}.
func (h *Handlers) InternalNormalizePlatforms(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	// Reads bearerToken directly rather than calling caller(), same as
	// resnapshot and the entry rematch: a service token's subject
	// (svc:<name>) is not a uuid and would trip caller's internalError
	// branch. See caller's doc comment.
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
		h.internalError(w, r, "list failed", err)
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

// InternalNormalizeRegions promotes free-text entry regions into the
// known set: every entry whose region sits outside
// regionkit.KnownRegions is folded (lowercase, trimmed) against the
// known values and regionkit.RegionSynonyms - exact-or-synonym, never
// fuzzy, so an unreviewed string is left as typed rather than
// misfiled. A custom entry (no
// product) gets a plain region write; a game-backed entry additionally
// re-picks its release-date and localized snapshot for the promoted
// region from a fresh product fetch, the same GetProduct hop the
// region-edit arm of UpdateEntry uses - an enrichment outage skips
// that row for the next run rather than failing the whole sweep (this
// lever has no platform-catalog dependency, so no whole-run 502).
// Re-runnable: promoted rows leave the selection set - though
// "normalized" here counts an error-free write, not a confirmed row
// change: PromoteEntryRegion/PromoteEntryRegionSnapshot skip the
// RowsAffected check (store-tier convention, same as RepointEntry),
// so a row that vanished mid-sweep still increments it. Guard: admin
// role or service token (the nightly job runs this ahead of the entry
// rematch, so a promoted region's pricing class corrects the same
// night); the caller's bearer rides the one enrichment hop.
//
// Contract-described like the other internal levers; the bff relays
// POST /api/admin/normalize-regions to this endpoint via the Admin page
// button, and the gateway publishes the relay path. For offline testing
// against the collection service directly, with the dev stack up and the
// admin fixture role already granted (task grant-fixture-admin):
//
//	kubectl -n vgkeep port-forward svc/collection 8085:8080 &
//	TOKEN=$(curl -s -X POST http://localhost:8082/oauth/dev/token \
//	  -H 'Content-Type: application/json' -d '{"user":"admin"}' \
//	  | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
//	curl -s -X POST http://localhost:8085/internal/normalize-regions \
//	  -H "Authorization: Bearer $TOKEN"
//
// Answers {"scanned":N,"normalized":N,"skipped":N}.
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
		h.internalError(w, r, "list failed", err)
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
