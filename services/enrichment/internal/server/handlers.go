package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/recs"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

const searchLimit = 20

// communityLaneLimit caps the search community lane (the contract's
// documented cap).
const communityLaneLimit = 10

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
	if kind != "game" && kind != "hardware" && kind != "pc_listing" {
		problem(w, r, http.StatusBadRequest, "invalid_param", "type must be game, hardware or pc_listing")
		return
	}
	nq := normQuery(q)

	var out api.SearchResults
	if body, err := h.cache.GetSearch(ctx, kind, nq); err != nil {
		h.failOpen(ctx, "search_get", err)
	} else if body != nil {
		if err := json.Unmarshal(body, &out); err != nil {
			h.failOpen(ctx, "search_decode", err)
		} else {
			h.countSearch(ctx, kind, "cache")
			h.interleaveCommunityResults(ctx, w, kind, q, out)
			return
		}
	}

	var (
		results []api.SearchResult
		perr    error
	)
	switch kind {
	case "game":
		results, perr = h.searchGames(ctx, q)
	case "hardware":
		results, perr = h.searchHardware(ctx, q)
	default:
		results, perr = h.searchPCListings(ctx, q)
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
		h.countSearch(ctx, kind, "degraded")
	} else {
		h.countSearch(ctx, kind, "provider")
	}
	if results == nil {
		results = []api.SearchResult{}
	}

	out = api.SearchResults{Degraded: degraded, Results: results}
	body, err := json.Marshal(out)
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
	h.interleaveCommunityResults(ctx, w, kind, q, out)
}

// communityResult maps an admin-minted community product onto the
// unified search shape. type stays the provider discriminator (game
// vs hardware); item_type carries the finer community kind for the
// pick, and origin marks the row so the SPA renders the community tag
// and builds a CommunityPick.
func communityResult(p store.Product) api.SearchResult {
	res := api.SearchResult{Name: p.Name}
	if p.Type == "game" {
		res.Type = "game"
	} else {
		res.Type = "hardware"
	}
	o := api.SearchResultOrigin("community")
	res.Origin = &o
	if id, err := uuid.Parse(p.ID); err == nil {
		res.ProductId = &id
	}
	it := api.SearchResultItemType(p.Type)
	res.ItemType = &it
	if p.Community != nil {
		if p.Community.PlatformName != "" {
			pn := p.Community.PlatformName
			res.PlatformName = &pn
		}
		if p.Community.CoverURL != "" {
			cu := p.Community.CoverURL
			res.CoverUrl = &cu
		}
		if !p.Community.FirstReleaseDate.IsZero() {
			fd := openapi_types.Date{Time: p.Community.FirstReleaseDate}
			res.FirstReleaseDate = &fd
		}
	}
	return res
}

// interleaveCommunityResults merges the community lane into the one
// results list and writes the answer. Community mints are scored
// against the query by the same name similarity the provider order
// uses and merged descending; a provider result precedes a community
// result of equal score (providers are the canonical catalog). The
// merge runs on the by-value copy AFTER cache resolution, so the
// provider cache stays a provider-only unit and a fresh mint still
// appears immediately. Game and hardware searches only; pc_listing
// picks price anchors, which community products never have.
func (h *Handlers) interleaveCommunityResults(ctx context.Context, w http.ResponseWriter, kind, q string, out api.SearchResults) {
	var types []string
	switch kind {
	case "game":
		types = []string{"game"}
	case "hardware":
		types = []string{"console", "accessory"}
	default: // pc_listing picks price anchors; community products have none
		writeJSON(w, http.StatusOK, out)
		return
	}
	comm, err := h.store.SearchCommunityProducts(ctx, types, q, communityLaneLimit)
	if err != nil {
		// Fail open like every other collaborator in this path: the
		// community lane is an optional overlay on results, so a store
		// fault degrades to the provider-only answer already in out
		// rather than discarding it behind a 500.
		h.failOpen(ctx, "community_search", err)
		writeJSON(w, http.StatusOK, out)
		return
	}
	if len(comm) == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	type scored struct {
		res      api.SearchResult
		score    float64
		provider bool
	}
	merged := make([]scored, 0, len(out.Results)+len(comm))
	for _, res := range out.Results {
		merged = append(merged, scored{res: res, score: match.Score(q, res.Name), provider: true})
	}
	for _, p := range comm {
		merged = append(merged, scored{res: communityResult(p), score: match.Score(q, p.Name), provider: false})
	}
	// Descending score; on a tie the provider row precedes the community
	// row. SliceStable preserves provider order and the store's name-asc
	// community order among otherwise-equal rows.
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].score != merged[j].score {
			return merged[i].score > merged[j].score
		}
		return merged[i].provider && !merged[j].provider
	})
	results := make([]api.SearchResult, 0, len(merged))
	for _, m := range merged {
		results = append(results, m.res)
	}
	out.Results = results
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) searchGames(ctx context.Context, q string) ([]api.SearchResult, error) {
	games, err := h.games.SearchGames(ctx, q, searchLimit)
	if err != nil {
		return nil, err
	}
	// Non-latin queries get the supplementary localization leg; latin
	// queries stay on the primary search alone (see hasNonLatinLetter).
	// A leg or fetch failure serves the primary results as-is - this is a
	// best-effort widening, never a hard dependency.
	if hasNonLatinLetter(q) {
		ids, lerr := h.games.SearchLocalizations(ctx, q, searchLimit)
		switch {
		case lerr != nil:
			h.logger.WarnContext(ctx, "localization search leg failed; serving primary results", "err", lerr)
			h.countLocalizationLeg(ctx, "error")
		case len(ids) == 0:
			h.countLocalizationLeg(ctx, "empty")
		default:
			have := make(map[int64]bool, len(games))
			for _, g := range games {
				have[g.ID] = true
			}
			var missing []int64
			for _, id := range ids {
				if !have[id] {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				extra, gerr := h.games.GamesByIDs(ctx, missing)
				if gerr != nil {
					h.logger.WarnContext(ctx, "localization leg fetch failed; serving primary results", "err", gerr)
					h.countLocalizationLeg(ctx, "error")
				} else {
					games = append(games, extra...)
					h.countLocalizationLeg(ctx, "merged")
				}
			} else {
				h.countLocalizationLeg(ctx, "merged")
			}
		}
	}
	games = rankExactFirst(q, games)
	if len(games) > searchLimit {
		games = games[:searchLimit]
	}
	out := make([]api.SearchResult, 0, len(games))
	for _, g := range games {
		res := gameResult(g)
		if mr := matchedRegion(q, g); mr != "" {
			res.MatchedRegion = &mr
		}
		out = append(out, res)
	}
	return out, nil
}

// rankExactFirst floats exact-name matches (normalized, so brackets,
// articles and possessives fold) to the top of the provider's
// relevance order - IGDB ranks loosely on exactness - with the rating
// count ordering the exacts so the widely known release leads.
// Everything else keeps provider order.
func rankExactFirst(q string, games []igdb.Game) []igdb.Game {
	exactName := func(g igdb.Game) bool {
		if match.SameName(g.Name, q) {
			return true
		}
		for _, b := range igdb.BundleLocalizations(g) {
			if (b.Name != "" && match.SameName(b.Name, q)) || (b.Translit != "" && match.SameName(b.Translit, q)) {
				return true
			}
		}
		return false
	}
	exact := make([]igdb.Game, 0, len(games))
	rest := make([]igdb.Game, 0, len(games))
	for _, g := range games {
		if exactName(g) {
			exact = append(exact, g)
		} else {
			rest = append(rest, g)
		}
	}
	sort.SliceStable(exact, func(i, j int) bool {
		return exact[i].TotalRatingCount > exact[j].TotalRatingCount
	})
	return append(exact, rest...)
}

// matchedRegion reports which region's localized title recognized the
// query, or "" when the canonical name did (or nothing did). Equality
// and containment over normQuery-folded strings - never the Dice
// scorer, which is whitespace-token-shaped and cannot grade CJK text.
// Guards: latin queries need 3+ runes, non-latin 2+ (so one
// character cannot annotate everything it appears in).
func matchedRegion(q string, g igdb.Game) string {
	nq := normQuery(q)
	minRunes := 3
	if !asciiOnlyQuery(nq) {
		minRunes = 2
	}
	if utf8.RuneCountInString(nq) < minRunes {
		return ""
	}
	contains := func(name string) bool {
		nn := normQuery(name)
		return nn != "" && (strings.Contains(nn, nq) || strings.Contains(nq, nn))
	}
	if contains(g.Name) {
		return ""
	}
	for _, b := range igdb.BundleLocalizations(g) {
		if (b.Name != "" && contains(b.Name)) || (b.Translit != "" && contains(b.Translit)) {
			return b.Region
		}
	}
	return ""
}

func asciiOnlyQuery(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// hasNonLatinLetter gates the supplementary localization leg: IGDB's
// own search already matches latin names and alternative names, so
// only queries carrying a non-latin letter (kana, Han, Hangul, ...)
// pay the extra provider call.
func hasNonLatinLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}

// platformReleaseRegions returns the distinct canonical regions this
// game released in on one platform, ordered by that region's earliest
// release date on the platform (a dateless row still asserts the
// region: it sorts after every dated region, ties broken
// alphabetically). Unlike platformReleaseDates, this is
// platform-exact: JP twin platforms (Famicom/NES, Super
// Famicom/SNES) are deliberately NOT folded together here, because a
// search result badges the actual physical release per platform row,
// not the collector's-console equivalence platformReleaseDates folds
// for the product projection's single scoped date.
func platformReleaseRegions(g igdb.Game, platformID int64) []string {
	type regionSpan struct {
		earliest time.Time
		hasDate  bool
	}
	byRegion := map[string]*regionSpan{}
	var order []string
	for _, rd := range g.ReleaseDates {
		// A platform-0 row matches no real platform; skipping it also
		// defends a platformID of 0 from matching every unplatformed row.
		if rd.Platform == 0 || rd.Platform != platformID {
			continue
		}
		name, ok := igdb.RegionName(rd.Region)
		if !ok {
			continue
		}
		span, seen := byRegion[name]
		if !seen {
			span = &regionSpan{}
			byRegion[name] = span
			order = append(order, name)
		}
		if rd.Date != 0 {
			d := time.Unix(rd.Date, 0).UTC().Truncate(24 * time.Hour)
			if !span.hasDate || d.Before(span.earliest) {
				span.earliest = d
				span.hasDate = true
			}
		}
	}
	if len(order) == 0 {
		return nil
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := byRegion[order[i]], byRegion[order[j]]
		if a.hasDate != b.hasDate {
			return a.hasDate // dated regions before dateless ones
		}
		if a.hasDate && !a.earliest.Equal(b.earliest) {
			return a.earliest.Before(b.earliest)
		}
		return order[i] < order[j]
	})
	return order
}

