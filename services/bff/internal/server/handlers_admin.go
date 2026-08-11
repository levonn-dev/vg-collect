// Admin maintenance levers: catalog refresh, entry rematch,
// snapshot recompute, and platform and region normalization sweeps.

package server

import (
	"net/http"

	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
)

// TriggerRefresh relays the admin's immediate catalog-refresh trigger.
func (h *Handlers) TriggerRefresh(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.enrichment.TriggerRefresh(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// NormalizeCommunityRegions relays the admin's community-product
// region normalization sweep (mirrors TriggerRefresh's idiom: no
// relayCollection-style helper exists for enrichment).
func (h *Handlers) NormalizeCommunityRegions(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.enrichment.NormalizeCommunityRegions(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// TriggerRematch relays the admin's immediate entry-rematch trigger.
func (h *Handlers) TriggerRematch(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.TriggerRematch(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// Resnapshot relays the admin's snapshot-field recompute sweep.
func (h *Handlers) Resnapshot(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.Resnapshot(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

// NormalizePlatforms relays the admin's platform canonicalization sweep.
func (h *Handlers) NormalizePlatforms(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.NormalizePlatforms(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

// NormalizeRegions relays the admin's region normalization sweep.
func (h *Handlers) NormalizeRegions(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.NormalizeRegions(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}
