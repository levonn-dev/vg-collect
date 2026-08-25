// Admin maintenance levers: catalog refresh, entry rematch, snapshot recompute, and normalization sweeps.

package server

import (
	"net/http"
)

// TriggerRefresh relays the admin's immediate catalog-refresh trigger.
func (h *Handlers) TriggerRefresh(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.TriggerRefresh(r.Context(), sess.AccessToken)
	h.relayEnrichment(w, r, res, err)
}

// NormalizeCommunityRegions relays the admin's community-product
// region normalization sweep.
func (h *Handlers) NormalizeCommunityRegions(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.NormalizeCommunityRegions(r.Context(), sess.AccessToken)
	h.relayEnrichment(w, r, res, err)
}

// TriggerRematch relays the admin's immediate entry-rematch trigger.
func (h *Handlers) TriggerRematch(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.TriggerRematch(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

// Resnapshot relays the admin's snapshot-field recompute sweep.
func (h *Handlers) Resnapshot(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.Resnapshot(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

// NormalizePlatforms relays the admin's platform canonicalization sweep.
func (h *Handlers) NormalizePlatforms(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.NormalizePlatforms(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

// NormalizeRegions relays the admin's region normalization sweep.
func (h *Handlers) NormalizeRegions(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.NormalizeRegions(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}