func gameResult(g igdb.Game) api.SearchResult {
	res := api.SearchResult{Type: api.SearchResultType("game"), Name: g.Name}
	id := g.ID
	res.IgdbGameId = &id
	if len(g.Platforms) > 0 {
		prs := make([]api.PlatformRef, 0, len(g.Platforms))
		for _, p := range g.Platforms {
			pr := api.PlatformRef{IgdbPlatformId: p.ID, Name: p.Name}
			if regions := platformReleaseRegions(g, p.ID); len(regions) > 0 {
				pr.ReleaseRegions = &regions
			}
			prs = append(prs, pr)
		}
		res.Platforms = &prs
	}
	if d := g.ReleaseDate(); !d.IsZero() {
		fd := openapi_types.Date{Time: d}
		res.FirstReleaseDate = &fd
	}
	if cu := g.CoverURL(); cu != "" {
		res.CoverUrl = &cu
	}
	if bundles := igdb.BundleLocalizations(g); len(bundles) > 0 {
		locs := make([]api.Localization, 0, len(bundles))
		for _, b := range bundles {
			al := api.Localization{Region: b.Region}
			if b.Name != "" {
				n := b.Name
				al.Name = &n
			}
			if b.Translit != "" {
				tr := b.Translit
				al.Translit = &tr
			}
			if b.CoverURL != "" {
				cu := b.CoverURL
				al.CoverUrl = &cu
			}
			locs = append(locs, al)
		}
		res.Localizations = &locs
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
			Type: "hardware", Name: p.Name,
			PcProductId: &id, ConsoleName: &console, Category: &cat,
		})
		if len(out) == searchLimit {
			break
		}
	}
	return out, nil
}

// searchPCListings is the all-of-PriceCharting search behind the
// proxy picker: no category filter (game listings included - the
// point is variant rows IGDB does not separate), with the provider's
// per-listing prices passed through so prints are tellable apart.
func (h *Handlers) searchPCListings(ctx context.Context, q string) ([]api.SearchResult, error) {
	// The provider's tokenizer misses possessive-less listing names
	// when the query keeps the possessive; the bare form returns the
	// superset, so every pc_listing query drops it.
	prods, err := h.prices.Search(ctx, match.ProviderQuery(q))
	if err != nil {
		return nil, err
	}
	out := make([]api.SearchResult, 0, len(prods))
	for _, p := range prods {
		out = append(out, pcListingResult(p.ID, p.Name, p.ConsoleName, p.Genre, quoteOf(p)))
		if len(out) == searchLimit {
			break
		}
	}
	return out, nil
}

// pcListingResult maps one PC listing - live (provider) or cached
// (a product's stored mapping, on the degraded path) - onto the wire
// shape.
func pcListingResult(id int64, name, console, category string, q store.PriceQuote) api.SearchResult {
	res := api.SearchResult{
		Type: api.SearchResultType("pc_listing"), Name: name,
		PcProductId: &id, ConsoleName: &console,
	}
	if category != "" {
		res.Category = &category
	}
	res.LooseCents = q.LooseCents
	res.CibCents = q.CIBCents
	res.NewCents = q.NewCents
	return res
}

