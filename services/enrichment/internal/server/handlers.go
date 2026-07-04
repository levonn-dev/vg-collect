package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/match"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/recs"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

const searchLimit = 20

// normQuery folds a query for cache keying (the provider gets the
// trimmed original).
func normQuery(q string) string {
	return strings.Join(strings.Fields(strings.ToLower(q)), " ")
}

// SearchCatalog is the discovery search: query cache in front of the
// provider, never DB-first (the lazily-built catalog is incomplete by
// construction). Provider down + cache cold degrades to a local name
// match, flagged and uncached.
func (h *Handlers) SearchCatalog(w http.ResponseWriter, r *http.Request, params api.SearchCatalogParams) {
	ctx := r.Context()
	q := strings.TrimSpace(params.Q)
	if q == "" {
		problem(w, r, http.StatusBadRequest, "invalid_param", "q must not be empty")
		return
	}
	// The generated binding checks presence, not enum membership.
	kind := string(params.Type)
	if kind != "game" && kind != "hardware" {
		problem(w, r, http.StatusBadRequest, "invalid_param", "type must be game or hardware")
		return
	}
	nq := normQuery(q)

	if body, err := h.cache.GetSearch(ctx, kind, nq); err != nil {
		h.failOpen(ctx, "search_get", err)
	} else if body != nil {
		writeRawJSON(w, body)
		return
	}

	var (
		results []api.SearchResult
		perr    error
	)
	if kind == "game" {
		results, perr = h.searchGames(ctx, q)
	} else {
		results, perr = h.searchHardware(ctx, q)
	}
	degraded := perr != nil
	if degraded {
		h.logger.WarnContext(ctx, "search provider unavailable; serving local catalog match", "kind", kind, "err", perr)
		local, err := h.store.SearchByName(ctx, q, searchLimit)
		if err != nil {
			problem(w, r, http.StatusInternalServerError, "internal", "search failed")
			return
		}
		results = localResults(kind, local)
	}
	if results == nil {
		results = []api.SearchResult{}
	}

	body, err := json.Marshal(api.SearchResults{Degraded: degraded, Results: results})
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "encoding failed")
		return
	}
	// Degraded answers are never cached: the next request should try
	// the provider again.
	if !degraded {
		if err := h.cache.PutSearch(ctx, kind, nq, body, h.searchTTL); err != nil {
			h.failOpen(ctx, "search_put", err)
		}
	}
	writeRawJSON(w, body)
}

func (h *Handlers) searchGames(ctx context.Context, q string) ([]api.SearchResult, error) {
	games, err := h.games.SearchGames(ctx, q, searchLimit)
	if err != nil {
		return nil, err
	}
	out := make([]api.SearchResult, 0, len(games))
	for _, g := range games {
		out = append(out, gameResult(g))
	}
	return out, nil
}

func gameResult(g igdb.Game) api.SearchResult {
	res := api.SearchResult{Type: api.SearchResultType("game"), Name: g.Name}
	id := g.ID
	res.IgdbGameId = &id
	if len(g.Platforms) > 0 {
		prs := make([]api.PlatformRef, 0, len(g.Platforms))
		for _, p := range g.Platforms {
			prs = append(prs, api.PlatformRef{IgdbPlatformId: p.ID, Name: p.Name})
		}
		res.Platforms = &prs
	}
	if y := g.ReleaseYear(); y > 0 {
		res.FirstReleaseYear = &y
	}
	if cu := g.CoverURL(); cu != "" {
		res.CoverUrl = &cu
	}
	return res
}

func isHardwareCategory(genre string) bool {
	switch genre {
	case "Systems", "Controllers", "Accessories":
		return true
	}
	return false
}

func (h *Handlers) searchHardware(ctx context.Context, q string) ([]api.SearchResult, error) {
	prods, err := h.prices.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]api.SearchResult, 0, len(prods))
	for _, p := range prods {
		if !isHardwareCategory(p.Genre) {
			continue
		}
		id, console, cat := p.ID, p.ConsoleName, p.Genre
		out = append(out, api.SearchResult{
			Type: api.SearchResultType("hardware"), Name: p.Name,
			PcProductId: &id, ConsoleName: &console, Category: &cat,
		})
		if len(out) == searchLimit {
			break
		}
	}
	return out, nil
}

