// Tag CRUD for the caller's collection.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

func toAPITag(t store.Tag) api.Tag {
	return api.Tag{Id: t.ID, Name: t.Name, EntryCount: t.EntryCount}
}

func validateTagName(name string) string {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 50 {
		return "name must be 1-50 characters"
	}
	return ""
}

// ListTags lists the caller's tags with usage counts.
func (h *Handlers) ListTags(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	tags, err := h.store.ListTags(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, "list failed", err)
		return
	}
	out := make([]api.Tag, len(tags))
	for i, t := range tags {
		out[i] = toAPITag(t)
	}
	writeJSON(w, http.StatusOK, map[string][]api.Tag{"tags": out})
}

// CreateTag creates a tag (unique per user, case-insensitively).
func (h *Handlers) CreateTag(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.TagCreate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if detail := validateTagName(body.Name); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	tag, err := h.store.CreateTag(r.Context(), userID, body.Name)
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "tag_exists", "a tag with that name already exists")
		return
	}
	if errors.Is(err, store.ErrUserTagCapExceeded) {
		problem(w, r, http.StatusTooManyRequests, "cap_exceeded",
			fmt.Sprintf("at most %d tags per user; delete a tag to create another", store.TagCap))
		return
	}
	if err != nil {
		h.internalError(w, r, "create failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, toAPITag(tag))
}

// RenameTag renames a tag.
func (h *Handlers) RenameTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.TagCreate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if detail := validateTagName(body.Name); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	tag, err := h.store.RenameTag(r.Context(), userID, tagId, body.Name)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "tag_not_found", "no such tag")
		return
	}
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "tag_exists", "a tag with that name already exists")
		return
	}
	if err != nil {
		h.internalError(w, r, "rename failed", err)
		return
	}
	writeJSON(w, http.StatusOK, toAPITag(tag))
}

// DeleteTag deletes a tag; entry links cascade, entries survive.
func (h *Handlers) DeleteTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.DeleteTag(r.Context(), userID, tagId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "tag_not_found", "no such tag")
		return
	}
	if err != nil {
		h.internalError(w, r, "delete failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