// localResults maps catalog products onto search results for the
// degraded path.
func localResults(kind string, prods []store.Product) []api.SearchResult {
	out := make([]api.SearchResult, 0, len(prods))
	if kind == "pc_listing" {
		// Degraded: any product's stored mapping is a known listing. A
		// resolved game/hardware product can carry the same
		// pc_product_id as a separate pc_listing anchor product (two
		// independent resolves; nothing ties their identities
		// together), so an order-preserving de-dupe keeps one row per
		// listing.
		seen := make(map[int64]bool, len(prods))
		for _, p := range prods {
			if p.PriceCharting == nil || seen[p.PriceCharting.PCProductID] {
				continue
			}
			seen[p.PriceCharting.PCProductID] = true
			pc := p.PriceCharting
			out = append(out, pcListingResult(pc.PCProductID, pc.PCName, pc.ConsoleName, "", pc.Current))
		}
		return out
	}
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
				if !p.IGDB.FirstReleaseDate.IsZero() {
					fd := openapi_types.Date{Time: p.IGDB.FirstReleaseDate}
					res.FirstReleaseDate = &fd
				}
			}
			if p.Platform != nil {
				pr := api.PlatformRef{IgdbPlatformId: p.Platform.IGDBID, Name: p.Platform.Name}
				if p.Platform.LogoURL != "" {
					lu := p.Platform.LogoURL
					pr.LogoUrl = &lu
				}
				prs := []api.PlatformRef{pr}
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

// GetFxLatest serves the provider's cached rate snapshot. Rates power
// display-side conversion in the SPA only; nothing stored ever
// depends on them, so a failure here degrades display to USD.
func (h *Handlers) GetFxLatest(w http.ResponseWriter, r *http.Request) {
	rates, err := h.fxRates.Latest(r.Context())
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_unavailable", "exchange rates are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, api.FXRates{Base: rates.Base, Date: rates.Date, Rates: rates.Rates})
}

// ListPlatforms serves the platform catalog joined with alias
// knowledge, for the custom-entry picker and the normalize lever. The
// answer is cached 24h (the search-cache idiom): reference data that
// only changes when the platform sweep lands new rows, so a cold build
// fetches wholesale through ensurePlatforms and the rest read Valkey.
func (h *Handlers) ListPlatforms(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if body, err := h.cache.GetPlatforms(ctx); err != nil {
		h.failOpen(ctx, "platforms_get", err)
	} else if body != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	cats, err := h.ensurePlatforms(ctx)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_unavailable", "the platform catalog is unavailable")
		return
	}
	out := api.PlatformCatalog{Platforms: make([]api.CatalogPlatform, 0, len(cats))}
	for _, c := range cats {
		// PlatformAliases returns nil for a platform with no known
		// aliases; the contract types aliases a required string[], so
		// coalesce to [] rather than marshal a null the picker cannot
		// filter over.
		al := match.PlatformAliases(c.Name)
		if al == nil {
			al = []string{}
		}
		out.Platforms = append(out.Platforms, api.CatalogPlatform{
			IgdbId: c.ID, Name: c.Name, Aliases: al,
		})
	}
	sort.Slice(out.Platforms, func(i, j int) bool { return out.Platforms[i].Name < out.Platforms[j].Name })
	body, err := json.Marshal(out)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "encoding failed")
		return
	}
	if err := h.cache.PutPlatforms(ctx, body, h.searchTTL); err != nil {
		h.failOpen(ctx, "platforms_put", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
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
	var pid int64
	if p.Platform != nil {
		pid = p.Platform.IGDBID
	}
	meta := store.NewIGDBMeta(games[0], pid, now)
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
	if p.MatchHold {
		held := true
		out.MatchHold = &held
	}
	if p.Origin == "community" {
		o := api.ProductOrigin("community")
		out.Origin = &o
	}
	if p.Community != nil {
		cm := api.CommunityMeta{}
		if p.Community.PlatformName != "" {
			pn := p.Community.PlatformName
			cm.PlatformName = &pn
		}
		if !p.Community.FirstReleaseDate.IsZero() {
			fd := openapi_types.Date{Time: p.Community.FirstReleaseDate}
			cm.FirstReleaseDate = &fd
		}
		if p.Community.CoverURL != "" {
			cu := p.Community.CoverURL
			cm.CoverUrl = &cu
		}
		out.Community = &cm
	}
	if p.Platform != nil {
		out.Platform = &api.PlatformRef{IgdbPlatformId: p.Platform.IGDBID, Name: p.Platform.Name}
		if p.Platform.LogoURL != "" {
			lu := p.Platform.LogoURL
			out.Platform.LogoUrl = &lu
		}
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
		if !p.IGDB.FirstReleaseDate.IsZero() {
			fd := openapi_types.Date{Time: p.IGDB.FirstReleaseDate}
			m.FirstReleaseDate = &fd
		}
		if len(p.IGDB.ReleaseDates) > 0 {
			rds := make([]api.ReleaseDate, 0, len(p.IGDB.ReleaseDates))
			for _, rd := range p.IGDB.ReleaseDates {
				rds = append(rds, api.ReleaseDate{Region: rd.Region, Date: openapi_types.Date{Time: rd.Date}})
			}
			m.ReleaseDates = &rds
		}
		if len(p.IGDB.Localizations) > 0 {
			locs := make([]api.Localization, 0, len(p.IGDB.Localizations))
			for _, l := range p.IGDB.Localizations {
				al := api.Localization{Region: l.Region}
				if l.Name != "" {
					n := l.Name
					al.Name = &n
				}
				if l.Translit != "" {
					tr := l.Translit
					al.Translit = &tr
				}
				if l.CoverURL != "" {
					cu := l.CoverURL
					al.CoverUrl = &cu
				}
				locs = append(locs, al)
			}
			m.Localizations = &locs
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

// ResolveProduct is find-or-create for a search selection. Game
// identity is listing-keyed - (igdb game, platform, PriceCharting
// listing) - so game resolves route through resolveGame, which picks
// the listing (the user's manual match, or auto-match by plain name)
// BEFORE the lookup. Hardware identity is unchanged: an existing
// identity returns as-is; a miss fetches the PriceCharting product
// and borrows IGDB platform metadata where a console mapping exists.
func (h *Handlers) ResolveProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.ResolveRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	typ := string(req.Type)

	var key store.ProductKey
	switch typ {
	case "game":
		h.resolveGame(w, r, req)
		return
	case "console", "accessory":
		if req.PcProductId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "type "+typ+" requires pc_product_id")
			return
		}
		key = store.ProductKey{Type: typ, PCProductID: *req.PcProductId,
			Region: deref(req.Region), Edition: deref(req.Edition), Variant: deref(req.Variant)}
	case "pc_listing":
		if req.PcProductId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "type pc_listing requires pc_product_id")
			return
		}
		// Stray igdb/region/edition/variant fields are ignored, like the
		// console/accessory path ignores stray igdb fields.
		key = store.ProductKey{Type: typ, PCProductID: *req.PcProductId}
	default:
		problem(w, r, http.StatusBadRequest, "invalid_body", "type must be game, console, accessory or pc_listing")
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
	p, err := h.buildHardwareProduct(ctx, typ, key)
	if err != nil {
		h.resolveError(w, r, err)
		return
	}
	h.createAndServe(ctx, w, r, p)
}

