// Product-identity administration: unmatched/community worklists, mapping corrections, minting, promotion.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
)

// ListUnmatchedProducts relays the admin worklist; enrichment enforces the role, problems relay.
func (h *Handlers) ListUnmatchedProducts(w http.ResponseWriter, r *http.Request, params api.ListUnmatchedProductsParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	up := &enrichapi.ListUnmatchedProductsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.enrichment.UnmatchedProducts(r.Context(), sess.AccessToken, up)
	h.relayEnrichment(w, r, res, err)
}

// ListCommunityProducts relays the admin community listing; enrichment enforces the role, problems relay.
func (h *Handlers) ListCommunityProducts(w http.ResponseWriter, r *http.Request, params api.ListCommunityProductsParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	cp := &enrichapi.ListCommunityProductsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.enrichment.CommunityProducts(r.Context(), sess.AccessToken, cp)
	h.relayEnrichment(w, r, res, err)
}

// SetProductMapping relays the moderated mapping correction.
func (h *Handlers) SetProductMapping(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.SetProductMapping(r.Context(), sess.AccessToken, productId, body)
	h.relayEnrichment(w, r, res, err)
}

// DeleteProduct is the one orchestrated admin call: the bff checks
// collection's entry references first, so its 403 gates access before any cross-user fact leaks.
func (h *Handlers) DeleteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
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
	h.relayEnrichment(w, r, res, err)
}

// CreateCommunityProduct relays the admin mint; enrichment enforces the role.
func (h *Handlers) CreateCommunityProduct(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.CreateCommunityProduct(r.Context(), sess.AccessToken, body)
	h.relayEnrichment(w, r, res, err)
}

// PromoteProduct relays the in-place promotion.
func (h *Handlers) PromoteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.PromoteProduct(r.Context(), sess.AccessToken, productId, body)
	h.relayEnrichment(w, r, res, err)
}

// ListPromoteCandidates relays the sweep worklist.
func (h *Handlers) ListPromoteCandidates(w http.ResponseWriter, r *http.Request, params api.ListPromoteCandidatesParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	up := &enrichapi.ListPromoteCandidatesParams{Limit: params.Limit, Offset: params.Offset, ProductId: params.ProductId}
	res, err := h.enrichment.PromoteCandidates(r.Context(), sess.AccessToken, up)
	h.relayEnrichment(w, r, res, err)
}

// DismissPromoteCandidate relays a candidate dismissal.
func (h *Handlers) DismissPromoteCandidate(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.DismissPromoteCandidate(r.Context(), sess.AccessToken, productId, body)
	h.relayEnrichment(w, r, res, err)
}
