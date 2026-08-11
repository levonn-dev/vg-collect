// Admin and CronJob levers: the catalog refresh trigger and its
// price, reprojection and candidate-sweep steps, and community region
// normalization.

package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// refreshBudget bounds a detached catalog refresh (well beyond a full
// catalog at polite provider rates).
const refreshBudget = 30 * time.Minute

// TriggerRefresh is the admin's immediate-refresh trigger.
func (h *Handlers) TriggerRefresh(w http.ResponseWriter, r *http.Request) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	h.startRefresh(w, r, "admin")
}

// InternalRefresh is the CronJob's trigger. Contract-described and
// served by the generated mux behind the blanket JWT middleware,
// service-token-gated in the handler (operators use TriggerRefresh's
// admin-role gate on /admin/refresh instead); the NetworkPolicy is the
// outer layer.
func (h *Handlers) InternalRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w, r) {
		return
	}
	h.startRefresh(w, r, "internal")
}

// startRefresh answers 202 and detaches the catalog refresh: at
// polite provider rates a real catalog outlives the server's write
// timeout, so the summary goes to the log, not the response. One
// refresh at a time.
func (h *Handlers) startRefresh(w http.ResponseWriter, r *http.Request, trigger string) {
	if !h.refreshing.CompareAndSwap(false, true) {
		problem(w, r, http.StatusConflict, "refresh_in_progress", "a catalog refresh is already running")
		return
	}
	// The started line pairs with the per-step finished summaries: a
	// start with no finishes inside the budget marks a hung refresh.
	h.logger.InfoContext(r.Context(), "catalog refresh started", "trigger", trigger)
	go func() { //nolint:gosec // G118: the refresh run deliberately outlives the trigger request (202 + detach)
		defer h.refreshing.Store(false)
		// Detached from the request context: the trigger returns at 202.
		ctx, cancel := context.WithTimeout(context.Background(), refreshBudget)
		defer cancel()
		// Registered last so it unwinds first: a panic in the refresh
		// run (a malformed doc, a nil field breaking an assumed
		// contract) is contained here instead of killing the process.
		// The guard reset and context cancel above still run afterward
		// as usual. The CronJob retries daily, so an uncontained panic
		// on a persistently bad doc would otherwise crash-loop.
		defer func() {
			if v := recover(); v != nil {
				h.logger.ErrorContext(ctx, "catalog refresh panicked", "panic", v)
			}
		}()
		h.runRefresh(ctx)
		// Every igdb-bearing product's projection is rebuilt from its
		// raw payload on what remains of the budget; only changed
		// projections are written, so steady state costs no provider
		// calls and any future projection-logic change self-deploys.
		h.runReprojection(ctx)
		h.runCandidateSweep(ctx)
	}()
	writeJSON(w, http.StatusAccepted, api.RefreshAccepted{Status: "started"})
}

// runRefresh walks every mapped product: current prices updated, one
// snapshot appended, failures counted and skipped (the walk finishes
// what it can). Orphaned products keep snapshotting by design. Once
// the budget expires, the ctx.Err() check between products stops the
// walk instead of burning a failure for every remaining product.
func (h *Handlers) runRefresh(ctx context.Context) {
	start := h.now()
	defer func() { h.recordRefreshStepDuration(ctx, "prices", h.now().Sub(start).Seconds()) }()
	prods, err := h.store.ListPriced(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "price refresh aborted", "err", err)
		return
	}
	var updated, snapshots, failures, processed int
	for _, p := range prods {
		if err := ctx.Err(); err != nil {
			h.logger.WarnContext(ctx, "price refresh stopped early: context done",
				"processed", processed, "remaining", len(prods)-processed, "err", err)
			break
		}
		processed++
		pc, err := h.prices.Product(ctx, p.PriceCharting.PCProductID)
		if err != nil {
			failures++
			h.countRefreshItem(ctx, "prices", "failed")
			h.logger.WarnContext(ctx, "refresh: price fetch failed", "product", p.ID, "pc_product", p.PriceCharting.PCProductID, "err", err)
			continue
		}
		q := quoteOf(pc)
		asOf := h.now()
		if err := h.store.SetCurrentPrices(ctx, p.ID, q, asOf); err != nil {
			failures++
			h.countRefreshItem(ctx, "prices", "failed")
			h.logger.WarnContext(ctx, "refresh: price update failed", "product", p.ID, "err", err)
			continue
		}
		updated++
		if err := h.store.AppendSnapshot(ctx, store.Snapshot{
			ProductID: p.ID, CapturedAt: asOf,
			LooseCents: q.LooseCents, CIBCents: q.CIBCents, NewCents: q.NewCents,
		}); err != nil {
			failures++
			h.countRefreshItem(ctx, "prices", "failed")
			h.logger.WarnContext(ctx, "refresh: snapshot failed", "product", p.ID, "err", err)
			continue
		}
		snapshots++
		// ok means the full item landed: price written and snapshot
		// appended (a failed invalidate below is a cache event, not an
		// item failure).
		h.countRefreshItem(ctx, "prices", "ok")
		if err := h.cache.InvalidateProduct(ctx, p.ID); err != nil {
			h.failOpen(ctx, "refresh_invalidate", err)
		}
	}
	h.logger.InfoContext(ctx, "price refresh finished",
		"processed", processed, "updated", updated, "snapshots", snapshots,
		"failures", failures, "duration_ms", h.now().Sub(start).Milliseconds())
}

