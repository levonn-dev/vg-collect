// Catalog discovery: search, FX rates, platform list, product resolve/read, and composed recommendations.

package server

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
)

// SearchCatalog proxies catalog discovery search to enrichment with the user's own token.
func (h *Handlers) SearchCatalog(w http.ResponseWriter, r *http.Request, params api.SearchCatalogParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.Search(r.Context(), sess.AccessToken, string(params.Type), params.Q)
	h.relayEnrichment(w, r, res, err)
}

// GetFx relays enrichment's exchange-rate snapshot with the user's own token.
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

// ResolveProduct proxies find-or-create; body passes through untouched, enrichment owns validation.
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

// GetRecommendations composes collection's library with enrichment scoring,
// cached per user (~1h); a degraded score is never cached, else it pins a bad answer for the TTL.
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
		req.Library[i] = enrichapi.LibraryEntry(g)
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
