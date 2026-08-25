// Admin and CronJob levers: the catalog refresh trigger and its
// price, reprojection and candidate-sweep steps, and community region normalization.

package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
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
	if !h.requireAdmin(w, r) {
		return
	}
	h.startRefresh(w, r, "admin")
}

// InternalRefresh is the CronJob's trigger: service-token-gated in
// the handler (operators use TriggerRefresh's admin-role gate
// instead); the NetworkPolicy is the outer layer.
func (h *Handlers) InternalRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w, r) {
		return
	}
	h.startRefresh(w, r, "internal")
}

// startRefresh answers 202 and detaches the catalog refresh via
// httpkit.TriggerDetached: it outlives the write timeout at polite
// provider rates, so the summary goes to the log. One at a time (409 on conflict).
func (h *Handlers) startRefresh(w http.ResponseWriter, r *http.Request, trigger string) {
	started := httpkit.TriggerDetached(w, r, httpkit.TriggerDetachedOptions{
		Guard:          &h.refreshing,
		ConflictCode:   "refresh_in_progress",
		ConflictDetail: "a catalog refresh is already running",
		Started: func() {
			// Pairs with the per-step finished summaries: a start with no
			// finishes inside the budget marks a hung refresh.
			h.logger.InfoContext(r.Context(), "catalog refresh started", "trigger", trigger)
		},
		Budget:   refreshBudget,
		Logger:   h.logger,
		PanicMsg: "catalog refresh panicked",
		Run: func(ctx context.Context) {
			h.runRefresh(ctx)
			// Every igdb-bearing product's projection is rebuilt from its
			// raw payload on what remains of the budget; only changed
			// projections are written, so steady state costs no provider
			// calls and any future projection-logic change self-deploys.
			h.runReprojection(ctx)
			h.runCandidateSweep(ctx)
		},
	})
	if !started {
		return
	}
	writeJSON(w, http.StatusAccepted, api.RefreshAccepted{Status: "started"})
}

// runRefresh walks every mapped product: prices updated, one snapshot
// appended, failures counted and skipped. Orphaned products keep
// snapshotting by design. On budget expiry, ctx.Err() stops the walk
// instead of failing every remaining product.
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
		// "ok" means price written + snapshot appended; a failed
		// invalidate below is a cache event, not an item failure.
		h.countRefreshItem(ctx, "prices", "ok")
		if err := h.cache.InvalidateProduct(ctx, p.ID); err != nil {
			h.failOpen(ctx, "refresh_invalidate", err)
		}
	}
	h.logger.InfoContext(ctx, "price refresh finished",
		"processed", processed, "updated", updated, "snapshots", snapshots,
		"failures", failures, "duration_ms", h.now().Sub(start).Milliseconds())
}

// runReprojection nightly-sweeps every igdb-bearing product (uncapped,
// like the price refresh), rebuilding each projection from raw and
// writing only diffs (SameProjection gates steady state write-free).
// Only stale/missing raws (below fields_version) refetch, a set that
// drains to zero as the catalog heals. A rebuild keeps the raw's
// existing fetch stamp, so staleness math stays honest.
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

	// Below fields_version, missing, or no raw at all: refetch, don't reproject as-is.
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
			// Match UpsertRaw's shape: a fetched listing with no rows is
			// fetched-none ([]), not pre-feature (nil), so it reprojects, not re-skips.
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
		// Defensive: ListIGDBProducts filters on igdb subdoc, so nil
		// here shouldn't happen; skip rather than deref.
		if p.IGDB == nil {
			missing++
			h.countRefreshItem(ctx, "reprojection", "skipped")
			h.logger.WarnContext(ctx, "reprojection: product matched the igdb filter but carries a nil projection",
				"product", p.ID)
			continue
		}
		// A missing raw, or one still nil-table after refetch, carries
		// no honest release data: skip it; the next reprojection retries.
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
		// raw.FetchedAt is honest: fresh raws carry `now`, existing raws
		// keep their stored stamp, so a rebuild never fakes freshness.
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

// runCandidateSweep name-searches each community product's
// promote-relevant provider and stashes flag-only candidates at the
// match threshold; never attaches (a repro shares its name with the
// original, an elevated false-positive risk). Dismissed pairs stay silent.
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

// InternalNormalizeCommunityRegions folds each community region
// outside regionkit.KnownRegions against known values and
// regionkit.RegionSynonyms (exact-or-synonym only, never fuzzy) and
// rewrites it in place; no fetch arm since community products carry
// no provider identity. Re-runnable; "normalized" counts an
// error-free write, not a confirmed row change, so scanned can exceed
// normalized+skipped. Guard: admin role or service token.
//
// Answers {"scanned":N,"normalized":N,"skipped":N}.
func (h *Handlers) InternalNormalizeCommunityRegions(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminOrService(w, r) {
		return
	}
	ctx := r.Context()
	known := make([]string, 0, len(regionkit.KnownRegions))
	for k := range regionkit.KnownRegions {
		known = append(known, k)
	}
	refs, err := h.store.ListCommunityRegionDocs(ctx, known)
	if err != nil {
		h.internalError(w, r, "normalize_regions_list", "list failed", err)
		return
	}
	folds := regionkit.RegionFoldMap()
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