// runReprojection sweeps every igdb-bearing product nightly (an
// uncapped ListIGDBProducts read, mirroring the price refresh's posture)
// and rebuilds each one's projection from its raw payload, writing only
// the ones that actually changed. The raws hold the full unfiltered
// release table, so a projection-logic change (like the JP-twin fold)
// redeploys here with zero provider calls; only raws below
// fields_version (nil release table, or missing fields a newer
// generation added) or ids with no raw at all are refetched - a set
// that drains to zero as the catalog heals, which bounds the provider
// cost of a full sweep. A rebuild sourced from an existing raw keeps
// that raw's fetch stamp - the projection changed, not the provider
// data - so read-path staleness math stays honest. The diff gate
// (SameProjection) makes steady state write-free: once every raw is
// healed and every projection matches, the nightly sweep reads Mongo
// and writes nothing.
// Detached-execution conventions match runRefresh.
func (h *Handlers) runReprojection(ctx context.Context) {
	start := h.now()
	defer func() { h.recordRefreshStepDuration(ctx, "reprojection", h.now().Sub(start).Seconds()) }()
	prods, err := h.store.ListIGDBProducts(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "reprojection aborted", "err", err)
		return
	}
	if len(prods) == 0 {
		return
	}

	// Distinct game ids, then the raws we already hold. A raw below
	// fields_version - nil release table, or missing fields a newer
	// generation added - must be refetched, never reprojected as-is; an
	// id with no raw at all is fetched too.
	ids := make([]int64, 0, len(prods))
	seen := make(map[int64]bool, len(prods))
	for _, p := range prods {
		if p.IGDB != nil && !seen[p.IGDB.GameID] {
			seen[p.IGDB.GameID] = true
			ids = append(ids, p.IGDB.GameID)
		}
	}
	raws, err := h.store.RawByIDs(ctx, ids)
	if err != nil {
		h.logger.ErrorContext(ctx, "reprojection aborted: raw read failed", "err", err)
		return
	}
	rawByID := make(map[int64]store.RawGame, len(raws))
	for _, rg := range raws {
		rawByID[rg.GameID] = rg
	}
	var fetchIDs []int64
	for _, id := range ids {
		if rg, ok := rawByID[id]; !ok || rg.Game.ReleaseDates == nil || rg.FieldsVersion < store.RawFieldsVersion {
			fetchIDs = append(fetchIDs, id)
		}
	}
	now := h.now()
	var fetched int
	if len(fetchIDs) > 0 {
		games, gerr := h.games.GamesByIDs(ctx, fetchIDs)
		if gerr != nil {
			h.logger.ErrorContext(ctx, "reprojection aborted: refetch failed", "err", gerr)
			return
		}
		fetched = len(games)
		if err := h.store.UpsertRaw(ctx, games, now); err != nil {
			h.logger.ErrorContext(ctx, "reprojection aborted: raw upsert failed", "err", err)
			return
		}
		for _, g := range games {
			// Match UpsertRaw's persisted shape: a fetched game listing no
			// rows is fetched-none ([]), not pre-feature (nil), so the
			// loop below reprojects rather than re-skips it.
			if g.ReleaseDates == nil {
				g.ReleaseDates = []igdb.ReleaseDate{}
			}
			rawByID[g.ID] = store.RawGame{GameID: g.ID, Game: g, FetchedAt: now, FieldsVersion: store.RawFieldsVersion}
		}
	}

	var processed, rebuilt, missing, failures int
	for _, p := range prods {
		if err := ctx.Err(); err != nil {
			h.logger.WarnContext(ctx, "reprojection stopped early: context done",
				"processed", processed, "remaining", len(prods)-processed, "err", err)
			break
		}
		processed++
		// Defensive: ListIGDBProducts filters on the igdb subdoc, so a
		// nil projection here should not happen - skip it rather than
		// deref, for loop-consistency.
		if p.IGDB == nil {
			missing++
			h.countRefreshItem(ctx, "reprojection", "skipped")
			h.logger.WarnContext(ctx, "reprojection: product matched the igdb filter but carries a nil projection",
				"product", p.ID)
			continue
		}
		// A missing raw (provider never returned it) or one still on the
		// pre-feature nil table (a refetch the provider could not honor)
		// carries no honest release data: skip rather than reproject a
		// nil table as a fetched-none empty. The next reprojection retries.
		raw, ok := rawByID[p.IGDB.GameID]
		if !ok || raw.Game.ReleaseDates == nil {
			missing++
			h.countRefreshItem(ctx, "reprojection", "skipped")
			h.logger.WarnContext(ctx, "reprojection: no usable raw for product",
				"product", p.ID, "game", p.IGDB.GameID)
			continue
		}
		var pid int64
		if p.Platform != nil {
			pid = p.Platform.IGDBID
		}
		// raw.FetchedAt is the honest stamp: freshly fetched raws carry
		// `now` (set at merge above); existing raws keep their stored
		// stamp, so a projection-only rebuild does not fake freshness.
		meta := store.NewIGDBMeta(raw.Game, pid, raw.FetchedAt)
		if meta.SameProjection(*p.IGDB) {
			// diff gate: nothing changed, no write, no invalidate
			h.countRefreshItem(ctx, "reprojection", "skipped")
			continue
		}
		if err := h.store.SetIGDB(ctx, p.ID, meta); err != nil {
			failures++
			h.countRefreshItem(ctx, "reprojection", "failed")
			h.logger.WarnContext(ctx, "reprojection: projection update failed", "product", p.ID, "err", err)
			continue
		}
		rebuilt++
		h.countRefreshItem(ctx, "reprojection", "ok")
		if err := h.cache.InvalidateProduct(ctx, p.ID); err != nil {
			h.failOpen(ctx, "reprojection_invalidate", err)
		}
	}
	h.logger.InfoContext(ctx, "reprojection finished",
		"processed", processed, "rebuilt", rebuilt, "fetched", fetched,
		"missing", missing, "failures", failures, "duration_ms", h.now().Sub(start).Milliseconds())
}