// localResults maps catalog products onto search results for the
// degraded path.
func localResults(kind string, prods []store.Product) []api.SearchResult {
	out := make([]api.SearchResult, 0, len(prods))
	for _, p := range prods {
		isGame := p.Type == "game"
		if (kind == "game") != isGame {
			continue
		}
		if isGame {
			res := api.SearchResult{Type: api.SearchResultType("game"), Name: p.Name}
			if p.IGDB != nil {
				id := p.IGDB.GameID
				res.IgdbGameId = &id
				if p.IGDB.CoverURL != "" {
					cu := p.IGDB.CoverURL
					res.CoverUrl = &cu
				}
				if p.IGDB.FirstReleaseYear > 0 {
					y := p.IGDB.FirstReleaseYear
					res.FirstReleaseYear = &y
				}
			}
			if p.Platform != nil {
				prs := []api.PlatformRef{{IgdbPlatformId: p.Platform.IGDBID, Name: p.Platform.Name}}
				res.Platforms = &prs
			}
			out = append(out, res)
			continue
		}
		res := api.SearchResult{Type: api.SearchResultType("hardware"), Name: p.Name}
		if p.PriceCharting != nil {
			id := p.PriceCharting.PCProductID
			console := p.PriceCharting.ConsoleName
			res.PcProductId = &id
			res.ConsoleName = &console
		}
		out = append(out, res)
	}
	return out
}

// GetProduct is the identity lookup: Valkey, then Mongo, refetching a
// stale IGDB projection inline best-effort (stale serves when the
// provider is down). Prices refresh on the daily cadence only.
func (h *Handlers) GetProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	ctx := r.Context()
	id := productId.String()

	if body, err := h.cache.GetProduct(ctx, id); err != nil {
		h.failOpen(ctx, "product_get", err)
	} else if body != nil {
		writeRawJSON(w, body)
		return
	}

	p, err := h.store.GetProduct(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "get failed")
		return
	}
	p = h.refreshIGDBIfStale(ctx, p)
	h.writeProduct(ctx, w, r, p)
}

// refreshIGDBIfStale refetches an out-of-date IGDB projection
// (populated backwards into igdb_raw + the product), serving the stale
// copy on any failure.
func (h *Handlers) refreshIGDBIfStale(ctx context.Context, p store.Product) store.Product {
	if p.IGDB == nil || h.now().Sub(p.IGDB.FetchedAt) < h.igdbRefreshAfter {
		return p
	}
	games, err := h.games.GamesByIDs(ctx, []int64{p.IGDB.GameID})
	if err != nil || len(games) == 0 {
		h.logger.WarnContext(ctx, "stale igdb refetch failed; serving stale", "product", p.ID, "err", err)
		return p
	}
	now := h.now()
	if err := h.store.UpsertRaw(ctx, games, now); err != nil {
		h.logger.WarnContext(ctx, "raw upsert failed", "product", p.ID, "err", err)
	}
	meta := store.NewIGDBMeta(games[0], now)
	if err := h.store.SetIGDB(ctx, p.ID, meta); err != nil {
		h.logger.WarnContext(ctx, "igdb projection update failed; serving stale", "product", p.ID, "err", err)
		return p
	}
	p.IGDB = &meta
	return p
}

// writeProduct marshals, caches (fail-open), and serves a product.
func (h *Handlers) writeProduct(ctx context.Context, w http.ResponseWriter, r *http.Request, p store.Product) {
	body, err := json.Marshal(toAPIProduct(p))
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "encoding failed")
		return
	}
	if err := h.cache.PutProduct(ctx, p.ID, body, h.productTTL); err != nil {
		h.failOpen(ctx, "product_put", err)
	}
	writeRawJSON(w, body)
}

