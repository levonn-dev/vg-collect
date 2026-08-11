// Product-identity administration: the unmatched and community
// worklists, mapping corrections, community minting, and promotion.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
)

// ListUnmatchedProducts relays the admin worklist. The bff holds no
// role logic for admin routes: enrichment enforces, problems relay.
func (h *Handlers) ListUnmatchedProducts(w http.ResponseWriter, r *http.Request, params api.ListUnmatchedProductsParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	up := &enrichapi.ListUnmatchedProductsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.enrichment.UnmatchedProducts(r.Context(), sess.AccessToken, up)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListCommunityProducts relays the admin community listing. The bff
// holds no role logic for admin routes: enrichment enforces, problems
// relay.
func (h *Handlers) ListCommunityProducts(w http.ResponseWriter, r *http.Request, params api.ListCommunityProductsParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	cp := &enrichapi.ListCommunityProductsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.enrichment.CommunityProducts(r.Context(), sess.AccessToken, cp)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// SetProductMapping relays the moderated mapping correction.
func (h *Handlers) SetProductMapping(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.SetProductMapping(r.Context(), sess.AccessToken, productId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// DeleteProduct is the one orchestrated admin call: only collection
// can see entries, so the bff runs the reference check there before
// relaying enrichment's guarded delete. Collection's 403 relays
// first, which keeps the role gate ahead of any cross-user fact.
func (h *Handlers) DeleteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	refs, err := h.collection.CountProductReferences(r.Context(), sess.AccessToken, productId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	if refs.Status != http.StatusOK {
		writeRelay(w, refs.Status, refs.ContentType, refs.Body)
		return
	}
	var count struct {
		EntryCount int64 `json:"entry_count"`
	}
	if err := json.Unmarshal(refs.Body, &count); err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection answered malformed")
		return
	}
	if count.EntryCount > 0 {
		detail := fmt.Sprintf("%d entries reference this product - repoint or delete those entries first", count.EntryCount)
		if count.EntryCount == 1 {
			detail = "1 entry references this product - repoint or delete it first"
		}
		writeProblem(w, r, http.StatusConflict, "product_referenced", detail)
		return
	}
	res, err := h.enrichment.DeleteProduct(r.Context(), sess.AccessToken, productId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// CreateCommunityProduct relays the admin mint; enrichment enforces
// the role.
func (h *Handlers) CreateCommunityProduct(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.CreateCommunityProduct(r.Context(), sess.AccessToken, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// PromoteProduct relays the in-place promotion.
func (h *Handlers) PromoteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.PromoteProduct(r.Context(), sess.AccessToken, productId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListPromoteCandidates relays the sweep worklist.
func (h *Handlers) ListPromoteCandidates(w http.ResponseWriter, r *http.Request, params api.ListPromoteCandidatesParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	up := &enrichapi.ListPromoteCandidatesParams{Limit: params.Limit, Offset: params.Offset, ProductId: params.ProductId}
	res, err := h.enrichment.PromoteCandidates(r.Context(), sess.AccessToken, up)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// DismissPromoteCandidate relays a candidate dismissal.
func (h *Handlers) DismissPromoteCandidate(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.DismissPromoteCandidate(r.Context(), sess.AccessToken, productId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}