// resolveGame picks the listing, then finds-or-creates the (game,
// platform, listing) member. Request region/edition/variant are
// ignored: they are entry-level facts, not game identity.
func (h *Handlers) resolveGame(w http.ResponseWriter, r *http.Request, req api.ResolveRequest) {
	ctx := r.Context()
	if req.IgdbGameId == nil || req.PlatformIgdbId == nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "type game requires igdb_game_id and platform_igdb_id")
		return
	}
	key := store.ProductKey{Type: "game", IGDBGameID: *req.IgdbGameId, PlatformIGDBID: *req.PlatformIgdbId}

	// Manual match: the chosen listing IS the member, so the lookup
	// needs no metadata or scoring work first.
	if req.PcProductId != nil {
		key.PCProductID = *req.PcProductId
		existing, err := h.store.FindProduct(ctx, key)
		if err == nil {
			h.writeProduct(ctx, w, r, existing)
			return
		}
		if !errors.Is(err, store.ErrNotFound) {
			problem(w, r, http.StatusInternalServerError, "internal", "lookup failed")
			return
		}
		p, berr := h.buildGameProduct(ctx, key)
		if berr != nil {
			h.resolveError(w, r, berr)
			return
		}
		h.createAndServe(ctx, w, r, p)
		return
	}

	// No pick: the auto-match winner (or the auto-miss null) is part
	// of the lookup key, so scoring runs before the find.
	g, fetchedAt, err := h.gamePayloadFor(ctx, key.IGDBGameID)
	if err != nil {
		h.resolveError(w, r, err)
		return
	}
	platform, err := platformOf(g, key.PlatformIGDBID)
	if err != nil {
		h.resolveError(w, r, err)
		return
	}
	meta := h.autoMatchGame(ctx, "resolve", g.Name, deref(req.MatchHint), platform.Name)
	if meta != nil {
		key.PCProductID = meta.PCProductID
	}
	existing, ferr := h.store.FindProduct(ctx, key)
	if ferr == nil {
		h.writeProduct(ctx, w, r, existing)
		return
	}
	if !errors.Is(ferr, store.ErrNotFound) {
		problem(w, r, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	platform.LogoURL = h.platformLogoFor(ctx, platform.IGDBID)
	igdbMeta := store.NewIGDBMeta(g, key.PlatformIGDBID, fetchedAt)
	h.createAndServe(ctx, w, r, store.Product{
		Type: "game", Name: g.Name, Platform: platform,
		IGDB:          &igdbMeta,
		PriceCharting: meta,
	})
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
	g, fetchedAt, err := h.gamePayloadFor(ctx, key.IGDBGameID)
	if err != nil {
		return store.Product{}, err
	}
	platform, err := platformOf(g, key.PlatformIGDBID)
	if err != nil {
		return store.Product{}, err
	}
	platform.LogoURL = h.platformLogoFor(ctx, platform.IGDBID)
	pcMeta, err := h.manualMatch(ctx, key.PCProductID)
	if err != nil {
		return store.Product{}, err
	}
	meta := store.NewIGDBMeta(g, key.PlatformIGDBID, fetchedAt)
	return store.Product{
		Type: "game", Name: g.Name, Platform: platform,
		IGDB:          &meta,
		PriceCharting: pcMeta,
	}, nil
}

// gamePayloadFor returns the game payload for scoring and projection:
// igdb_raw when present (no provider call - names are stable), else a
// GamesByIDs fetch populated backwards into igdb_raw. The returned
// time is the payload's fetch stamp, so a raw-sourced product keeps
// an honest IGDBMeta.FetchedAt for the read path's staleness refresh.
func (h *Handlers) gamePayloadFor(ctx context.Context, gameID int64) (igdb.Game, time.Time, error) {
	raws, err := h.store.RawByIDs(ctx, []int64{gameID})
	if err != nil {
		return igdb.Game{}, time.Time{}, fmt.Errorf("raw read: %w", err)
	}
	// A raw doc without a release table predates this feature, and one
	// below fields_version predates fields a newer generation added;
	// either way one refetch repairs it (UpsertRaw stamps the empty
	// array and the current version from then on, so a fetched-but-none
	// current raw does not refetch forever).
	if len(raws) == 1 && raws[0].Game.ReleaseDates != nil && raws[0].FieldsVersion >= store.RawFieldsVersion {
		return raws[0].Game, raws[0].FetchedAt, nil
	}
	games, err := h.games.GamesByIDs(ctx, []int64{gameID})
	if err != nil {
		// Provider down. A pre-feature raw (present, nil release table)
		// is still a usable stale payload - its projection just misses
		// the per-region dates - so serve it, matching
		// refreshIGDBIfStale's serve-stale degrade. This is safe because
		// the nightly reprojection refetches nil-table raws, so a product
		// minted from this stale copy is repaired on the next walk. With
		// no raw at all there is nothing to serve, so the error stands.
		if len(raws) == 1 {
			h.logger.WarnContext(ctx, "pre-feature raw refetch failed; serving stale payload", "game", gameID, "err", err)
			return raws[0].Game, raws[0].FetchedAt, nil
		}
		return igdb.Game{}, time.Time{}, &resolveErr{http.StatusBadGateway, "upstream_unavailable", "game metadata provider unavailable"}
	}
	if len(games) == 0 {
		return igdb.Game{}, time.Time{}, &resolveErr{http.StatusNotFound, "unknown_game", "no such igdb game"}
	}
	now := h.now()
	if err := h.store.UpsertRaw(ctx, games, now); err != nil {
		return igdb.Game{}, time.Time{}, fmt.Errorf("raw upsert: %w", err)
	}
	return games[0], now, nil
}

// platformOf checks the release-platform membership a game resolve
// promises (the payload's platform list carries names only; logos
// come from the platform catalog at create time).
func platformOf(g igdb.Game, platformID int64) (*store.Platform, error) {
	for _, pl := range g.Platforms {
		if pl.ID == platformID {
			return &store.Platform{IGDBID: pl.ID, Name: pl.Name}, nil
		}
	}
	return nil, &resolveErr{http.StatusBadRequest, "invalid_body", "the game did not release on that platform"}
}

// manualMatch fetches the exact listing the user chose, with the same
// error taxonomy as hardware resolve.
func (h *Handlers) manualMatch(ctx context.Context, pcID int64) (*store.PCMeta, error) {
	pc, err := h.prices.Product(ctx, pcID)
	if errors.Is(err, pricecharting.ErrNotFound) {
		return nil, &resolveErr{http.StatusNotFound, "unknown_pc_product", "no such pricecharting product"}
	}
	if err != nil {
		return nil, &resolveErr{http.StatusBadGateway, "upstream_unavailable", "price provider unavailable"}
	}
	return &store.PCMeta{
		PCProductID: pc.ID, PCName: pc.Name, ConsoleName: pc.ConsoleName,
		// Exact by construction (the user chose the listing) but still
		// machine-made: verified stays admin-only.
		MatchConfidence: 1.0, Verified: false,
		Current: quoteOf(pc), AsOf: h.now(),
	}, nil
}

// searchPCListingsCached is the resolve-side twin of the pc_listing
// search endpoint: same cache key, same cached body shape, same
// degraded discipline (a provider failure answers the caller and is
// never cached). Auto-match runs on every no-pick game resolve, so
// repeat adds of a family are a cache hit instead of a provider call.
func (h *Handlers) searchPCListingsCached(ctx context.Context, q string) ([]api.SearchResult, error) {
	nq := normQuery(q)
	if body, err := h.cache.GetSearch(ctx, "pc_listing", nq); err != nil {
		h.failOpen(ctx, "search_get", err)
	} else if body != nil {
		var res api.SearchResults
		if err := json.Unmarshal(body, &res); err == nil {
			return res.Results, nil
		}
		// A malformed cache entry reads as a miss.
	}
	results, err := h.searchPCListings(ctx, q)
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []api.SearchResult{}
	}
	body, err := json.Marshal(api.SearchResults{Degraded: false, Results: results})
	if err != nil {
		return results, nil // still usable for scoring; just not cached
	}
	if err := h.cache.PutSearch(ctx, "pc_listing", nq, body, h.searchTTL); err != nil {
		h.failOpen(ctx, "search_put", err)
	}
	return results, nil
}