// toAPIProduct maps the store document onto the contract type.
func toAPIProduct(p store.Product) api.Product {
	pid, _ := uuid.Parse(p.ID)
	out := api.Product{
		Id: pid, Type: api.ProductType(p.Type), Name: p.Name,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if p.Region != "" {
		out.Region = &p.Region
	}
	if p.Edition != "" {
		out.Edition = &p.Edition
	}
	if p.Variant != "" {
		out.Variant = &p.Variant
	}
	if p.Platform != nil {
		out.Platform = &api.PlatformRef{IgdbPlatformId: p.Platform.IGDBID, Name: p.Platform.Name}
	}
	if p.IGDB != nil {
		m := api.IgdbMeta{
			GameId:       p.IGDB.GameID,
			Name:         p.IGDB.Name,
			Genres:       make([]string, 0, len(p.IGDB.Genres)),
			Themes:       append([]string{}, p.IGDB.Themes...),
			Franchises:   append([]string{}, p.IGDB.Franchises...),
			SimilarGames: append([]int64{}, p.IGDB.SimilarGames...),
			Companies:    make([]api.CompanyCredit, 0, len(p.IGDB.Companies)),
			FetchedAt:    p.IGDB.FetchedAt,
		}
		for _, g := range p.IGDB.Genres {
			m.Genres = append(m.Genres, g.Name)
		}
		for _, c := range p.IGDB.Companies {
			m.Companies = append(m.Companies, api.CompanyCredit{Name: c.Name, Developer: c.Developer, Publisher: c.Publisher})
		}
		if p.IGDB.CoverURL != "" {
			cu := p.IGDB.CoverURL
			m.CoverUrl = &cu
		}
		if p.IGDB.FirstReleaseYear > 0 {
			y := p.IGDB.FirstReleaseYear
			m.FirstReleaseYear = &y
		}
		out.Igdb = &m
	}
	if p.PriceCharting != nil {
		pc := api.PricechartingMeta{
			PcProductId:     p.PriceCharting.PCProductID,
			PcName:          p.PriceCharting.PCName,
			ConsoleName:     p.PriceCharting.ConsoleName,
			MatchConfidence: p.PriceCharting.MatchConfidence,
			Verified:        p.PriceCharting.Verified,
			AsOf:            p.PriceCharting.AsOf,
		}
		pc.LooseCents = p.PriceCharting.Current.LooseCents
		pc.CibCents = p.PriceCharting.Current.CIBCents
		pc.NewCents = p.PriceCharting.Current.NewCents
		out.Pricecharting = &pc
	}
	return out
}

// quoteOf copies a provider product's prices into a store quote
// (values copied so nothing aliases the response).
func quoteOf(p pricecharting.Product) store.PriceQuote {
	var q store.PriceQuote
	if p.LoosePriceCents != nil {
		v := *p.LoosePriceCents
		q.LooseCents = &v
	}
	if p.CIBPriceCents != nil {
		v := *p.CIBPriceCents
		q.CIBCents = &v
	}
	if p.NewPriceCents != nil {
		v := *p.NewPriceCents
		q.NewCents = &v
	}
	return q
}

// ResolveProduct is find-or-create for a search selection. Existing
// identity: returned as-is, no provider calls. Create (game): full
// IGDB payload into igdb_raw + projection + scored PriceCharting
// auto-match with an initial snapshot when matched. Create (hardware):
// PriceCharting product + borrowed IGDB platform metadata where a
// console mapping exists.
func (h *Handlers) ResolveProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.ResolveRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	typ := string(req.Type)
	region, edition, variant := deref(req.Region), deref(req.Edition), deref(req.Variant)

	var key store.ProductKey
	switch typ {
	case "game":
		if req.IgdbGameId == nil || req.PlatformIgdbId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "type game requires igdb_game_id and platform_igdb_id")
			return
		}
		key = store.ProductKey{Type: typ, IGDBGameID: *req.IgdbGameId, PlatformIGDBID: *req.PlatformIgdbId,
			Region: region, Edition: edition, Variant: variant}
	case "console", "accessory":
		if req.PcProductId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "type "+typ+" requires pc_product_id")
			return
		}
		key = store.ProductKey{Type: typ, PCProductID: *req.PcProductId,
			Region: region, Edition: edition, Variant: variant}
	default:
		problem(w, r, http.StatusBadRequest, "invalid_body", "type must be game, console or accessory")
		return
	}

	existing, err := h.store.FindProduct(ctx, key)
	if err == nil {
		h.writeProduct(ctx, w, r, existing)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}

	var p store.Product
	if typ == "game" {
		p, err = h.buildGameProduct(ctx, key)
	} else {
		p, err = h.buildHardwareProduct(ctx, typ, key)
	}
	if err != nil {
		h.resolveError(w, r, err)
		return
	}
	// Pre-mint the id at this single create call site: CreateProduct
	// (store.go) mints an id only when p.ID == "", otherwise it
	// preserves the caller-supplied one. Setting it here up front means
	// a duplicate-key convergence (a concurrent resolve already won)
	// is detectable below by an id mismatch, since the winner's
	// document it returns instead never carries this id.
	p.ID = uuid.NewString()
	created, err := h.store.CreateProduct(ctx, p)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	// First price point for a freshly matched product: the walk takes
	// over from tomorrow. Losing this snapshot is tolerable (warn).
	// created.ID == p.ID proves this call won the create race; on a
	// lost race (duplicate-key convergence) CreateProduct returns the
	// winner's document under a different id, and the winner already
	// appended its own initial snapshot, so this call must not append
	// a second one for the same product.
	if created.PriceCharting != nil && created.ID == p.ID {
		snap := store.Snapshot{
			ProductID: created.ID, CapturedAt: h.now(),
			LooseCents: created.PriceCharting.Current.LooseCents,
			CIBCents:   created.PriceCharting.Current.CIBCents,
			NewCents:   created.PriceCharting.Current.NewCents,
		}
		if err := h.store.AppendSnapshot(ctx, snap); err != nil {
			h.logger.WarnContext(ctx, "initial snapshot failed", "product", created.ID, "err", err)
		}
	}
	h.writeProduct(ctx, w, r, created)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// resolveErr classifies build failures onto the contract.
