// Catalog-candidate submissions: filing, reading, canceling,
// acknowledging, and the admin verdict queue.

package server

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
)

// CreateSubmission relays a catalog-candidate filing.
func (h *Handlers) CreateSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.CreateSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// GetSubmission relays the latest-submission read.
func (h *Handlers) GetSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.GetSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// CancelSubmission relays a pending-submission cancel.
func (h *Handlers) CancelSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.CancelSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// AckSubmissionResolution relays the approval-banner acknowledgement.
func (h *Handlers) AckSubmissionResolution(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.AckSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListSubmissions relays the admin queue; collection enforces the role.
func (h *Handlers) ListSubmissions(w http.ResponseWriter, r *http.Request, params api.ListSubmissionsParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	up := &collectionapi.ListSubmissionsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.collection.ListSubmissions(r.Context(), sess.AccessToken, up)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// SubmitVerdict relays an admin verdict; collection enforces the role
// and orchestrates approve_new.
func (h *Handlers) SubmitVerdict(w http.ResponseWriter, r *http.Request, submissionId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.SubmitVerdict(r.Context(), sess.AccessToken, submissionId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}