// autoMatchGame scores the family's listing candidates; nil when the
// provider is degraded or nothing same-console clears the threshold
// (never guessed - the resolve then lands on the unmatched member).
// The winner's prices ride from the search row (cache-aged at worst;
// the daily walk refreshes them). source names the calling flow
// (resolve or rematch) on the outcome counter.
func (h *Handlers) autoMatchGame(ctx context.Context, source, name, hint, platformName string) *store.PCMeta {
	results, err := h.searchPCListingsCached(ctx, name)
	if err != nil {
		h.countMatch(ctx, source, "provider_down")
		h.logger.WarnContext(ctx, "auto-match skipped: price provider unavailable", "name", name, "err", err)
		return nil
	}
	cands := make([]match.Candidate, 0, len(results))
	for _, r := range results {
		if r.PcProductId == nil || r.ConsoleName == nil {
			continue
		}
		cands = append(cands, match.Candidate{PCProductID: *r.PcProductId, Name: r.Name, ConsoleName: *r.ConsoleName})
	}
	res := match.Best(name, hint, platformName, cands)
	if !res.OK {
		h.countMatch(ctx, source, "below_threshold")
		h.logger.InfoContext(ctx, "auto-match below threshold; landing on the unmatched member",
			"name", name, "platform", platformName, "hint", hint,
			"best_confidence", res.Confidence, "threshold", matchThreshold)
		return nil
	}
	for _, r := range results {
		if r.PcProductId != nil && *r.PcProductId == res.PCProductID {
			h.countMatch(ctx, source, "matched")
			return &store.PCMeta{
				PCProductID: res.PCProductID, PCName: res.PCName, ConsoleName: res.ConsoleName,
				MatchConfidence: res.Confidence, Verified: false,
				Current: store.PriceQuote{LooseCents: r.LooseCents, CIBCents: r.CibCents, NewCents: r.NewCents},
				AsOf:    h.now(),
			}
		}
	}
	return nil // unreachable: the winner came from results
}

// createAndServe inserts the built product and serves the outcome.
// The id is pre-minted at this single call site: CreateProduct mints
// only when p.ID == "", so a duplicate-key convergence (a concurrent
// resolve already won the identity) is detectable by an id mismatch -
// the winner's document never carries this id, and the winner already
// appended its own initial snapshot, so only the winning create
// snapshots. There is no fill step: every path that reaches here
// already carries its full identity, mapping included.
func (h *Handlers) createAndServe(ctx context.Context, w http.ResponseWriter, r *http.Request, p store.Product) {
	p.ID = uuid.NewString()
	created, err := h.store.CreateProduct(ctx, p)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "create failed")
		return
	}
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

// buildHardwareProduct builds any PC-anchored product (console,
// accessory, or a pc_listing price anchor) from its listing.
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
func (h *Handlers) ensurePlatforms(ctx context.Context) ([]store.CatalogPlatform, error) {
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
	rows := make([]store.CatalogPlatform, 0, len(ps))
	for _, p := range ps {
		rows = append(rows, store.CatalogPlatform{
			ID: p.ID, Name: p.Name, Abbreviation: p.Abbreviation,
			Generation: p.Generation, LogoURL: p.LogoURL(),
		})
	}
	return rows, nil
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
			return &store.Platform{IGDBID: p.ID, Name: p.Name, LogoURL: p.LogoURL}
		}
	}
	return nil
}

// platformLogoFor reads the catalog logo for a platform id ("" when
// the catalog is unavailable or the platform ships no logo).
func (h *Handlers) platformLogoFor(ctx context.Context, igdbID int64) string {
	ps, err := h.ensurePlatforms(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "platform catalog unavailable; product keeps no platform logo", "err", err)
		return ""
	}
	for _, p := range ps {
		if p.ID == igdbID {
			return p.LogoURL
		}
	}
	return ""
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

// BatchPriceHistory returns each requested product's snapshot series
// inside the window, oldest first (the collection dashboard's
// value-over-time composition reads it). Unknown ids and products with
// no in-window points are absent from the map.
func (h *Handlers) BatchPriceHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.PriceHistoryRequest
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
	days := 90
	if req.Days != nil {
		days = *req.Days
	}
	if days < 1 || days > 365 {
		problem(w, r, http.StatusBadRequest, "invalid_body", "days must be between 1 and 365")
		return
	}
	ids := make([]string, len(req.ProductIds))
	for i, id := range req.ProductIds {
		ids[i] = id.String()
	}
	snaps, err := h.store.SnapshotsSince(ctx, ids, h.now().UTC().AddDate(0, 0, -days))
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "history lookup failed")
		return
	}
	series := make(map[string][]api.PricePoint, len(snaps))
	for id, points := range snaps {
		out := make([]api.PricePoint, len(points))
		for i, p := range points {
			out[i] = api.PricePoint{
				CapturedAt: p.CapturedAt,
				LooseCents: p.LooseCents,
				CibCents:   p.CIBCents,
				NewCents:   p.NewCents,
			}
		}
		series[id] = out
	}
	writeJSON(w, http.StatusOK, api.PriceHistoryResponse{Series: series})
}