type resolveErr struct {
	status int
	code   string
	detail string
}

func (e *resolveErr) Error() string { return e.code + ": " + e.detail }

func (h *Handlers) resolveError(w http.ResponseWriter, r *http.Request, err error) {
	var re *resolveErr
	if errors.As(err, &re) {
		problem(w, r, re.status, re.code, re.detail)
		return
	}
	problem(w, r, http.StatusInternalServerError, "internal", "resolve failed")
}

func (h *Handlers) buildGameProduct(ctx context.Context, key store.ProductKey) (store.Product, error) {
	games, err := h.games.GamesByIDs(ctx, []int64{key.IGDBGameID})
	if err != nil {
		return store.Product{}, &resolveErr{http.StatusBadGateway, "upstream_unavailable", "game metadata provider unavailable"}
	}
	if len(games) == 0 {
		return store.Product{}, &resolveErr{http.StatusNotFound, "unknown_game", "no such igdb game"}
	}
	g := games[0]
	var platform *store.Platform
	for _, pl := range g.Platforms {
		if pl.ID == key.PlatformIGDBID {
			platform = &store.Platform{IGDBID: pl.ID, Name: pl.Name}
			break
		}
	}
	if platform == nil {
		return store.Product{}, &resolveErr{http.StatusBadRequest, "invalid_body", "the game did not release on that platform"}
	}
	now := h.now()
	if err := h.store.UpsertRaw(ctx, games, now); err != nil {
		return store.Product{}, fmt.Errorf("raw upsert: %w", err)
	}
	meta := store.NewIGDBMeta(g, now)
	return store.Product{
		Type: "game", Name: g.Name, Platform: platform,
		Region: key.Region, Edition: key.Edition, Variant: key.Variant,
		IGDB: &meta,
		// A provider outage or a below-threshold score both land here
		// as nil: stored unmatched, correctable via the admin mapping
		// endpoint. Never guessed.
		PriceCharting: h.autoMatch(ctx, g.Name, key.Edition, platform.Name),
	}, nil
}

