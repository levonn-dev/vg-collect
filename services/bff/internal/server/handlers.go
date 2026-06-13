package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/bff/internal/session"
	"github.com/levonn-dev/vg-collect/services/bff/internal/userclient"
)

var _ api.ServerInterface = (*Handlers)(nil)

// Login starts a login as a browser navigation: failures land back on
// the login page with ?error=<code>, never as JSON nobody renders. The
// dev provider has no external IdP, so its start and callback collapse
// into this single request (token mint, cookie, straight home).
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request, params api.LoginParams) {
	if params.Provider == "dev" {
		user := ""
		if params.User != nil {
			user = *params.User
		}
		pair, err := h.auth.DevToken(r.Context(), user)
		if err != nil {
			h.redirectLoginError(w, r, err)
			return
		}
		h.completeNavLogin(w, r, pair)
		return
	}
	authorizeURL, err := h.auth.Start(r.Context(), params.Provider)
	if err != nil {
		h.redirectLoginError(w, r, err)
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// Callback is the redirect URI registered with providers: it finishes
// the login server-to-server; code/state outcomes are never exposed to
// scripts (navigation in, navigation out).
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request, params api.CallbackParams) {
	code, state := "", ""
	if params.Code != nil {
		code = *params.Code
	}
	if params.State != nil {
		state = *params.State
	}
	if code == "" || state == "" {
		http.Redirect(w, r, "/login?error=login_failed", http.StatusFound)
		return
	}
	pair, err := h.auth.Callback(r.Context(), code, state)
	if err != nil {
		h.redirectLoginError(w, r, err)
		return
	}
	h.completeNavLogin(w, r, pair)
}

// completeNavLogin seals the pair into the session cookie and sends
// the browser home.
func (h *Handlers) completeNavLogin(w http.ResponseWriter, r *http.Request, pair authclient.TokenPair) {
	sealed, err := h.codec.Seal(session.Session{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "cookie seal failed", "err", err)
		http.Redirect(w, r, "/login?error=login_failed", http.StatusFound)
		return
	}
	http.SetCookie(w, h.codec.Cookie(sealed, int(pair.RefreshExpiresIn)))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handlers) redirectLoginError(w http.ResponseWriter, r *http.Request, err error) {
	code := "login_failed"
	switch {
	case errors.Is(err, authclient.ErrEmailUnverified):
		code = "email_unverified"
	case errors.Is(err, authclient.ErrProviderError):
		code = "provider_error"
	}
	h.logger.WarnContext(r.Context(), "login failed", "err", err)
	http.Redirect(w, r, "/login?error="+code, http.StatusFound)
}

// Logout is allowlisted (it does its own cookie work) and idempotent:
// whatever state the session is in, the cookie is gone afterwards.
// Denylist and revocation are best-effort; a logged-out user must not
// be blocked by a flaky dependency, and the short access TTL plus the
// server-side family revocation bound the exposure.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(session.CookieName); err == nil {
		if sess, err := h.codec.Open(ck.Value); err == nil {
			if claims, err := session.ParseClaims(sess.AccessToken); err == nil {
				ttl := time.Until(claims.Exp) + time.Minute
				if ttl < time.Minute {
					ttl = time.Minute
				}
				if derr := h.cache.DenylistAdd(r.Context(), []string{claims.JTI}, ttl); derr != nil {
					h.failOpenEvent(r.Context(), "denylist_add", derr)
				}
			}
			if rerr := h.auth.Revoke(r.Context(), sess.RefreshToken); rerr != nil {
				h.logger.ErrorContext(r.Context(), "refresh chain revocation failed", "err", rerr)
			}
		}
	}
	http.SetCookie(w, h.codec.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

// ListProviders relays the auth service's enabled-provider list so the
// login page renders exactly the buttons that can succeed.
func (h *Handlers) ListProviders(w http.ResponseWriter, r *http.Request) {
	names, err := h.auth.Providers(r.Context())
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "auth service unavailable")
		return
	}
	writeJSON(w, http.StatusOK, api.Providers{Providers: names})
}

// GetMe composes the signed-in user's profile from the user service,
// briefly cached (the bff caches only what it composes; pass-throughs
// stay uncached).
func (h *Handlers) GetMe(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	if body, err := h.cache.GetMe(r.Context(), claims.Sub); err != nil {
		h.failOpenEvent(r.Context(), "me_get", err)
	} else if body != nil {
		writeRawJSON(w, body)
		return
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
	me := api.Me{Id: u.Id, Email: u.Email, DisplayName: u.DisplayName, AvatarUrl: u.AvatarUrl, Roles: roles}
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