const (
	recsDefaultLimit = 20
	recsMaxLimit     = 50
	// recsCandidateCap bounds the metadata-fetch budget per request.
	recsCandidateCap = 200
	recsTopGenres    = 3
	// maxLibraryEntries mirrors the contract's maxItems on the score
	// request; past it the request is rejected, not truncated.
	maxLibraryEntries = 2500
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
	if len(req.Library) > maxLibraryEntries {
		problem(w, r, http.StatusBadRequest, "library_too_large", fmt.Sprintf("library exceeds %d entries", maxLibraryEntries))
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
	h.startRefresh(w, r, "admin")
}

// validCoverURL enforces the cover-link shape: https only, at most 512
// chars. The image is never fetched server-side (SSRF surface); the
// client renders it with a broken-image fallback.
func validCoverURL(s string) bool {
	return len(s) <= 512 && strings.HasPrefix(s, "https://")
}

// CreateCommunityProduct mints an anchor-less product from an
// approved catalog submission. Community identity is the curated
// name: no identity-index membership, no uniqueness machinery - the
// review panel's search is the dedup check. Variant stays empty (the
// single edition field carries the entry idiom's note).
func (h *Handlers) CreateCommunityProduct(w http.ResponseWriter, r *http.Request) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	var req api.CommunityProductCreate
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	switch string(req.Type) {
	case "game", "console", "accessory":
	default:
		problem(w, r, http.StatusBadRequest, "invalid_body", "type must be game, console or accessory")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "name must not be empty")
		return
	}
	if req.CoverUrl != nil && *req.CoverUrl != "" && !validCoverURL(*req.CoverUrl) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "cover_url must be an https URL up to 512 chars")
		return
	}
	p := store.Product{Type: string(req.Type), Name: name, Origin: "community"}
	if req.Region != nil {
		p.Region = *req.Region
	}
	if req.Edition != nil {
		p.Edition = *req.Edition
	}
	hasCover := req.CoverUrl != nil && *req.CoverUrl != ""
	if req.PlatformName != nil || req.FirstReleaseDate != nil || hasCover {
		cm := &store.CommunityMeta{}
		if req.PlatformName != nil {
			cm.PlatformName = *req.PlatformName
		}
		if req.FirstReleaseDate != nil {
			cm.FirstReleaseDate = req.FirstReleaseDate.Time
		}
		if hasCover {
			cm.CoverURL = *req.CoverUrl
		}
		p.Community = cm
	}
	created, err := h.store.CreateProduct(r.Context(), p)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, toAPIProduct(created))
}

// ListUnmatchedProducts serves the admin worklist. Unlike the walk's
// ListUnmatchedGames, this read spans all product types and includes
// held products - surfacing deliberate clears is the point.
func (h *Handlers) ListUnmatchedProducts(w http.ResponseWriter, r *http.Request, params api.ListUnmatchedProductsParams) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	limit := 200
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	offset := 0
	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}
	prods, total, err := h.store.ListUnmatchedProducts(r.Context(), limit, offset)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	page := api.UnmatchedProductsPage{Products: make([]api.Product, 0, len(prods)), TotalCount: total}
	for _, p := range prods {
		page.Products = append(page.Products, toAPIProduct(p))
	}
	writeJSON(w, http.StatusOK, page)
}

// ListCommunityProducts serves the admin community listing: every
// admin-minted, un-promoted community product, so an admin can find
// and remove ones no entry references. Unlike ListUnmatchedProducts,
// origin community is the filter (not the absence of a mapping -
// community products never carry one).
func (h *Handlers) ListCommunityProducts(w http.ResponseWriter, r *http.Request, params api.ListCommunityProductsParams) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	limit := 200
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	offset := 0
	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}
	prods, total, err := h.store.ListCommunityProductsPage(r.Context(), limit, offset)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	page := api.CommunityProductsPage{Products: make([]api.Product, 0, len(prods)), TotalCount: total}
	for _, p := range prods {
		page.Products = append(page.Products, toAPIProduct(p))
	}
	writeJSON(w, http.StatusOK, page)
}

// ListPromoteCandidates pages the sweep's flagged community products,
// strongest candidate first.
func (h *Handlers) ListPromoteCandidates(w http.ResponseWriter, r *http.Request, params api.ListPromoteCandidatesParams) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	limit := 200
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	offset := 0
	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}
	productID := ""
	if params.ProductId != nil {
		productID = params.ProductId.String()
	}
	prods, total, err := h.store.ListPromoteCandidateProducts(r.Context(), limit, offset, productID)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	page := api.PromoteCandidatesPage{Products: make([]api.PromoteCandidateProduct, 0, len(prods)), TotalCount: total}
	for _, p := range prods {
		row := api.PromoteCandidateProduct{Product: toAPIProduct(p), Candidates: make([]api.PromoteCandidate, 0, len(p.PromoteCandidates))}
		for _, c := range p.PromoteCandidates {
			row.Candidates = append(row.Candidates, api.PromoteCandidate{
				Provider: api.PromoteCandidateProvider(c.Provider), ProviderId: c.ProviderID,
				Name: c.Name, Score: c.Score, FoundAt: c.FoundAt,
			})
		}
		page.Products = append(page.Products, row)
	}
	writeJSON(w, http.StatusOK, page)
}