// autoMatch searches PriceCharting and scores candidates; nil when
// nothing clears the threshold or the provider is down (logged).
func (h *Handlers) autoMatch(ctx context.Context, name, edition, platformName string) *store.PCMeta {
	hits, err := h.prices.Search(ctx, name)
	if err != nil {
		h.logger.WarnContext(ctx, "auto-match skipped: price provider unavailable", "name", name, "err", err)
		return nil
	}
	cands := make([]match.Candidate, 0, len(hits))
	for _, hit := range hits {
		cands = append(cands, match.Candidate{PCProductID: hit.ID, Name: hit.Name, ConsoleName: hit.ConsoleName})
	}
	res := match.Best(name, edition, platformName, cands)
	if !res.OK {
		h.logger.InfoContext(ctx, "auto-match below threshold; storing unmatched",
			"name", name, "platform", platformName, "best_confidence", res.Confidence, "threshold", matchThreshold)
		return nil
	}
	var winner pricecharting.Product
	for _, hit := range hits {
		if hit.ID == res.PCProductID {
			winner = hit
			break
		}
	}
	return &store.PCMeta{
		PCProductID: res.PCProductID, PCName: res.PCName, ConsoleName: res.ConsoleName,
		MatchConfidence: res.Confidence, Verified: false,
		Current: quoteOf(winner), AsOf: h.now(),
	}
}

func (h *Handlers) buildHardwareProduct(ctx context.Context, typ string, key store.ProductKey) (store.Product, error) {
	pc, err := h.prices.Product(ctx, key.PCProductID)
	if errors.Is(err, pricecharting.ErrNotFound) {
		return store.Product{}, &resolveErr{http.StatusNotFound, "unknown_pc_product", "no such pricecharting product"}
	}
	if err != nil {
		return store.Product{}, &resolveErr{http.StatusBadGateway, "upstream_unavailable", "price provider unavailable"}
	}
	return store.Product{
		Type: typ, Name: pc.Name,
		// Consoles borrow IGDB platform names/ids where a mapping
		// exists; nil is fine (accessory categories often have none).
		Platform: h.platformFor(ctx, pc.ConsoleName),
		Region:   key.Region, Edition: key.Edition, Variant: key.Variant,
		PriceCharting: &store.PCMeta{
			PCProductID: pc.ID, PCName: pc.Name, ConsoleName: pc.ConsoleName,
			// The user picked this exact product from hardware search:
			// exact by construction, but still machine-made (not
			// admin-verified).
			MatchConfidence: 1.0, Verified: false,
			Current: quoteOf(pc), AsOf: h.now(),
		},
	}, nil
}

// ensurePlatforms serves the cached IGDB platform catalog, fetching it
// wholesale on first need or after the staleness horizon (stale serves
// when the provider is down).
func (h *Handlers) ensurePlatforms(ctx context.Context) ([]igdb.Platform, error) {
	at, err := h.store.PlatformsFetchedAt(ctx)
	if err != nil {
		return nil, err
	}
	if !at.IsZero() && h.now().Sub(at) < h.igdbRefreshAfter {
		return h.store.ListPlatforms(ctx)
	}
	ps, err := h.games.Platforms(ctx)
	if err != nil {
		if !at.IsZero() {
			h.logger.WarnContext(ctx, "platform refetch failed; serving stale", "err", err)
			return h.store.ListPlatforms(ctx)
		}
		return nil, err
	}
	if err := h.store.UpsertPlatforms(ctx, ps, h.now()); err != nil {
		h.logger.WarnContext(ctx, "platform upsert failed", "err", err)
	}
	return ps, nil
}

// platformFor reverse-maps a PriceCharting console-name onto the IGDB
// platform catalog; nil when nothing maps.
func (h *Handlers) platformFor(ctx context.Context, consoleName string) *store.Platform {
	ps, err := h.ensurePlatforms(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "platform catalog unavailable; hardware keeps no platform ref", "err", err)
		return nil
	}
	for _, p := range ps {
		if match.ConsoleMatches(p.Name, consoleName) {
			return &store.Platform{IGDBID: p.ID, Name: p.Name}
		}
	}
	return nil
}

// BatchPrices reads current prices straight from the catalog (the
// daily walk keeps them fresh). Unknown ids are absent from the map;
// unmatched products carry unmatched=true and no price fields.
func (h *Handlers) BatchPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.PricesBatchRequest
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	// The schema's maxItems is documentation; the generated models do
	// not validate, so the cap is enforced here.
	if len(req.ProductIds) > 500 {
		problem(w, r, http.StatusBadRequest, "invalid_body", "at most 500 product_ids per call")
		return
	}
	ids := make([]string, len(req.ProductIds))
	for i, id := range req.ProductIds {
		ids[i] = id.String()
	}
	prods, err := h.store.ProductsByIDs(ctx, ids)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "price lookup failed")
		return
	}
	prices := make(map[string]api.ProductPrices, len(prods))
	for _, p := range prods {
		pp := api.ProductPrices{Unmatched: p.PriceCharting == nil}
		if pc := p.PriceCharting; pc != nil {
			pp.LooseCents = pc.Current.LooseCents
			pp.CibCents = pc.Current.CIBCents
			pp.NewCents = pc.Current.NewCents
			asOf := pc.AsOf
			pp.AsOf = &asOf
		}
		prices[p.ID] = pp
	}
	writeJSON(w, http.StatusOK, api.PricesBatchResponse{Prices: prices})
}