// runCandidateSweep is the catalog refresh's community pass: for each community
// product, name-search the promote-relevant provider (games need igdb
// identity to promote, hardware needs a listing) and stash flag-only
// candidates at the same never-guess threshold. Never attaches: a
// repro shares its name with the original it reproduces, so a
// high-scoring match has an elevated false-positive base rate here -
// providers propose, admins decide. Dismissed pairs stay silent.
func (h *Handlers) runCandidateSweep(ctx context.Context) {
	start := h.now()
	defer func() { h.recordRefreshStepDuration(ctx, "sweep", h.now().Sub(start).Seconds()) }()
	comm, err := h.store.ListCommunityProducts(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "candidate sweep: list failed", "err", err)
		return
	}
	var swept, flagged, failed int
	for _, p := range comm {
		if ctx.Err() != nil {
			h.logger.WarnContext(ctx, "candidate sweep: budget exhausted", "swept", swept)
			return
		}
		swept++
		dismissed := make(map[string]bool, len(p.DismissedCandidates))
		for _, d := range p.DismissedCandidates {
			dismissed[fmt.Sprintf("%s:%d", d.Provider, d.ProviderID)] = true
		}
		var cands []store.PromoteCandidate
		switch p.Type {
		case "game":
			games, gerr := h.games.SearchGames(ctx, p.Name, searchLimit)
			if gerr != nil {
				failed++
				h.countRefreshItem(ctx, "sweep", "failed")
				continue
			}
			for _, g := range games {
				if dismissed[fmt.Sprintf("igdb:%d", g.ID)] {
					continue
				}
				if sc := match.Score(p.Name, g.Name); sc >= match.Threshold {
					cands = append(cands, store.PromoteCandidate{
						Provider: "igdb", ProviderID: g.ID, Name: g.Name, Score: sc, FoundAt: h.now(),
					})
				}
			}
		case "console", "accessory":
			prods, perr := h.prices.Search(ctx, p.Name)
			if perr != nil {
				failed++
				h.countRefreshItem(ctx, "sweep", "failed")
				continue
			}
			for _, pc := range prods {
				if dismissed[fmt.Sprintf("pricecharting:%d", pc.ID)] {
					continue
				}
				if sc := match.Score(p.Name, pc.Name); sc >= match.Threshold {
					cands = append(cands, store.PromoteCandidate{
						Provider: "pricecharting", ProviderID: pc.ID, Name: pc.Name, Score: sc, FoundAt: h.now(),
					})
				}
			}
		}
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })
		if err := h.store.ReplacePromoteCandidates(ctx, p.ID, cands); err != nil {
			failed++
			h.countRefreshItem(ctx, "sweep", "failed")
			h.logger.WarnContext(ctx, "candidate sweep: store failed", "product", p.ID, "err", err)
			continue
		}
		if len(cands) > 0 {
			flagged++
			h.countRefreshItem(ctx, "sweep", "flagged")
		} else {
			h.countRefreshItem(ctx, "sweep", "ok")
		}
	}
	h.logger.InfoContext(ctx, "candidate sweep complete", "swept", swept, "flagged", flagged, "failed", failed)
}

