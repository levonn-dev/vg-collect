package server

import (
	"errors"
	"net/http"
	"net/url"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/services/user/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

// validAvatarURL reports whether s is an absolute http(s) URL, a check
// the schema can't express (length is specval's job, maxLength 2048).
// UpdateUser 400s on failure; UpsertUser drops the value instead.
func validAvatarURL(s string) bool {
	parsed, err := url.Parse(s)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func (h *Handlers) UpsertUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.IsService() || claims.Subject != "svc:auth" {
		problem(w, r, http.StatusForbidden, "forbidden", "auth service credential required")
		return
	}
	var req api.UpsertUserRequest
	if !httpkit.DecodeBody(w, r, maxBodyBytes, &req) { // internal endpoint; cap a buggy caller
		return
	}
	// required (api/user.yaml) only checks key presence, not blankness (no
	// minLength); this check is what actually rejects a present-but-empty value.
	if req.Email == "" || req.DisplayName == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "email and display_name are required")
		return
	}
	// avatar_url arrives verbatim from the OIDC claim (services/auth/internal/oidc/rp.go);
	// an invalid one is dropped, not 400ed - this is the login path, no browser to answer.
	avatarURL := req.AvatarUrl
	invalidAvatar := avatarURL != nil && *avatarURL != "" && !validAvatarURL(*avatarURL)
	if invalidAvatar {
		avatarURL = nil
	}
	hint := ""
	if req.LocaleHint != nil {
		hint = *req.LocaleHint
	}
	currency, currencySource := currencyForLocale(hint)
	u, created, err := h.store.Upsert(r.Context(), req.Email, req.DisplayName, avatarURL, currency)
	if err != nil {
		h.internalError(w, r, "upsert", "upsert failed", err)
		return
	}
	if invalidAvatar {
		h.logger.WarnContext(r.Context(), "upsert avatar_url invalid; stored without one", "user_id", u.ID.String())
	}
	outcome := "existing"
	if created {
		outcome = "created"
		// The currency seed happens only when the insert takes; an
		// existing row keeps its currency regardless of the hint.
		vgotel.Count(r.Context(), h.currencySeeds, attribute.String("source", currencySource))
		h.logger.InfoContext(r.Context(), "account created",
			"user_id", u.ID.String(), "preferred_currency", u.PreferredCurrency, "currency_source", currencySource)
	}
	vgotel.Count(r.Context(), h.accountUpserts, attribute.String("outcome", outcome))
	writeJSON(w, http.StatusOK, toAPI(u))
}

func (h *Handlers) GetUser(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if claims.Subject != userId.String() && !claims.IsService() && !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "may only read your own user")
		return
	}
	u, err := h.store.Get(r.Context(), userId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "user_not_found", "no such user")
		return
	}
	if err != nil {
		h.internalError(w, r, "get", "get failed", err)
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
	if !httpkit.DecodeBody(w, r, maxBodyBytes, &req) {
		return
	}
	if req.Handle != nil {
		// Shape is specval's job (common.yaml's Handle schema; store.ValidHandle
		// mirrors it but has no production caller). The pattern requires alnum
		// first/last chars, so no whitespace trim is needed. Reserved-handle
		// is a business rule the schema can't express, so it stays a hand check.
		if store.ReservedHandles[store.NormalizeHandle(*req.Handle)] {
			problem(w, r, http.StatusBadRequest, "invalid_body", "that handle is reserved")
			return
		}
	}
	// enum membership is specval's job; these two lines only convert the
	// generated enum-typed pointer to the plain *string store.Update expects.
	visibility := (*string)(req.ProfileVisibility)
	landingPage := (*string)(req.LandingPage)
	// avatar_url's length cap is specval's job (maxLength 2048);
	// validAvatarURL is the scheme/host structural check the schema can't
	// express. Empty stays exempt (the clear-the-field convention).
	if req.AvatarUrl != nil && *req.AvatarUrl != "" && !validAvatarURL(*req.AvatarUrl) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "avatar_url must be an http(s) URL")
		return
	}
	// preferred_currency's pattern is specval's job (mirrors CurrencyCode).
	u, err := h.store.Update(r.Context(), userId, req.Handle, req.AvatarUrl, req.PreferredCurrency, visibility, landingPage, h.handleCooldown)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "user_not_found", "no such user")
		return
	}
	if errors.Is(err, store.ErrHandleTaken) {
		problem(w, r, http.StatusConflict, "handle_taken", "another account already owns that handle")
		return
	}
	if errors.Is(err, store.ErrHandleCooldown) {
		problem(w, r, http.StatusTooManyRequests, "handle_cooldown", "handle was changed too recently; try again later")
		return
	}
	if err != nil {
		h.internalError(w, r, "update", "update failed", err)
		return
	}
	writeJSON(w, http.StatusOK, toAPI(u))
}

// DeleteUser is the last leg of account deletion; the bff runs collection
// purge, social purge, and auth wipe ahead of this one. Idempotent, so a
// retry converges.
func (h *Handlers) DeleteUser(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if claims.Subject != userId.String() {
		problem(w, r, http.StatusForbidden, "forbidden", "may only delete your own account")
		return
	}
	deleted, err := h.store.Delete(r.Context(), userId)
	if err != nil {
		h.internalError(w, r, "delete", "delete failed", err)
		return
	}
	// noop means a retry converged on an already-deleted row.
	outcome := "noop"
	if deleted {
		outcome = "deleted"
	}
	vgotel.Count(r.Context(), h.accountDeletes, attribute.String("outcome", outcome))
	h.logger.InfoContext(r.Context(), "account deleted", "user_id", userId.String(), "outcome", outcome)
	w.WriteHeader(http.StatusNoContent)
}

func toAPI(u store.User) api.User {
	roles := make([]common.Role, len(u.Roles))
	for i, r := range u.Roles {
		roles[i] = common.Role(r)
	}
	return api.User{
		Id:                u.ID,
		Email:             u.Email,
		Handle:            u.Handle,
		AvatarUrl:         u.AvatarURL,
		ProfileVisibility: common.Visibility(u.ProfileVisibility),
		PreferredCurrency: u.PreferredCurrency,
		LandingPage:       common.LandingPage(u.LandingPage),
		Roles:             roles,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
	}
}