const (
	recsDefaultLimit = 20
	recsMaxLimit     = 50
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
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	limit := recsDefaultLimit
	if req.Limit != nil {
		limit = min(max(*req.Limit, 1), recsMaxLimit)
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
		writeJSON(w, http.StatusOK, api.ScoreResponse{Degraded: false, Recommendations: []api.Recommendation{}})
		return
	}

	// Metadata for the owned games (edges + genres).
	raw, degraded, err := h.ensureRaw(ctx, owned)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "metadata lookup failed")
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
		problem(w, r, http.StatusInternalServerError, "internal", "metadata lookup failed")
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
	out := make([]api.Recommendation, 0, limit)
	for _, sc := range scored {
		rg, ok := candRaw[sc.GameID]
		if !ok {
			// Candidate the provider no longer knows: skip (already
			// reflected in degraded when a fetch failed).
			continue
		}
		rec := api.Recommendation{
			IgdbGameId: sc.GameID,
			Name:       rg.Game.Name,
			Genres:     genreNames(rg.Game),
			Score:      sc.Score,
		}
		if cu := rg.Game.CoverURL(); cu != "" {
			rec.CoverUrl = &cu
		}
		if y := rg.Game.ReleaseYear(); y > 0 {
			rec.FirstReleaseYear = &y
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

// refreshBudget bounds a detached walk (well beyond a full catalog at
// polite provider rates).
const refreshBudget = 30 * time.Minute

// TriggerRefresh is the admin's immediate-refresh trigger.
func (h *Handlers) TriggerRefresh(w http.ResponseWriter, r *http.Request) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	h.startRefresh(w, r)
}

// InternalRefresh is the CronJob's trigger (hand-routed in routes.go,
// outside the contract and the JWT guard). It authenticates the
// static internal-caller token; the NetworkPolicy is the outer layer.
func (h *Handlers) InternalRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.internalCallerOK(r) {
		problem(w, r, http.StatusUnauthorized, "invalid_internal_token", "missing or wrong X-Internal-Token")
		return
	}
	h.startRefresh(w, r)
}

// internalCallerOK checks X-Internal-Token against the accepted set in
// constant time per candidate. The set holds one entry in steady state
// and two during a rotation (accept old + new while the CronJob flips).
func (h *Handlers) internalCallerOK(r *http.Request) bool {
	got := []byte(r.Header.Get("X-Internal-Token"))
	if len(got) == 0 {
		return false
	}
	for _, s := range h.refreshSecrets {
		if subtle.ConstantTimeCompare(got, []byte(s)) == 1 {
			return true
		}
	}
	return false
}

// startRefresh answers 202 and detaches the walk: at polite provider
// rates a real catalog outlives the server's write timeout, so the
// summary goes to the log, not the response. One walk at a time.
func (h *Handlers) startRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.refreshing.CompareAndSwap(false, true) {
		problem(w, r, http.StatusConflict, "refresh_in_progress", "a refresh walk is already running")
		return
	}
	go func() {
		defer h.refreshing.Store(false)
		// Detached from the request context: the trigger returns at 202.
		ctx, cancel := context.WithTimeout(context.Background(), refreshBudget)
		defer cancel()
		// Registered last so it unwinds first: a panic in the walk (a
		// malformed doc, a nil field breaking an assumed contract) is
		// contained here instead of killing the process. The guard
		// reset and context cancel above still run afterward as usual.
		// The CronJob retries daily, so an uncontained panic on a
		// persistently bad doc would otherwise crash-loop.
		defer func() {
			if v := recover(); v != nil {
				h.logger.ErrorContext(ctx, "refresh walk panicked", "panic", v)
			}
		}()
		h.runRefresh(ctx)
	}()
	writeJSON(w, http.StatusAccepted, api.RefreshAccepted{Status: api.RefreshAcceptedStatus("started")})
}