// InternalNormalizeCommunityRegions promotes free-text community
// product regions into the known set: every community product whose
// curated community.region sits outside knownRegions is folded
// (lowercase, trimmed) against the known values and regionSynonyms -
// exact-or-synonym, never fuzzy, so an unreviewed string is left as
// typed rather than misfiled. This is enrichment's twin of
// collection's normalize-regions lever, scoped to the community
// products this service owns, but with no fetch arm: a community
// product carries no provider identity to re-fetch and no release-
// date/localization snapshot to re-pick, so promotion is a plain
// community.region field rewrite (no 502 - nothing here calls out to
// another service). Re-runnable: promoted rows leave the selection
// set - though "normalized" here counts an error-free write, not a
// confirmed row change (SetCommunityRegion skips the matched-count
// check, the same store-tier convention as collection's
// PromoteEntryRegion); a write failure logs and counts only in the
// failed metric outcome, so scanned can exceed normalized+skipped.
// Guard: admin role or service token (the nightly job runs this
// alongside collection's platform/region levers).
//
// Contract-described; the bff relays POST /api/admin/normalize-community-regions
// to this endpoint via the Admin page button, and the gateway publishes the relay
// path. For offline testing against the enrichment service directly, with
// the dev stack up and the admin fixture role already granted (task
// grant-fixture-admin):
//
//	kubectl -n vgkeep port-forward svc/enrichment 8086:8080 &
//	TOKEN=$(curl -s -X POST http://localhost:8082/oauth/dev/token \
//	  -H 'Content-Type: application/json' -d '{"user":"admin"}' \
//	  | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
//	curl -s -X POST http://localhost:8086/internal/normalize-community-regions \
//	  -H "Authorization: Bearer $TOKEN"
//
// Answers {"scanned":N,"normalized":N,"skipped":N}.
func (h *Handlers) InternalNormalizeCommunityRegions(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	ctx := r.Context()
	refs, err := h.store.ListCommunityRegionDocs(ctx)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	folds := regionFoldMap()
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	var normalized, skipped int
	for _, ref := range refs {
		canon, matched := folds[norm(ref.Region)]
		if !matched {
			skipped++
			h.countNormalizeCommunityRegions(ctx, "skipped")
			continue
		}
		if err := h.store.SetCommunityRegion(ctx, ref.ID, canon); err != nil {
			h.logger.WarnContext(ctx, "normalize community regions: write failed", "product", ref.ID, "err", err)
			h.countNormalizeCommunityRegions(ctx, "failed")
			continue
		}
		h.logger.InfoContext(ctx, "normalize community regions: promoted",
			"product", ref.ID, "from", ref.Region, "to", canon)
		normalized++
		h.countNormalizeCommunityRegions(ctx, "normalized")
	}
	h.logger.InfoContext(ctx, "normalize-community-regions complete",
		"scanned", len(refs), "normalized", normalized, "skipped", skipped)
	writeJSON(w, http.StatusOK, map[string]int{
		"scanned": len(refs), "normalized": normalized, "skipped": skipped,
	})
}
