// Catalog search: provider search with cache-first reads, the
// degraded local-catalog fallback, and the community-lane interleave.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

const searchLimit = 20

// communityLaneLimit caps the search community lane (the contract's
// documented cap).
const communityLaneLimit = 10

// normQuery folds a query for cache keying (the provider gets the
// trimmed original).
func normQuery(q string) string {
	return strings.Join(strings.Fields(strings.ToLower(q)), " ")
}

// matchNamesFor returns the auto-match target forms for a game in an
// entry region: the region's chained transliteration first when a
// bundle carries one (it becomes the primary provider query), then
// the canonical name. Base regions and games without a bundle keep
// the canonical name alone - zero extra provider calls.
func matchNamesFor(g igdb.Game, region string) []string {
	for _, id := range regionQueryChains[region] {
		for _, b := range igdb.BundleLocalizations(g) {
			if b.Region == id && b.Translit != "" {
				return []string{b.Translit, g.Name}
			}
		}
	}
	return []string{g.Name}
}

// matchCandidates adapts provider search rows to scoring candidates.
func matchCandidates(results []api.SearchResult) []match.Candidate {
	cands := make([]match.Candidate, 0, len(results))
	for _, r := range results {
		if r.PcProductId == nil || r.ConsoleName == nil {
			continue
		}
		cands = append(cands, match.Candidate{PCProductID: *r.PcProductId, Name: r.Name, ConsoleName: *r.ConsoleName})
	}
	return cands
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
		if p.Community.Region != "" {
			rg := p.Community.Region
			res.Region = &rg
		}
		if !p.Community.FirstReleaseDate.IsZero() {
			fd := openapi_types.Date{Time: p.Community.FirstReleaseDate}
			res.FirstReleaseDate = &fd
		}
		if len(p.Community.Developers) > 0 {
			devs := p.Community.Developers
			res.Developers = &devs
		}
		if len(p.Community.Publishers) > 0 {
			pubs := p.Community.Publishers
			res.Publishers = &pubs
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
