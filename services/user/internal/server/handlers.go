package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/services/user/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/user/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

const (
	maxDisplayName = 100
	maxAvatarURL   = 2048
)

var preferredCurrencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func (h *Handlers) UpsertUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("service") {
		problem(w, r, http.StatusForbidden, "forbidden", "role service required")
		return
	}
	var req api.UpsertUserRequest
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // internal endpoint; cap a buggy caller
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if req.Email == "" || req.DisplayName == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "email and display_name are required")
		return
	}
	hint := ""
	if req.LocaleHint != nil {
		hint = *req.LocaleHint
	}
	u, err := h.store.Upsert(r.Context(), req.Email, req.DisplayName, req.AvatarUrl, currencyForLocale(hint))
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "upsert failed")
		return
	}
	writeJSON(w, http.StatusOK, toAPI(u))
}

func (h *Handlers) GetUser(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if claims.Subject != userId.String() && !claims.HasRole("service") && !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "may only read your own user")
		return
	}
	u, err := h.store.Get(r.Context(), userId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "user_not_found", "no such user")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "get failed")
		return
	}
	writeJSON(w, http.StatusOK, toAPI(u))
}

func (h *Handlers) UpdateUser(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if claims.Subject != userId.String() {
		problem(w, r, http.StatusForbidden, "forbidden", "may only edit your own profile")
		return
	}
	var req api.UpdateUserRequest
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if trimmed == "" || len(trimmed) > maxDisplayName {
			problem(w, r, http.StatusBadRequest, "invalid_body", "display_name must be 1-100 characters")
			return
		}
		req.DisplayName = &trimmed
	}
	if req.AvatarUrl != nil && *req.AvatarUrl != "" {
		if len(*req.AvatarUrl) > maxAvatarURL {
			problem(w, r, http.StatusBadRequest, "invalid_body", "avatar_url must be at most 2048 characters")
			return
		}
		parsed, err := url.Parse(*req.AvatarUrl)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			problem(w, r, http.StatusBadRequest, "invalid_body", "avatar_url must be an http(s) URL")
			return
		}
	}
	if req.PreferredCurrency != nil && !preferredCurrencyRe.MatchString(*req.PreferredCurrency) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "preferred_currency must be a 3-letter uppercase code")
		return
	}
	u, err := h.store.Update(r.Context(), userId, req.DisplayName, req.AvatarUrl, req.PreferredCurrency)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "user_not_found", "no such user")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	writeJSON(w, http.StatusOK, toAPI(u))
}

// DeleteUser is one leg of account deletion; the collection purge and
// auth wipe are the caller's (bff's) other legs. Idempotent so an
// interrupted deletion can be retried to convergence.
func (h *Handlers) DeleteUser(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if claims.Subject != userId.String() {
		problem(w, r, http.StatusForbidden, "forbidden", "may only delete your own account")
		return
	}
	if err := h.store.Delete(r.Context(), userId); err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toAPI(u store.User) api.User {
	roles := make([]api.UserRoles, len(u.Roles))
	for i, r := range u.Roles {
		roles[i] = api.UserRoles(r)
	}
	return api.User{
		Id:                u.ID,
		Email:             u.Email,
		DisplayName:       u.DisplayName,
		AvatarUrl:         u.AvatarURL,
		PreferredCurrency: u.PreferredCurrency,
		Roles:             roles,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
	}
}
