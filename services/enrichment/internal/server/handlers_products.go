// Product identity: find-or-create resolve for games and hardware,
// the read/serve path, and the admin mapping and delete levers.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

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
		h.internalError(w, r, "product_lookup", "get failed", err)
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
		h.internalError(w, r, "product_encode", "encoding failed", err)
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
		Id: pid, Type: common.ProductType(p.Type), Name: p.Name,
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
		o := common.ProductOrigin("community")
		out.Origin = &o
	}
	if p.Community != nil {
		cm := common.CommunityMeta{}
		if p.Community.PlatformName != "" {
			pn := p.Community.PlatformName
			cm.PlatformName = &pn
		}
		if len(p.Community.Developers) > 0 {
			devs := p.Community.Developers
			cm.Developers = &devs
		}
		if len(p.Community.Publishers) > 0 {
			pubs := p.Community.Publishers
			cm.Publishers = &pubs
		}
		if p.Community.Region != "" {
			rg := p.Community.Region
			cm.Region = &rg
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
		out.Platform = &common.PlatformRef{IgdbPlatformId: p.Platform.IGDBID, Name: p.Platform.Name}
		if p.Platform.LogoURL != "" {
			lu := p.Platform.LogoURL
			out.Platform.LogoUrl = &lu
		}
	}
	if p.IGDB != nil {
		m := common.IgdbMeta{
			GameId:       p.IGDB.GameID,
			Name:         p.IGDB.Name,
			Genres:       make([]string, 0, len(p.IGDB.Genres)),
			Themes:       append([]string{}, p.IGDB.Themes...),
			Franchises:   append([]string{}, p.IGDB.Franchises...),
			SimilarGames: append([]int64{}, p.IGDB.SimilarGames...),
			Companies:    make([]common.CompanyCredit, 0, len(p.IGDB.Companies)),
			FetchedAt:    p.IGDB.FetchedAt,
		}
		for _, g := range p.IGDB.Genres {
			m.Genres = append(m.Genres, g.Name)
		}
		for _, c := range p.IGDB.Companies {
			m.Companies = append(m.Companies, common.CompanyCredit{Name: c.Name, Developer: c.Developer, Publisher: c.Publisher})
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
			rds := make([]common.ReleaseDate, 0, len(p.IGDB.ReleaseDates))
			for _, rd := range p.IGDB.ReleaseDates {
				rds = append(rds, common.ReleaseDate{Region: common.ReleaseRegion(rd.Region), Date: openapi_types.Date{Time: rd.Date}})
			}
			m.ReleaseDates = &rds
		}
		if len(p.IGDB.Localizations) > 0 {
			locs := make([]common.Localization, 0, len(p.IGDB.Localizations))
			for _, l := range p.IGDB.Localizations {
				al := common.Localization{Region: l.Region}
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
		pc := common.PricechartingMeta{
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
	if !httpkit.DecodeBody(w, r, 16*1024, &req) {
		return
	}
	typ := string(req.Type)

	// type's enum (game, console, accessory, pc_listing) is specval's
	// job; no default arm needed below (specval guarantees typ is one
	// of the four before this handler ever runs). Each arm's own
	// pc_product_id requirement is a cross-field rule the flat request
	// schema cannot express, so it stays.
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
	}

	existing, err := h.store.FindProduct(ctx, key)
	if err == nil {
		h.writeProduct(ctx, w, r, existing)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		h.internalError(w, r, "resolve_lookup", "lookup failed", err)
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
// platform, listing) member. Request edition/variant are ignored
// (entry-level facts); region is a MATCHING input only - it steers
// which listing auto-match lands on and never joins identity or the
// stored product. The picker path ignores it entirely.
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
			h.internalError(w, r, "resolve_game_lookup", "lookup failed", err)
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
	region := deref(req.Region)
	meta := h.autoMatchGame(ctx, "resolve", matchNamesFor(g, region), deref(req.MatchHint), platform.Name, region)
	if meta != nil {
		key.PCProductID = meta.PCProductID
	}
	existing, ferr := h.store.FindProduct(ctx, key)
	if ferr == nil {
		h.writeProduct(ctx, w, r, existing)
		return
	}
	if !errors.Is(ferr, store.ErrNotFound) {
		h.internalError(w, r, "resolve_game_lookup", "lookup failed", ferr)
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
	h.internalError(w, r, "resolve", "resolve failed", err)
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
		// minted from this stale copy is repaired on the next refresh. With
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

// autoMatchGame scores the family's listing candidates; nil when the
// provider is degraded or nothing the region admits clears the
// threshold (never guessed - the resolve then lands on the unmatched
// member). names come from matchNamesFor: names[0] is the primary
// provider query; when the region gate admits nothing from it and a
// second form exists, one fallback search runs with the alternate
// form - PriceCharting files JP listings under romaji or hybrid
// names, so either query can be the one that hits. Both legs ride the
// shared pc_listing search cache. source names the calling flow on
// the outcome counter; region rides it as a label.
func (h *Handlers) autoMatchGame(ctx context.Context, source string, names []string, hint, platformName, region string) *store.PCMeta {
	results, err := h.searchPCListingsCached(ctx, names[0])
	if err != nil {
		h.countMatch(ctx, source, "provider_down", region)
		h.logger.WarnContext(ctx, "auto-match skipped: price provider unavailable", "name", names[0], "err", err)
		return nil
	}
	fallbackFired := false
	// names[1] != names[0] guards a bundle whose transliteration equals
	// the canonical name: it offers no alternate form, so re-searching
	// it would just repeat the primary query the gate already rejected.
	if len(names) > 1 && names[1] != names[0] && len(match.FilterConsole(platformName, region, matchCandidates(results))) == 0 {
		fb, fbErr := h.searchPCListingsCached(ctx, names[1])
		if fbErr != nil {
			h.countFallbackSearch(ctx, "error")
		} else {
			fallbackFired = true
			results = append(results, fb...)
		}
	}
	res := match.Best(names, hint, platformName, region, matchCandidates(results))
	if fallbackFired {
		outcome := "still_empty"
		if res.OK {
			outcome = "matched"
		}
		h.countFallbackSearch(ctx, outcome)
	}
	if !res.OK {
		h.countMatch(ctx, source, "below_threshold", region)
		h.logger.InfoContext(ctx, "auto-match below threshold; landing on the unmatched member",
			"name", names[0], "platform", platformName, "region", region, "hint", hint,
			"best_confidence", res.Confidence, "threshold", matchThreshold)
		return nil
	}
	for _, r := range results {
		if r.PcProductId != nil && *r.PcProductId == res.PCProductID {
			h.countMatch(ctx, source, "matched", region)
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
		h.internalError(w, r, "product_create", "create failed", err)
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
	if !h.requireAdmin(w, r) {
		return
	}
	var req api.MappingRequest
	if !httpkit.DecodeBody(w, r, 16*1024, &req) {
		return
	}
	id := productId.String()
	prod, err := h.store.GetProduct(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	} else if err != nil {
		h.internalError(w, r, "product_mapping_get", "get failed", err)
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
			h.internalError(w, r, "product_mapping_clear", "mapping clear failed", err)
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
			h.internalError(w, r, "product_mapping_update", "mapping update failed", err)
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
		h.internalError(w, r, "product_mapping_reload", "reload failed", err)
		return
	}
	h.writeProduct(ctx, w, r, p)
}

// DeleteProduct is the residue mop: it permanently removes an
// unmatched product (and its snapshots) that exploration or stale
// matching left behind. Matched products refuse - clear first - and
// the bff verified no entries reference the product before calling,
// because entries are invisible from here.
func (h *Handlers) DeleteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	ctx := r.Context()
	if !h.requireAdmin(w, r) {
		return
	}
	id := productId.String()
	deleted, err := h.store.DeleteUnmatchedProduct(ctx, id)
	if err != nil && !deleted {
		h.internalError(w, r, "product_delete", "delete failed", err)
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
			h.internalError(w, r, "product_delete_get", "get failed", gerr)
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