// runRefresh walks every mapped product: current prices updated, one
// snapshot appended, failures counted and skipped (the walk finishes
// what it can). Orphaned products keep snapshotting by design.
func (h *Handlers) runRefresh(ctx context.Context) {
	start := h.now()
	prods, err := h.store.ListPriced(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "refresh walk aborted", "err", err)
		return
	}
	var updated, snapshots, failures int
	for _, p := range prods {
		pc, err := h.prices.Product(ctx, p.PriceCharting.PCProductID)
		if err != nil {
			failures++
			h.logger.WarnContext(ctx, "refresh: price fetch failed", "product", p.ID, "pc_product", p.PriceCharting.PCProductID, "err", err)
			continue
		}
		q := quoteOf(pc)
		asOf := h.now()
		if err := h.store.SetCurrentPrices(ctx, p.ID, q, asOf); err != nil {
			failures++
			h.logger.WarnContext(ctx, "refresh: price update failed", "product", p.ID, "err", err)
			continue
		}
		updated++
		if err := h.store.AppendSnapshot(ctx, store.Snapshot{
			ProductID: p.ID, CapturedAt: asOf,
			LooseCents: q.LooseCents, CIBCents: q.CIBCents, NewCents: q.NewCents,
		}); err != nil {
			failures++
			h.logger.WarnContext(ctx, "refresh: snapshot failed", "product", p.ID, "err", err)
			continue
		}
		snapshots++
		if err := h.cache.InvalidateProduct(ctx, p.ID); err != nil {
			h.failOpen(ctx, "refresh_invalidate", err)
		}
	}
	h.logger.InfoContext(ctx, "refresh walk finished",
		"walked", len(prods), "updated", updated, "snapshots", snapshots,
		"failures", failures, "duration_ms", h.now().Sub(start).Milliseconds())
}

// SetProductMapping is the moderated correction: validate the mapping
// against the provider, fetch prices, snapshot, mark verified.
func (h *Handlers) SetProductMapping(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	ctx := r.Context()
	claims, _ := jwtauth.FromContext(ctx)
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	var req api.MappingRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	id := productId.String()
	if _, err := h.store.GetProduct(ctx, id); errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	} else if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "get failed")
		return
	}

	if req.PcProductId == nil {
		if err := h.store.SetPriceCharting(ctx, id, nil); err != nil {
			problem(w, r, http.StatusInternalServerError, "internal", "mapping clear failed")
			return
		}
	} else {
		pc, err := h.prices.Product(ctx, *req.PcProductId)
		if errors.Is(err, pricecharting.ErrNotFound) {
			problem(w, r, http.StatusNotFound, "unknown_pc_product", "no such pricecharting product")
			return
		}
		if err != nil {
			problem(w, r, http.StatusBadGateway, "upstream_unavailable", "price provider unavailable")
			return
		}
		q := quoteOf(pc)
		asOf := h.now()
		meta := &store.PCMeta{
			PCProductID: pc.ID, PCName: pc.Name, ConsoleName: pc.ConsoleName,
			MatchConfidence: 1.0, Verified: true,
			Current: q, AsOf: asOf,
		}
		if err := h.store.SetPriceCharting(ctx, id, meta); err != nil {
			problem(w, r, http.StatusInternalServerError, "internal", "mapping update failed")
			return
		}
		// A moderated correction is a fresh price point.
		if err := h.store.AppendSnapshot(ctx, store.Snapshot{
			ProductID: id, CapturedAt: asOf,
			LooseCents: q.LooseCents, CIBCents: q.CIBCents, NewCents: q.NewCents,
		}); err != nil {
			h.logger.WarnContext(ctx, "mapping snapshot failed", "product", id, "err", err)
		}
	}

	// The correction must be visible immediately, not after the TTL.
	if err := h.cache.InvalidateProduct(ctx, id); err != nil {
		h.failOpen(ctx, "mapping_invalidate", err)
	}
	p, err := h.store.GetProduct(ctx, id)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "reload failed")
		return
	}
	h.writeProduct(ctx, w, r, p)
}
