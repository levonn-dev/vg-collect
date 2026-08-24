package server

import (
	"errors"
	"net/http"
	"net/url"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/user/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

// validAvatarURL reports whether s is an absolute http(s) URL - a
// structural check the contract's string schema cannot express itself
// (length is specval's job, maxLength 2048). Shared by UpdateUser,
// which 400s the caller on failure, and UpsertUser, which drops the
// value instead (see its own comment for why).
func validAvatarURL(s string) bool {
	parsed, err := url.Parse(s)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func (h *Handlers) UpsertUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("service") {
		problem(w, r, http.StatusForbidden, "forbidden", "role service required")
		return
	}
	var req api.UpsertUserRequest
	if !httpkit.DecodeBody(w, r, maxBodyBytes, &req) { // internal endpoint; cap a buggy caller
		return
	}
	// email/display_name are contract-required (api/user.yaml), but
	// the schema's required keyword only checks key presence, not
	// blankness: neither field carries a minLength, so a
	// present-but-empty value passes specval untouched, and this check
	// is what actually rejects "".
	if req.Email == "" || req.DisplayName == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "email and display_name are required")
		return
	}
	// avatar_url arrives verbatim from the OIDC picture claim (see
	// services/auth/internal/oidc/rp.go), so a misbehaving provider can
	// send anything. This endpoint has no browser on the other end to
	// hand a 400 to - it is the login path itself - so an invalid claim
	// is dropped (stored as absent) rather than failing the login.
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
		if h.currencySeeds != nil {
			h.currencySeeds.Add(r.Context(), 1, metric.WithAttributes(attribute.String("source", currencySource)))
		}
		h.logger.InfoContext(r.Context(), "account created",
			"user_id", u.ID.String(), "preferred_currency", u.PreferredCurrency, "currency_source", currencySource)
	}
	if h.accountUpserts != nil {
		h.accountUpserts.Add(r.Context(), 1, metric.WithAttributes(attribute.String("outcome", outcome)))
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
		// Shape (length, character set) is specval's job: it enforces
		// common.yaml's Handle schema ahead of this handler
		// (store.ValidHandle checks the same shape but has no
		// production caller). The pattern already requires alnum
		// first/last characters, so a handle reaching here has no
		// leading/trailing whitespace to trim. Reserved-handle is a
		// business rule the schema cannot express, so it stays a hand
		// check.
		if store.ReservedHandles[store.NormalizeHandle(*req.Handle)] {
			problem(w, r, http.StatusBadRequest, "invalid_body", "that handle is reserved")
			return
		}
	}
	// profile_visibility/landing_page's enum membership is now
	// specval's job (common contract enum, no gap); these two lines
	// only convert the generated enum-typed pointer to the plain
	// *string store.Update expects, the same pointer-type-conversion
	// idiom collection's UpdateEntry uses for its own optional enum
	// fields.
	visibility := (*string)(req.ProfileVisibility)
	landingPage := (*string)(req.LandingPage)
	// avatar_url's length cap is the contract's job (maxLength 2048,
	// enforced by specval ahead of this handler); validAvatarURL is the
	// scheme/host check, a structural check the string schema cannot
	// express. Empty stays exempt - the documented clear-the-field
	// convention.
	if req.AvatarUrl != nil && *req.AvatarUrl != "" && !validAvatarURL(*req.AvatarUrl) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "avatar_url must be an http(s) URL")
		return
	}
	// preferred_currency's pattern (^[A-Z]{3}$) is now specval's job:
	// an exact mirror of CurrencyCode's contract pattern, no gap.
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

// DeleteUser is the last leg of account deletion; the collection
// purge, the social graph purge, and the auth identity wipe are the
// caller's (bff's) other legs, run in that order ahead of this one.
// Idempotent so an interrupted deletion can be retried to convergence.
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
	if h.accountDeletes != nil {
		h.accountDeletes.Add(r.Context(), 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	}
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
