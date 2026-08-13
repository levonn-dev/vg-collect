// Community products: admin mints, the unmatched/community/promote
// worklists, and promoting a community product onto provider anchors.

package server

import (
	"errors"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/catalogval"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// CreateCommunityProduct mints an anchor-less product from an
// approved catalog submission. Community identity is the curated
// name: no identity-index membership, no uniqueness machinery - the
// review panel's search is the dedup check. Variant stays empty (the
// single edition field carries the entry idiom's note).
func (h *Handlers) CreateCommunityProduct(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req api.CommunityProductCreate
	if !httpkit.DecodeBody(w, r, 16*1024, &req) {
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
	if req.CoverUrl != nil && *req.CoverUrl != "" && !catalogval.ValidCoverURL(*req.CoverUrl) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "cover_url must be an https URL up to 512 chars")
		return
	}
	devs, detail := catalogval.NormalizeCredits("developers", req.Developers)
	if detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	pubs, detail := catalogval.NormalizeCredits("publishers", req.Publishers)
	if detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	p := store.Product{Type: string(req.Type), Name: name, Origin: "community"}
	if req.Edition != nil {
		p.Edition = *req.Edition
	}
	hasCover := req.CoverUrl != nil && *req.CoverUrl != ""
	// Trimmed, not the raw value: a whitespace-only region must not by
	// itself earn an otherwise-empty community block (cm.Region below
	// trims too, so an untrimmed check here would mint a block whose
	// only field renders as empty anyway).
	hasRegion := req.Region != nil && strings.TrimSpace(*req.Region) != ""
	// Trimmed, not the raw value: a whitespace-only platform_name must not
	// by itself earn an otherwise-empty community block (cm.PlatformName
	// below trims too, so an untrimmed check here would mint a block whose
	// only field renders as empty anyway).
	hasPlatformName := req.PlatformName != nil && strings.TrimSpace(*req.PlatformName) != ""
	if hasPlatformName || req.FirstReleaseDate != nil || hasCover || hasRegion || devs != nil || pubs != nil {
		cm := &store.CommunityMeta{Developers: devs, Publishers: pubs}
		if hasPlatformName {
			cm.PlatformName = strings.TrimSpace(*req.PlatformName)
		}
		if hasRegion {
			cm.Region = strings.TrimSpace(*req.Region)
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
		h.internalError(w, r, "community_product_create", "create failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, toAPIProduct(created))
}

// ListUnmatchedProducts serves the admin worklist: every unmatched
// product regardless of type, including held ones - surfacing
// deliberate clears is the point.
func (h *Handlers) ListUnmatchedProducts(w http.ResponseWriter, r *http.Request, params api.ListUnmatchedProductsParams) {
	if !h.requireAdmin(w, r) {
		return
	}
	limit := httpkit.ClampSilent(params.Limit, 200, 1, 500)
	offset := httpkit.ClampSilent(params.Offset, 0, 0)
	prods, total, err := h.store.ListUnmatchedProducts(r.Context(), limit, offset)
	if err != nil {
		h.internalError(w, r, "unmatched_products_list", "list failed", err)
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
	if !h.requireAdmin(w, r) {
		return
	}
	limit := httpkit.ClampSilent(params.Limit, 200, 1, 500)
	offset := httpkit.ClampSilent(params.Offset, 0, 0)
	prods, total, err := h.store.ListCommunityProductsPage(r.Context(), limit, offset)
	if err != nil {
		h.internalError(w, r, "community_products_list", "list failed", err)
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
	if !h.requireAdmin(w, r) {
		return
	}
	limit := httpkit.ClampSilent(params.Limit, 200, 1, 500)
	offset := httpkit.ClampSilent(params.Offset, 0, 0)
	productID := ""
	if params.ProductId != nil {
		productID = params.ProductId.String()
	}
	prods, total, err := h.store.ListPromoteCandidateProducts(r.Context(), limit, offset, productID)
	if err != nil {
		h.internalError(w, r, "promote_candidates_list", "list failed", err)
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
	if !h.requireAdmin(w, r) {
		return
	}
	var req api.DismissCandidateRequest
	if !httpkit.DecodeBody(w, r, 16*1024, &req) {
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
		h.internalError(w, r, "promote_candidate_dismiss", "dismiss failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PromoteProduct attaches provider anchors to a community product and
// flips it to provider origin in place: the id stays stable, so every
// adopter upgrades through live reads. The identity index adjudicates
// the re-entry - a twin answers 409 with the holder named and nothing
// changes (true merge is deliberately not automated).
func (h *Handlers) PromoteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	ctx := r.Context()
	if !h.requireAdmin(w, r) {
		return
	}
	var req api.PromoteRequest
	if !httpkit.DecodeBody(w, r, 16*1024, &req) {
		return
	}
	id := productId.String()
	prod, err := h.store.GetProduct(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "product_not_found", "no such product")
		return
	} else if err != nil {
		h.internalError(w, r, "promote_product_get", "get failed", err)
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
		h.internalError(w, r, "promote_product_write", "promote failed", err)
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
		h.internalError(w, r, "promote_product_reload", "reload failed", err)
		return
	}
	h.writeProduct(ctx, w, r, p)
}
