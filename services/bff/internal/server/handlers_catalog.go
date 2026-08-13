// Catalog discovery: search, FX rates, the platform list,
// product resolve and read, and the composed recommendations feed.

package server

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/enrichapi"
)

// SearchCatalog proxies catalog discovery search to the enrichment
// service with the user's own token.
func (h *Handlers) SearchCatalog(w http.ResponseWriter, r *http.Request, params api.SearchCatalogParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.Search(r.Context(), sess.AccessToken, string(params.Type), params.Q)
	h.relayEnrichment(w, r, res, err)
}

// GetFx relays the enrichment service's exchange-rate snapshot with
// the user's own token.
func (h *Handlers) GetFx(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.FX(r.Context(), sess.AccessToken)
	h.relayEnrichment(w, r, res, err)
}

// ListPlatforms relays the platform catalog for the custom-entry picker.
func (h *Handlers) ListPlatforms(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.ListPlatforms(r.Context(), sess.AccessToken)
	h.relayEnrichment(w, r, res, err)
}

// ResolveProduct proxies find-or-create; the body passes through
// untouched (enrichment owns its validation).
func (h *Handlers) ResolveProduct(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := httpkit.ReadCapped(w, r, 16*1024)
	if !ok {
		return
	}
	res, err := h.enrichment.Resolve(r.Context(), sess.AccessToken, body)
	h.relayEnrichment(w, r, res, err)
}

// GetProduct proxies a catalog product read.
func (h *Handlers) GetProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.Product(r.Context(), sess.AccessToken, productId)
	h.relayEnrichment(w, r, res, err)
}

// GetRecommendations composes the collection library summary with
// enrichment scoring, cached per user for about an hour. The bff owns
// this cache because it owns the composition; the user's own entry
// mutations invalidate it, and a degraded score is never cached (it
// would pin a bad answer for the whole TTL).
func (h *Handlers) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if body, err := h.cache.GetRecs(r.Context(), claims.Sub); err != nil {
		h.failOpenEvent(r.Context(), "recs_get", err)
		h.cacheLookupEvent(r.Context(), "recs", "miss")
	} else if body != nil {
		h.cacheLookupEvent(r.Context(), "recs", "hit")
		writeRawJSON(w, body)
		return
	} else {
		h.cacheLookupEvent(r.Context(), "recs", "miss")
	}
	lib, err := h.collection.LibrarySummary(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	req := enrichapi.ScoreRequest{Library: make([]enrichapi.LibraryEntry, len(lib.Library))}
	for i, g := range lib.Library {
		req.Library[i] = enrichapi.LibraryEntry{IgdbGameId: g.IgdbGameId, Rating: g.Rating, Status: g.Status}
	}
	body, degraded, err := h.enrichment.Score(r.Context(), sess.AccessToken, req)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	if !degraded {
		if perr := h.cache.PutRecs(r.Context(), claims.Sub, body, h.recsTTL); perr != nil {
			h.failOpenEvent(r.Context(), "recs_put", perr)
		}
	}
	writeRawJSON(w, body)
}
