// Catalog submission review: filing a custom entry for review,
// caller-facing reads, and admin verdicts.

package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// Submission abuse caps (contract-documented). Cancelled rows persist
// precisely so the rolling window counts every creation - a
// cancel/recreate loop cannot evade it.
const (
	submissionPendingCap = 10
	submissionDailyCap   = 20
	submissionRateWindow = 24 * time.Hour
)

func toAPISubmission(s store.Submission) api.Submission {
	out := api.Submission{
		Id: s.ID, EntryId: s.EntryID, Status: common.SubmissionStatus(s.Status),
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	out.RejectReason = s.RejectReason
	out.ProductId = s.ProductID
	out.ReviewedAt = s.ReviewedAt
	out.ResolutionAckAt = s.ResolutionAckAt
	return out
}

// CreateSubmission files a custom entry into the catalog review
// queue, under the per-user abuse caps.
func (h *Handlers) CreateSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	entry, err := h.store.GetEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "submission_create_entry_load", "get failed", err)
		return
	}
	if entry.ProductID != nil {
		problem(w, r, http.StatusBadRequest, "entry_not_custom", "only custom entries submit to the catalog")
		return
	}
	pending, err := h.store.CountPendingSubmissions(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, "submission_pending_count", "count failed", err)
		return
	}
	if pending >= submissionPendingCap {
		h.logger.WarnContext(r.Context(), "submission cap hit", "user_id", userID, "cap", "pending")
		problem(w, r, http.StatusTooManyRequests, "too_many_pending_submissions",
			fmt.Sprintf("at most %d submissions may be pending; wait for review or cancel one", submissionPendingCap))
		return
	}
	recent, err := h.store.CountSubmissionsSince(r.Context(), userID, time.Now().UTC().Add(-submissionRateWindow))
	if err != nil {
		h.internalError(w, r, "submission_window_count", "count failed", err)
		return
	}
	if recent >= submissionDailyCap {
		h.logger.WarnContext(r.Context(), "submission cap hit", "user_id", userID, "cap", "rate")
		problem(w, r, http.StatusTooManyRequests, "submission_rate_limited",
			fmt.Sprintf("at most %d submissions per rolling 24h; try again later", submissionDailyCap))
		return
	}
	sub, err := h.store.CreateSubmission(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrSubmissionPending) {
		problem(w, r, http.StatusConflict, "submission_pending", "a submission is already pending for this entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "submission_create", "create failed", err)
		return
	}
	h.logger.InfoContext(r.Context(), "submission created", "submission_id", sub.ID, "entry_id", sub.EntryID)
	h.submissionEvent(r.Context(), "created")
	writeJSON(w, http.StatusCreated, toAPISubmission(sub))
}

// GetSubmission serves the entry page's latest-submission read.
func (h *Handlers) GetSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetEntry(r.Context(), userID, entryId); errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	} else if err != nil {
		h.internalError(w, r, "submission_get_entry_load", "get failed", err)
		return
	}
	sub, err := h.store.LatestSubmissionForEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "the entry has no submissions")
		return
	}
	if err != nil {
		h.internalError(w, r, "submission_latest_get", "get failed", err)
		return
	}
	writeJSON(w, http.StatusOK, toAPISubmission(sub))
}

