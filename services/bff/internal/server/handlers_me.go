// The signed-in user's own profile: read, update,
// linked-identity management, and full account deletion.

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

// GetMe composes the signed-in user's profile from the user service,
// briefly cached (the bff caches only what it composes; pass-throughs
// stay uncached).
func (h *Handlers) GetMe(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if body, err := h.cache.GetMe(r.Context(), claims.Sub); err != nil {
		h.failOpenEvent(r.Context(), "me_get", err)
		h.cacheLookupEvent(r.Context(), "me", "miss")
	} else if body != nil {
		h.cacheLookupEvent(r.Context(), "me", "hit")
		writeRawJSON(w, body)
		return
	} else {
		h.cacheLookupEvent(r.Context(), "me", "miss")
	}
	u, err := h.users.Get(r.Context(), claims.Sub, sess.AccessToken)
	if errors.Is(err, userclient.ErrUserNotFound) {
		// The account vanished mid-session; the session dies with it.
		http.SetCookie(w, h.codec.ClearCookie())
		h.unauthorized(w, r)
		return
	}
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	roles := make([]string, len(u.Roles))
	for i, role := range u.Roles {
		roles[i] = string(role)
	}
	me := api.Me{Id: u.Id, Email: u.Email, Handle: u.Handle, AvatarUrl: u.AvatarUrl,
		Roles: roles, PreferredCurrency: u.PreferredCurrency,
		ProfileVisibility: api.MeProfileVisibility(u.ProfileVisibility),
		LandingPage:       api.MeLandingPage(u.LandingPage)}
	body, err := json.Marshal(me)
	if err != nil {
		writeProblem(w, r, http.StatusInternalServerError, "internal", "encoding failed")
		return
	}
	if perr := h.cache.PutMe(r.Context(), claims.Sub, body, h.meTTL); perr != nil {
		h.failOpenEvent(r.Context(), "me_put", perr)
	}
	writeRawJSON(w, body)
}

// UpdateMe forwards a profile edit and drops the cached projection so
// the app bar updates on the next fetch, not at TTL.
func (h *Handlers) UpdateMe(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.users.Update(r.Context(), claims.Sub, sess.AccessToken, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	if res.Status != http.StatusOK {
		writeRelay(w, res.Status, res.ContentType, res.Body)
		return
	}
	if cerr := h.cache.InvalidateMe(r.Context(), claims.Sub); cerr != nil {
		h.failOpenEvent(r.Context(), "me_invalidate", cerr)
	}
	var u userapi.User
	if err := json.Unmarshal(res.Body, &u); err != nil {
		writeProblem(w, r, http.StatusInternalServerError, "internal", "user service answer unreadable")
		return
	}
	roles := make([]string, len(u.Roles))
	for i, role := range u.Roles {
		roles[i] = string(role)
	}
	writeJSON(w, http.StatusOK, api.Me{Id: u.Id, Email: u.Email, Handle: u.Handle, AvatarUrl: u.AvatarUrl,
		Roles: roles, PreferredCurrency: u.PreferredCurrency,
		ProfileVisibility: api.MeProfileVisibility(u.ProfileVisibility),
		LandingPage:       api.MeLandingPage(u.LandingPage)})
}

// GetMyIdentities lists the session account's linked logins. Uncached:
// it changes exactly when the user links or unlinks.
func (h *Handlers) GetMyIdentities(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	ids, err := h.auth.ListIdentities(r.Context(), claims.Sub, sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "auth service unavailable")
		return
	}
	out := api.Identities{Identities: make([]api.Identity, len(ids))}
	for i, id := range ids {
		out.Identities[i] = api.Identity{Id: id.Id, Provider: id.Provider, Email: id.Email, CreatedAt: id.CreatedAt}
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteMyIdentity unlinks one login, relaying the auth service's two
// user-meaningful refusals.
func (h *Handlers) DeleteMyIdentity(w http.ResponseWriter, r *http.Request, identityId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	err := h.auth.DeleteIdentity(r.Context(), identityId, sess.AccessToken)
	switch {
	case errors.Is(err, authclient.ErrIdentityNotFound):
		writeProblem(w, r, http.StatusNotFound, "identity_not_found", "no such linked login on your account")
	case errors.Is(err, authclient.ErrLastIdentity):
		writeProblem(w, r, http.StatusConflict, "last_identity", "an account must keep at least one login")
	case err != nil:
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "auth service unavailable")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteMe deletes the account everywhere. Order is self-healing:
// data first - collection, then the social graph - then auth, then
// the user row that login resolution anchors on; an interruption
// leaves a login-able account that can retry, and the email fallback
// re-attaches an abandoned partial.
func (h *Handlers) DeleteMe(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.PurgeUserData(r.Context(), sess.AccessToken)
	if err != nil || res.Status != http.StatusNoContent {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection purge failed; retry")
		return
	}
	if sres, serr := h.social.PurgeUserData(r.Context(), sess.AccessToken); serr != nil || sres.Status != http.StatusNoContent {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social purge failed; retry")
		return
	}
	if err := h.auth.DeleteUserAuth(r.Context(), claims.Sub, sess.AccessToken); err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "login erase failed; retry")
		return
	}
	if err := h.users.Delete(r.Context(), claims.Sub, sess.AccessToken); err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "account delete failed; retry")
		return
	}
	ttl := max(time.Until(claims.Exp)+time.Minute, time.Minute)
	if derr := h.cache.DenylistAdd(r.Context(), []string{claims.JTI}, ttl); derr != nil {
		h.failOpenEvent(r.Context(), "denylist_add", derr)
	}
	if cerr := h.cache.InvalidateMe(r.Context(), claims.Sub); cerr != nil {
		h.failOpenEvent(r.Context(), "me_invalidate", cerr)
	}
	if cerr := h.cache.InvalidateRecs(r.Context(), claims.Sub); cerr != nil {
		h.failOpenEvent(r.Context(), "recs_invalidate", cerr)
	}
	http.SetCookie(w, h.codec.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}