// DismissPromoteCandidate silences one candidate pair permanently.
func (h *Handlers) DismissPromoteCandidate(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	var req api.DismissCandidateRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	switch string(req.Provider) {
	case "igdb", "pricecharting":
	default:
		problem(w, r, http.StatusBadRequest, "invalid_body", "provider must be igdb or pricecharting")
		return
	}
	err := h.store.DismissPromoteCandidate(r.Context(), productId.String(), string(req.Provider), req.ProviderId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "dismiss failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// InternalRefresh is the CronJob's trigger (hand-routed in routes.go,
// outside the contract and the JWT guard). It authenticates the
// static internal-caller token; the NetworkPolicy is the outer layer.
func (h *Handlers) InternalRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.internalCallerOK(r) {
		problem(w, r, http.StatusUnauthorized, "invalid_internal_token", "missing or wrong X-Internal-Token")
		return
	}
	h.startRefresh(w, r, "internal")
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
func (h *Handlers) startRefresh(w http.ResponseWriter, r *http.Request, trigger string) {
	if !h.refreshing.CompareAndSwap(false, true) {
		problem(w, r, http.StatusConflict, "refresh_in_progress", "a refresh walk is already running")
		return
	}
	// The started line pairs with the per-step finished summaries: a
	// start with no finishes inside the budget marks a hung walk.
	h.logger.InfoContext(r.Context(), "refresh walk started", "trigger", trigger)
	go func() { //nolint:gosec // G118: the walk deliberately outlives the trigger request (202 + detach)
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
		// Price freshness for matched members takes budget priority;
		// the re-match step runs on what remains.
		h.runRematch(ctx)
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
	defer func() { h.recordWalkDuration(ctx, "prices", h.now().Sub(start).Seconds()) }()
	prods, err := h.store.ListPriced(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "refresh walk aborted", "err", err)
		return
	}
	var updated, snapshots, failures, walked int
	for _, p := range prods {
		if err := ctx.Err(); err != nil {
			h.logger.WarnContext(ctx, "refresh walk stopped early: context done",
				"walked", walked, "remaining", len(prods)-walked, "err", err)
			break
		}
		walked++
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
	h.logger.InfoContext(ctx, "refresh walk finished",
		"walked", walked, "updated", updated, "snapshots", snapshots,
		"failures", failures, "duration_ms", h.now().Sub(start).Milliseconds())
}

// rematchPerWalk caps the nightly re-match step; the worklist rotates
// oldest-first, so a larger backlog upgrades across runs without
// starving the priced walk's provider budget.
const rematchPerWalk = 50

// runRematch walks unmatched game products (admin-held ones are
// filtered out by the store) and auto-matches each by its plain name
// through the shared search cache. A landed upgrade snapshots once
// and invalidates the product cache; a duplicate-key means another
// member already carries the listing - logged and skipped, never
// merged. Detached-walk conventions match runRefresh.
func (h *Handlers) runRematch(ctx context.Context) {
	start := h.now()
	defer func() { h.recordWalkDuration(ctx, "rematch", h.now().Sub(start).Seconds()) }()
	prods, err := h.store.ListUnmatchedGames(ctx, rematchPerWalk)
	if err != nil {
		h.logger.ErrorContext(ctx, "re-match walk aborted", "err", err)
		return
	}
	var matched, skipped, failures, walked int
	for _, p := range prods {
		if err := ctx.Err(); err != nil {
			h.logger.WarnContext(ctx, "re-match walk stopped early: context done",
				"walked", walked, "remaining", len(prods)-walked, "err", err)
			break
		}
		walked++
		if p.Platform == nil {
			skipped++
			h.countRefreshItem(ctx, "rematch", "skipped")
			continue
		}
		meta := h.autoMatchGame(ctx, "rematch", p.Name, "", p.Platform.Name)
		if meta == nil {
			skipped++
			h.countRefreshItem(ctx, "rematch", "skipped")
			continue
		}
		landed, err := h.store.SetPriceChartingIfMissing(ctx, p.ID, meta)
		if errors.Is(err, store.ErrIdentityTaken) {
			skipped++
			h.countRefreshItem(ctx, "rematch", "skipped")
			h.logger.InfoContext(ctx, "re-match: listing already carried by another member; skipping",
				"product", p.ID, "pc_product", meta.PCProductID)
			continue
		}
		if err != nil {
			failures++
			h.countRefreshItem(ctx, "rematch", "failed")
			h.logger.WarnContext(ctx, "re-match: mapping update failed", "product", p.ID, "err", err)
			continue
		}
		if !landed {
			skipped++ // matched by something else since the list read
			h.countRefreshItem(ctx, "rematch", "skipped")
			continue
		}
		matched++
		// ok means the mapping landed; the best-effort snapshot below
		// does not demote it.
		h.countRefreshItem(ctx, "rematch", "ok")
		if err := h.store.AppendSnapshot(ctx, store.Snapshot{
			ProductID: p.ID, CapturedAt: meta.AsOf,
			LooseCents: meta.Current.LooseCents, CIBCents: meta.Current.CIBCents, NewCents: meta.Current.NewCents,
		}); err != nil {
			h.logger.WarnContext(ctx, "re-match: snapshot failed", "product", p.ID, "err", err)
		}
		if err := h.cache.InvalidateProduct(ctx, p.ID); err != nil {
			h.failOpen(ctx, "rematch_invalidate", err)
		}
	}
	h.logger.InfoContext(ctx, "re-match walk finished",
		"walked", walked, "matched", matched, "skipped", skipped,
		"failures", failures, "duration_ms", h.now().Sub(start).Milliseconds())
}

// runReprojection sweeps every igdb-bearing product nightly (an
// uncapped ListIGDBProducts read, mirroring the price walk's posture)
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
// Detached-walk conventions match runRefresh.
func (h *Handlers) runReprojection(ctx context.Context) {
	start := h.now()
	defer func() { h.recordWalkDuration(ctx, "reprojection", h.now().Sub(start).Seconds()) }()
	prods, err := h.store.ListIGDBProducts(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "reprojection walk aborted", "err", err)
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
		h.logger.ErrorContext(ctx, "reprojection walk aborted: raw read failed", "err", err)
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
			h.logger.ErrorContext(ctx, "reprojection walk aborted: refetch failed", "err", gerr)
			return
		}
		fetched = len(games)
		if err := h.store.UpsertRaw(ctx, games, now); err != nil {
			h.logger.ErrorContext(ctx, "reprojection walk aborted: raw upsert failed", "err", err)
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

	var walked, rebuilt, missing, failures int
	for _, p := range prods {
		if err := ctx.Err(); err != nil {
			h.logger.WarnContext(ctx, "reprojection walk stopped early: context done",
				"walked", walked, "remaining", len(prods)-walked, "err", err)
			break
		}
		walked++
		// Defensive: ListIGDBProducts filters on the igdb subdoc, so a
		// nil projection here should not happen - skip it rather than
		// deref, for loop-consistency.
		if p.IGDB == nil {
			missing++
			h.countRefreshItem(ctx, "reprojection", "skipped")
			h.logger.WarnContext(ctx, "reprojection walk: product matched the igdb filter but carries a nil projection",
				"product", p.ID)
			continue
		}
		// A missing raw (provider never returned it) or one still on the
		// pre-feature nil table (a refetch the provider could not honor)
		// carries no honest release data: skip rather than reproject a
		// nil table as a fetched-none empty. The next walk retries.
		raw, ok := rawByID[p.IGDB.GameID]
		if !ok || raw.Game.ReleaseDates == nil {
			missing++
			h.countRefreshItem(ctx, "reprojection", "skipped")
			h.logger.WarnContext(ctx, "reprojection walk: no usable raw for product",
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
			h.logger.WarnContext(ctx, "reprojection walk: projection update failed", "product", p.ID, "err", err)
			continue
		}
		rebuilt++
		h.countRefreshItem(ctx, "reprojection", "ok")
		if err := h.cache.InvalidateProduct(ctx, p.ID); err != nil {
			h.failOpen(ctx, "reprojection_invalidate", err)
		}
	}
	h.logger.InfoContext(ctx, "reprojection walk finished",
		"walked", walked, "rebuilt", rebuilt, "fetched", fetched,
		"missing", missing, "failures", failures, "duration_ms", h.now().Sub(start).Milliseconds())
}

// runCandidateSweep is the walk's community pass: for each community
// product, name-search the promote-relevant provider (games need igdb
// identity to promote, hardware needs a listing) and stash flag-only
// candidates at the same never-guess threshold. Never attaches: a
// repro shares its name with the original it reproduces, so a
// high-scoring match has an elevated false-positive base rate here -
// providers propose, admins decide. Dismissed pairs stay silent.
func (h *Handlers) runCandidateSweep(ctx context.Context) {
	start := h.now()
	defer func() { h.recordWalkDuration(ctx, "sweep", h.now().Sub(start).Seconds()) }()
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

// DeleteProduct is the residue mop: it permanently removes an
// unmatched product (and its snapshots) that exploration or stale
// matching left behind. Matched products refuse - clear first - and
// the bff verified no entries reference the product before calling,
// because entries are invisible from here.
func (h *Handlers) DeleteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	ctx := r.Context()
	claims, _ := jwtauth.FromContext(ctx)
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	id := productId.String()
	deleted, err := h.store.DeleteUnmatchedProduct(ctx, id)
	if err != nil && !deleted {
		problem(w, r, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	if err != nil {
		// The product is gone; orphaned snapshots are cleanup debt, not
		// a reason to fail the delete.
		h.logger.WarnContext(ctx, "product snapshots delete failed", "product", id, "err", err)
	}
	if !deleted {
		if _, gerr := h.store.GetProduct(ctx, id); errors.Is(gerr, store.ErrNotFound) {
			problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
			return
		} else if gerr != nil {
			problem(w, r, http.StatusInternalServerError, "internal", "get failed")
			return
		}
		problem(w, r, http.StatusConflict, "product_matched", "the product carries a mapping - clear it first")
		return
	}
	if err := h.cache.InvalidateProduct(ctx, id); err != nil {
		h.failOpen(ctx, "delete_invalidate", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// identityKey addresses the slot in prod's identity family that
// carries listing pcID (0 = the family's unmatched member, for game
// products). Identity-conflict holder lookups use it; the hardware
// filter takes the id literally, so a hardware unmatched slot is not
// addressable and that lookup simply misses.
func identityKey(prod store.Product, pcID int64) store.ProductKey {
	k := store.ProductKey{
		Type: prod.Type, PCProductID: pcID,
		Region: prod.Region, Edition: prod.Edition, Variant: prod.Variant,
	}
	if prod.IGDB != nil {
		k.IGDBGameID = prod.IGDB.GameID
	}
	if prod.Platform != nil {
		k.PlatformIGDBID = prod.Platform.IGDBID
	}
	return k
}

// withHolder appends the conflicting identity's current holder to an
// identity_taken detail so the admin can look it up instead of
// guessing. Best-effort: a missed lookup leaves the detail as-is.
func withHolder(ctx context.Context, st Store, detail string, key store.ProductKey) string {
	holder, err := st.FindProduct(ctx, key)
	if err != nil {
		return detail
	}
	return fmt.Sprintf("%s (holder: %s %q)", detail, holder.ID, holder.Name)
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
	prod, err := h.store.GetProduct(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	} else if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "get failed")
		return
	}
	if prod.Origin == "community" {
		problem(w, r, http.StatusConflict, "product_not_provider",
			"community products take anchors through promote, not the mapping fix")
		return
	}

	if req.PcProductId == nil {
		if err := h.store.SetPriceCharting(ctx, id, nil); errors.Is(err, store.ErrIdentityTaken) {
			problem(w, r, http.StatusConflict, "identity_taken",
				withHolder(ctx, h.store, "clearing would collide with an existing unmatched product of the same identity", identityKey(prod, 0)))
			return
		} else if err != nil {
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
		if err := h.store.SetPriceCharting(ctx, id, meta); errors.Is(err, store.ErrIdentityTaken) {
			problem(w, r, http.StatusConflict, "identity_taken",
				withHolder(ctx, h.store, "another product with the same identity already carries that listing", identityKey(prod, pc.ID)))
			return
		} else if err != nil {
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

// PromoteProduct attaches provider anchors to a community product and
// flips it to provider origin in place: the id stays stable, so every
// adopter upgrades through live reads. The identity index adjudicates
// the re-entry - a twin answers 409 with the holder named and nothing
// changes (true merge is deliberately not automated).
func (h *Handlers) PromoteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	ctx := r.Context()
	claims, _ := jwtauth.FromContext(ctx)
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	var req api.PromoteRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	id := productId.String()
	prod, err := h.store.GetProduct(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	} else if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "get failed")
		return
	}
	if prod.Origin != "community" {
		problem(w, r, http.StatusConflict, "product_not_community",
			"only community products promote; provider products use the mapping fix")
		return
	}

	var (
		igdbMeta *store.IGDBMeta
		platform *store.Platform
		pcMeta   *store.PCMeta
	)
	switch prod.Type {
	case "game":
		if req.IgdbGameId == nil || req.PlatformIgdbId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "game promotion requires igdb_game_id and platform_igdb_id")
			return
		}
		g, fetchedAt, gerr := h.gamePayloadFor(ctx, *req.IgdbGameId)
		if gerr != nil {
			// Same taxonomy the resolve flow consumes: a *resolveErr
			// carries unknown_game (404) or upstream_unavailable (502),
			// while a raw read/upsert fault is an internal DB error, not a
			// provider outage. resolveError is that exact classifier, so
			// reuse it rather than re-deriving a mapping that would slot
			// the DB fault under a misleading 502.
			h.resolveError(w, r, gerr)
			return
		}
		p, perr := platformOf(g, *req.PlatformIgdbId)
		if perr != nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "the game did not release on that platform")
			return
		}
		platform = p
		meta := store.NewIGDBMeta(g, *req.PlatformIgdbId, fetchedAt)
		igdbMeta = &meta
	case "console", "accessory":
		if req.PcProductId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "hardware promotion requires pc_product_id")
			return
		}
	default:
		problem(w, r, http.StatusBadRequest, "invalid_body", "only game, console and accessory products promote")
		return
	}
	if req.PcProductId != nil {
		pc, perr := h.prices.Product(ctx, *req.PcProductId)
		if errors.Is(perr, pricecharting.ErrNotFound) {
			problem(w, r, http.StatusNotFound, "unknown_pc_product", "no such pricecharting product")
			return
		}
		if perr != nil {
			problem(w, r, http.StatusBadGateway, "upstream_unavailable", "price provider unavailable")
			return
		}
		pcMeta = &store.PCMeta{
			PCProductID: pc.ID, PCName: pc.Name, ConsoleName: pc.ConsoleName,
			MatchConfidence: 1.0, Verified: true,
			Current: quoteOf(pc), AsOf: h.now(),
		}
	}

	err = h.store.PromoteProduct(ctx, id, igdbMeta, platform, pcMeta)
	if errors.Is(err, store.ErrIdentityTaken) {
		key := store.ProductKey{Type: prod.Type, Region: prod.Region, Edition: prod.Edition, Variant: prod.Variant}
		if req.IgdbGameId != nil {
			key.IGDBGameID = *req.IgdbGameId
		}
		if req.PlatformIgdbId != nil {
			key.PlatformIGDBID = *req.PlatformIgdbId
		}
		if req.PcProductId != nil {
			key.PCProductID = *req.PcProductId
		}
		problem(w, r, http.StatusConflict, "identity_taken",
			withHolder(ctx, h.store, "a provider product already holds that identity", key))
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "promote failed")
		return
	}
	if pcMeta != nil {
		if err := h.store.AppendSnapshot(ctx, store.Snapshot{
			ProductID: id, CapturedAt: pcMeta.AsOf,
			LooseCents: pcMeta.Current.LooseCents, CIBCents: pcMeta.Current.CIBCents, NewCents: pcMeta.Current.NewCents,
		}); err != nil {
			h.logger.WarnContext(ctx, "promote snapshot failed", "product", id, "err", err)
		}
	}
	if err := h.cache.InvalidateProduct(ctx, id); err != nil {
		h.failOpen(ctx, "promote_invalidate", err)
	}
	p, err := h.store.GetProduct(ctx, id)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "reload failed")
		return
	}
	h.writeProduct(ctx, w, r, p)
}