// AckSubmissionResolution stamps the caller's approved submission for
// the entry so the approval banner stops reappearing. The two-step
// ownership idiom mirrors GetSubmission: a foreign or missing entry is
// entry_not_found, an entry with no approved submission is
// submission_not_found. Idempotent - an already-acked submission is a
// 204 no-op.
func (h *Handlers) AckSubmissionResolution(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetEntry(r.Context(), userID, entryId); errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	} else if err != nil {
		h.internalError(w, r, "submission_ack_entry_load", "get failed", err)
		return
	}
	sub, err := h.store.LatestApprovedSubmissionForEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "the entry has no approved submission")
		return
	}
	if err != nil {
		h.internalError(w, r, "submission_latest_approved_get", "get failed", err)
		return
	}
	if sub.ResolutionAckAt == nil {
		if err := h.store.AckSubmissionResolution(r.Context(), sub.ID); err != nil {
			h.internalError(w, r, "submission_ack", "ack failed", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// CancelSubmission flips the entry's pending submission to cancelled.
func (h *Handlers) CancelSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.CancelSubmission(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "nothing is pending for this entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "submission_cancel", "cancel failed", err)
		return
	}
	h.submissionEvent(r.Context(), "cancelled")
	w.WriteHeader(http.StatusNoContent)
}

// ListSubmissions pages the pending queue with live proposals.
func (h *Handlers) ListSubmissions(w http.ResponseWriter, r *http.Request, params api.ListSubmissionsParams) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	// limit/offset are already known within the contract's 1-500/>=0
	// bounds by the time this runs (specval; api/collection.yaml
	// declares both on ListSubmissions). Only the default-when-absent
	// case needs handling here.
	limit := 200
	if params.Limit != nil {
		limit = *params.Limit
	}
	offset := 0
	if params.Offset != nil {
		offset = *params.Offset
	}
	rows, total, err := h.store.ListPendingSubmissions(r.Context(), limit, offset)
	if err != nil {
		h.internalError(w, r, "list_submissions", "list failed", err)
		return
	}
	page := api.AdminSubmissionsPage{Submissions: make([]common.AdminSubmission, 0, len(rows)), TotalCount: total}
	for _, row := range rows {
		as := common.AdminSubmission{
			Id: row.ID, EntryId: row.EntryID, UserId: row.UserID,
			Status:      common.SubmissionStatus(row.Status),
			DisplayName: row.DisplayName, ItemType: common.ItemType(row.ItemType),
			Region:    row.Region,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		as.PlatformName = row.PlatformName
		as.Edition = row.Edition
		as.CoverUrl = row.CoverURL
		if row.FirstReleaseDate != nil {
			d := openapi_types.Date{Time: *row.FirstReleaseDate}
			as.FirstReleaseDate = &d
		}
		if len(row.Developers) > 0 {
			devs := row.Developers
			as.Developers = &devs
		}
		if len(row.Publishers) > 0 {
			pubs := row.Publishers
			as.Publishers = &pubs
		}
		page.Submissions = append(page.Submissions, as)
	}
	writeJSON(w, http.StatusOK, page)
}

// SubmitVerdict resolves one pending submission. approve_new is the
// two-phase orchestration: mint (or reuse a prior attempt's recorded
// mint), record on the still-pending row, then adopt+approve - a
// crash between phases retries without twin mints. The only orphan
// window is mint-succeeds-before-record; the guarded product delete
// mops it.
func (h *Handlers) SubmitVerdict(w http.ResponseWriter, r *http.Request, submissionId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	_, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.VerdictRequest
	if !httpkit.DecodeBody(w, r, maxBodyBytes, &body) {
		return
	}
	sub, err := h.store.GetSubmission(r.Context(), submissionId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "no such submission")
		return
	}
	if err != nil {
		h.internalError(w, r, "verdict_get_submission", "get failed", err)
		return
	}
	if sub.Status != "pending" {
		problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
		return
	}

	switch string(body.Action) {
	case "reject":
		if body.Reason == nil || strings.TrimSpace(*body.Reason) == "" {
			problem(w, r, http.StatusBadRequest, "invalid_body", "reject requires a reason")
			return
		}
		out, err := h.store.RejectSubmission(r.Context(), sub.ID, strings.TrimSpace(*body.Reason))
		if errors.Is(err, store.ErrSubmissionResolved) {
			problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
			return
		}
		if err != nil {
			h.internalError(w, r, "verdict_reject", "reject failed", err)
			return
		}
		h.logger.InfoContext(r.Context(), "submission verdict",
			"submission_id", out.ID, "entry_id", out.EntryID, "action", "reject")
		h.submissionEvent(r.Context(), "rejected")
		writeJSON(w, http.StatusOK, toAPISubmission(out))
	case "approve_existing":
		if body.ProductId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "approve_existing requires product_id")
			return
		}
		h.adoptAndApprove(w, r, bearer, sub, *body.ProductId, "approve_existing")
	case "approve_new":
		if body.Product == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "approve_new requires product")
			return
		}
		// Reuse the id a prior approve_new minted and recorded, so a retry
		// never double-mints. Corner: if that recorded product was deleted
		// before the retry, approve_new 404s here; reject, or approve_existing
		// (which overwrites the recorded id), is the escape.
		productID := sub.ProductID
		if productID == nil {
			minted, err := h.enrichment.CreateCommunityProduct(r.Context(), bearer, mintRequest(*body.Product))
			if err != nil {
				problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
				return
			}
			if err := h.store.RecordSubmissionProduct(r.Context(), sub.ID, minted.Id); errors.Is(err, store.ErrSubmissionResolved) {
				problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
				return
			} else if err != nil {
				h.internalError(w, r, "verdict_record_product", "record failed", err)
				return
			}
			productID = &minted.Id
		}
		h.adoptAndApprove(w, r, bearer, sub, *productID, "approve_new")
	default:
		problem(w, r, http.StatusBadRequest, "invalid_body", "action must be approve_new, approve_existing or reject")
	}
}

// adoptAndApprove is the shared verdict tail: fetch the product,
// snapshot it onto the submitter's entry, resolve the row - one
// transaction for the last two. action names the verdict arm for the
// admin audit log.
func (h *Handlers) adoptAndApprove(w http.ResponseWriter, r *http.Request, bearer string, sub store.Submission, productID uuid.UUID, action string) {
	product, err := h.enrichment.GetProduct(r.Context(), bearer, productID)
	if errors.Is(err, enrichmentclient.ErrUnknownProduct) {
		problem(w, r, http.StatusNotFound, "unknown_product", "no such product in the catalog")
		return
	}
	if err != nil {
		problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
		return
	}
	entry, err := h.store.GetEntry(r.Context(), sub.UserID, sub.EntryID)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "verdict_entry_load", "entry load failed", err)
		return
	}
	out, err := h.store.ApproveSubmission(r.Context(), sub.ID, catalogSnapshot(product, entry.Region))
	if errors.Is(err, store.ErrSubmissionResolved) {
		problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
		return
	}
	if err != nil {
		h.internalError(w, r, "verdict_approve", "approve failed", err)
		return
	}
	h.logger.InfoContext(r.Context(), "submission verdict",
		"submission_id", out.ID, "entry_id", out.EntryID, "action", action, "product_id", productID)
	h.submissionEvent(r.Context(), "approved")
	h.invalidateDashboard(r.Context(), sub.UserID)
	writeJSON(w, http.StatusOK, toAPISubmission(out))
}

// mintRequest maps the verdict's curated fields onto enrichment's
// mint request.
func mintRequest(p common.CommunityProductSpec) enrichapi.CreateCommunityProductJSONRequestBody {
	out := enrichapi.CreateCommunityProductJSONRequestBody{
		Type: p.Type,
		Name: p.Name,
	}
	out.PlatformName = p.PlatformName
	out.Region = p.Region
	out.Edition = p.Edition
	out.FirstReleaseDate = p.FirstReleaseDate
	out.Developers = p.Developers
	out.Publishers = p.Publishers
	out.CoverUrl = p.CoverUrl
	return out
}
