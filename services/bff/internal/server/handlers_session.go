// Login, callback, logout, and provider-link handlers: the
// browser-navigation auth flows and their outcome redirects.

package server

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
)

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
		h.completeNavLogin(w, r, pair, "login", "/")
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
// a login or an account link server-to-server; code/state outcomes are
// never exposed to scripts (navigation in, navigation out). A link's
// two refusals land back on the account page with a code the frontend
// renders as a message, never as a failed login.
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request, params api.CallbackParams) {
	code, state := "", ""
	if params.Code != nil {
		code = *params.Code
	}
	if params.State != nil {
		state = *params.State
	}
	if code == "" || state == "" {
		h.loginEvent(r.Context(), "login", "failed")
		http.Redirect(w, r, "/login?error=login_failed", http.StatusFound)
		return
	}
	pair, err := h.auth.Callback(r.Context(), code, state)
	switch {
	case errors.Is(err, authclient.ErrLinkConflict):
		h.loginEvent(r.Context(), "link", "conflict")
		http.Redirect(w, r, "/account?link_error=conflict", http.StatusFound)
		return
	case errors.Is(err, authclient.ErrLinkEmailUnverified):
		h.loginEvent(r.Context(), "link", "email_unverified")
		http.Redirect(w, r, "/account?link_error=email_unverified", http.StatusFound)
		return
	case err != nil:
		h.redirectLoginError(w, r, err)
		return
	}
	if pair.LinkedProvider != nil {
		h.completeNavLogin(w, r, pair, "link", "/account?linked="+url.QueryEscape(*pair.LinkedProvider))
		return
	}
	h.completeNavLogin(w, r, pair, "login", "/")
}

// completeNavLogin seals the pair into the session cookie and sends
// the browser to target (home after a login, the account page after a
// link). flow (login|link) labels the outcome counter: success only on
// the cookie-set redirect, so a seal failure counts as failed.
func (h *Handlers) completeNavLogin(w http.ResponseWriter, r *http.Request, pair authclient.TokenPair, flow, target string) {
	sealed, err := h.codec.Seal(session.Session{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "cookie seal failed", "err", err)
		h.loginEvent(r.Context(), flow, "failed")
		http.Redirect(w, r, "/login?error=login_failed", http.StatusFound)
		return
	}
	http.SetCookie(w, h.codec.Cookie(sealed, int(pair.RefreshExpiresIn)))
	h.loginEvent(r.Context(), flow, "success")
	http.Redirect(w, r, target, http.StatusFound)
}

// loginOutcome maps a login failure onto the outcome vocabulary of
// vg.bff.auth.logins (the ?error redirect codes differ only in the
// catch-all, login_failed).
func loginOutcome(err error) string {
	switch {
	case errors.Is(err, authclient.ErrEmailUnverified):
		return "email_unverified"
	case errors.Is(err, authclient.ErrProviderError):
		return "provider_error"
	default:
		return "failed"
	}
}

func (h *Handlers) redirectLoginError(w http.ResponseWriter, r *http.Request, err error) {
	outcome := loginOutcome(err)
	code := outcome
	if code == "failed" {
		code = "login_failed"
	}
	h.loginEvent(r.Context(), "login", outcome)
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
				ttl := max(time.Until(claims.Exp)+time.Minute, time.Minute)
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

// LinkLogin starts linking another provider as a navigation. Outcomes
// travel as /account query params: navigations cannot render JSON.
func (h *Handlers) LinkLogin(w http.ResponseWriter, r *http.Request, params api.LinkLoginParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	if params.Provider == "dev" {
		user := ""
		if params.User != nil {
			user = *params.User
		}
		pair, err := h.auth.DevLink(r.Context(), user, sess.AccessToken)
		if err != nil {
			h.loginEvent(r.Context(), "link", linkOutcome(err))
			http.Redirect(w, r, "/account?link_error="+linkErrorCode(err), http.StatusFound)
			return
		}
		h.completeNavLogin(w, r, pair, "link", "/account?linked=dev")
		return
	}
	authorizeURL, err := h.auth.LinkStart(r.Context(), params.Provider, sess.AccessToken)
	if err != nil {
		h.loginEvent(r.Context(), "link", "failed")
		http.Redirect(w, r, "/account?link_error=link_failed", http.StatusFound)
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// linkOutcome maps a link failure onto the outcome vocabulary of
// vg.bff.auth.logins; linkErrorCode derives the ?link_error redirect
// code from it (they differ only in the catch-all, link_failed).
func linkOutcome(err error) string {
	switch {
	case errors.Is(err, authclient.ErrLinkConflict):
		return "conflict"
	case errors.Is(err, authclient.ErrLinkEmailUnverified):
		return "email_unverified"
	default:
		return "failed"
	}
}

func linkErrorCode(err error) string {
	if o := linkOutcome(err); o != "failed" {
		return o
	}
	return "link_failed"
}
