// Saved view CRUD.

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// toAPIView maps a stored view; Params round-trips verbatim.
func toAPIView(v store.View) (api.SavedView, error) {
	var params map[string]any
	if err := json.Unmarshal(v.Params, &params); err != nil {
		return api.SavedView{}, err
	}
	return api.SavedView{
		Id: v.ID, Name: v.Name, Slug: v.Slug,
		Visibility:  common.Visibility(v.Visibility),
		PublishedAt: v.PublishedAt,
		Params:      params,
		CreatedAt:   v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}, nil
}

// maxViewParamsBytes caps the opaque view document.
const maxViewParamsBytes = 8192

// viewBody decodes and validates a ViewCreate; the marshaled params
// come back for storage, along with the resolved visibility (default
// private). name's minLength/maxLength, and visibility's enum, are
// specval's job now; name keeps its blank-after-trim guard (minLength
// alone does not catch "   ", only a literal empty string - see
// validateTagName's comment for the sibling gap on tag names).
func viewBody(w http.ResponseWriter, r *http.Request) (api.ViewCreate, []byte, string, bool) {
	var body api.ViewCreate
	if !httpkit.DecodeBody(w, r, maxBodyBytes, &body) {
		return body, nil, "", false
	}
	if strings.TrimSpace(body.Name) == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "name must not be blank")
		return body, nil, "", false
	}
	params, err := json.Marshal(body.Params)
	if err != nil || body.Params == nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "params must be a JSON object")
		return body, nil, "", false
	}
	if len(params) > maxViewParamsBytes { // marshaled bytes, not characters: a storage cap, not a maxLength
		problem(w, r, http.StatusBadRequest, "invalid_body", "params is too large")
		return body, nil, "", false
	}
	visibility := "private"
	if body.Visibility != nil {
		visibility = string(*body.Visibility)
	}
	return body, params, visibility, true
}

func (h *Handlers) respondView(w http.ResponseWriter, r *http.Request, v store.View, status int) {
	out, err := toAPIView(v)
	if err != nil {
		h.internalError(w, r, "view_encode", "view encoding failed", err)
		return
	}
	writeJSON(w, status, out)
}

// ListViews lists the caller's saved views.
func (h *Handlers) ListViews(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	views, err := h.store.ListViews(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, "list_views", "list failed", err)
		return
	}
	if len(views) == 0 {
		// First visit (or factory reset): give the two starter
		// shelves, then re-read so the response includes them.
		if err := h.store.SeedDefaultViews(r.Context(), userID); err != nil {
			h.internalError(w, r, "views_seed", "seed failed", err)
			return
		}
		if views, err = h.store.ListViews(r.Context(), userID); err != nil {
			h.internalError(w, r, "list_views", "list failed", err)
			return
		}
	}
	out := make([]api.SavedView, len(views))
	for i, v := range views {
		av, err := toAPIView(v)
		if err != nil {
			h.internalError(w, r, "view_encode", "view encoding failed", err)
			return
		}
		out[i] = av
	}
	writeJSON(w, http.StatusOK, map[string][]api.SavedView{"views": out})
}

// CreateView saves a view (an opaque frontend params document).
func (h *Handlers) CreateView(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	body, params, visibility, ok := viewBody(w, r)
	if !ok {
		return
	}
	v, err := h.store.CreateView(r.Context(), userID, body.Name, params, visibility)
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "view_exists", "a view with that name already exists")
		return
	}
	if err != nil {
		h.internalError(w, r, "create_view", "create failed", err)
		return
	}
	h.respondView(w, r, v, http.StatusCreated)
}

// UpdateView replaces a saved view's name and params.
func (h *Handlers) UpdateView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	body, params, visibility, ok := viewBody(w, r)
	if !ok {
		return
	}
	v, err := h.store.UpdateView(r.Context(), userID, viewId, body.Name, params, visibility)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "view_not_found", "no such view")
		return
	}
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "view_exists", "a view with that name already exists")
		return
	}
	if err != nil {
		h.internalError(w, r, "update_view", "update failed", err)
		return
	}
	h.respondView(w, r, v, http.StatusOK)
}

// DeleteView deletes a saved view.
func (h *Handlers) DeleteView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.DeleteView(r.Context(), userID, viewId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "view_not_found", "no such view")
		return
	}
	if err != nil {
		h.internalError(w, r, "delete_view", "delete failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
