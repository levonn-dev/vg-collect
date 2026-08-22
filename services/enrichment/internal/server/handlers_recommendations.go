// Recommendation scoring against a caller-supplied library summary.

package server

import (
	"context"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/recs"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

const (
	recsDefaultLimit = 20
	// recsCandidateCap bounds the metadata-fetch budget per request.
	recsCandidateCap = 200
	recsTopGenres    = 3
)

// ScoreRecommendations scores unowned games against the caller's
// library summary. User-agnostic: nothing here is stored per-user;
// igdb_raw is the shared metadata cache that fetches populate.
func (h *Handlers) ScoreRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.ScoreRequest
	if !httpkit.DecodeBody(w, r, 256*1024, &req) {
		return
	}
	// limit is already known within the contract's 1-50 bound by the
	// time this runs (specval; ScoreRequest.limit carries minimum 1,
	// maximum 50 in api/enrichment.yaml). Only the default-when-absent
	// case needs handling here.
	limit := recsDefaultLimit
	if req.Limit != nil {
		limit = *req.Limit
	}

	lib := make([]recs.LibraryGame, 0, len(req.Library))
	owned := make([]int64, 0, len(req.Library))
	for _, e := range req.Library {
		g := recs.LibraryGame{GameID: e.IgdbGameId, Rating: e.Rating}
		if e.Status != nil {
			g.Status = *e.Status
		}
		lib = append(lib, g)
		owned = append(owned, e.IgdbGameId)
	}
	if len(lib) == 0 {
		writeJSON(w, http.StatusOK, api.ScoreResponse{Degraded: false, Recommendations: []common.Recommendation{}})
		return
	}

	// Metadata for the owned games (edges + genres).
	raw, degraded, err := h.ensureRaw(ctx, owned)
	if err != nil {
		h.internalError(w, r, "recs_owned_metadata", "metadata lookup failed", err)
		return
	}
	meta := make(map[int64]recs.Meta, len(raw))
	for id, rg := range raw {
		meta[id] = toRecsMeta(rg)
	}

	// Candidates from edges, then their metadata.
	cands := recs.CandidateIDs(lib, meta, recsCandidateCap)
	candRaw, candDegraded, err := h.ensureRaw(ctx, cands)
	if err != nil {
		h.internalError(w, r, "recs_candidate_metadata", "metadata lookup failed", err)
		return
	}
	degraded = degraded || candDegraded
	for id, rg := range candRaw {
		meta[id] = toRecsMeta(rg)
	}

	// Sparse edges: top up from the genre profile.
	if len(cands) < limit {
		genres := recs.TopGenres(lib, meta, recsTopGenres)
		if len(genres) > 0 {
			exclude := append(append([]int64{}, owned...), cands...)
			pop, err := h.games.PopularGames(ctx, genres, exclude, limit*2)
			if err != nil {
				h.logger.WarnContext(ctx, "genre-profile fallback unavailable", "err", err)
				degraded = true
			} else {
				now := h.now()
				if err := h.store.UpsertRaw(ctx, pop, now); err != nil {
					h.logger.WarnContext(ctx, "raw upsert failed", "err", err)
				}
				for _, g := range pop {
					if _, ok := meta[g.ID]; !ok {
						cands = append(cands, g.ID)
					}
					meta[g.ID] = toRecsMeta(store.RawGame{GameID: g.ID, Game: g, FetchedAt: now})
					candRaw[g.ID] = store.RawGame{GameID: g.ID, Game: g, FetchedAt: now}
				}
			}
		}
	}

	scored := recs.Score(lib, meta, cands)
	out := make([]common.Recommendation, 0, limit)
	for _, sc := range scored {
		rg, ok := candRaw[sc.GameID]
		if !ok {
			// Candidate the provider no longer knows: skip (already
			// reflected in degraded when a fetch failed).
			continue
		}
		rec := common.Recommendation{
			IgdbGameId: sc.GameID,
			Name:       rg.Game.Name,
			Genres:     genreNames(rg.Game),
			Score:      sc.Score,
		}
		if cu := rg.Game.CoverURL(); cu != "" {
			rec.CoverUrl = &cu
		}
		if d := rg.Game.ReleaseDate(); !d.IsZero() {
			fd := openapi_types.Date{Time: d}
			rec.FirstReleaseDate = &fd
		}
		out = append(out, rec)
		if len(out) == limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, api.ScoreResponse{Degraded: degraded, Recommendations: out})
}

func genreNames(g igdb.Game) []string {
	out := make([]string, 0, len(g.Genres))
	for _, gen := range g.Genres {
		out = append(out, gen.Name)
	}
	return out
}

func toRecsMeta(rg store.RawGame) recs.Meta {
	genres := make([]int64, 0, len(rg.Game.Genres))
	for _, g := range rg.Game.Genres {
		genres = append(genres, g.ID)
	}
	return recs.Meta{Similar: rg.Game.SimilarGames, GenreIDs: genres}
}

// ensureRaw returns raw payloads for ids, reading igdb_raw first and
// fetching gaps through the provider (populated backwards). The bool
// reports a failed gap-fetch (degraded); ids the provider does not
// know stay absent silently.
func (h *Handlers) ensureRaw(ctx context.Context, ids []int64) (map[int64]store.RawGame, bool, error) {
	out := make(map[int64]store.RawGame, len(ids))
	if len(ids) == 0 {
		return out, false, nil
	}
	raws, err := h.store.RawByIDs(ctx, ids)
	if err != nil {
		return nil, false, err
	}
	for _, rg := range raws {
		out[rg.GameID] = rg
	}
	var missing []int64
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out, false, nil
	}
	games, err := h.games.GamesByIDs(ctx, missing)
	if err != nil {
		h.logger.WarnContext(ctx, "candidate metadata fetch failed; scoring without it", "missing", len(missing), "err", err)
		return out, true, nil
	}
	now := h.now()
	if err := h.store.UpsertRaw(ctx, games, now); err != nil {
		h.logger.WarnContext(ctx, "raw upsert failed", "err", err)
	}
	for _, g := range games {
		out[g.ID] = store.RawGame{GameID: g.ID, Game: g, FetchedAt: now}
	}
	return out, false, nil
}
