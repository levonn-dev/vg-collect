package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
)

// securityCSP is the SPA's CSP: style-src allows inline (React style
// props), img-src allows https+data (avatars/cover art), connect-src is same-origin API only.
const securityCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'self'; form-action 'self'"

// securityPermissionsPolicy denies unused browser features, unconditionally.
const securityPermissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=()"

// SecurityHeaders applies the hardening set to every response (harmless
// on JSON, load-bearing on SPA documents); HSTS only when cookies are Secure.
func (h *Handlers) SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", securityCSP)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		hd.Set("Permissions-Policy", securityPermissionsPolicy)
		if h.cookieSecure {
			hd.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// CheckOrigin is the second CSRF layer next to SameSite=Lax; browsers send
// Origin or Sec-Fetch-Site cross-site, so bearing neither means a non-browser client, exempt from CSRF.
func (h *Handlers) CheckOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if origin := r.Header.Get("Origin"); origin != "" {
				if !slices.Contains(h.publicOrigins, origin) {
					writeProblem(w, r, http.StatusForbidden, "origin_forbidden", "cross-origin mutation rejected")
					return
				}
			} else if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
				writeProblem(w, r, http.StatusForbidden, "origin_forbidden", "cross-site mutation rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// unauthenticated is the explicit allowlist of /api paths reachable
// without a session; everything outside /api is public by construction.
var unauthenticated = map[string]bool{
	"/api/auth/login":     true,
	"/api/auth/callback":  true,
	"/api/auth/logout":    true,
	"/api/auth/providers": true,
}

// Authenticate guards /api/*: opens the cookie, checks the denylist
// (fail-open), refreshes near expiry, and hands the session to the handler via context.
func (h *Handlers) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean first so a non-normalized request (/./api/me, //api/me) can't
		// slip the /api/ guard; this trust boundary must not depend on downstream normalization.
		p := path.Clean(r.URL.Path)
		if !strings.HasPrefix(p, "/api/") || unauthenticated[p] {
			next.ServeHTTP(w, r)
			return
		}
		ck, err := r.Cookie(session.CookieName)
		if err != nil {
			h.unauthorized(w, r)
			return
		}
		sess, err := h.codec.Open(ck.Value)
		if err != nil {
			h.clearAndUnauthorized(w, r)
			return
		}
		claims, err := session.ParseClaims(sess.AccessToken)
		if err != nil {
			h.clearAndUnauthorized(w, r)
			return
		}
		if h.denylisted(r, claims.JTI) {
			h.clearAndUnauthorized(w, r)
			return
		}
		if h.now().After(claims.Exp.Add(-h.refreshWindow)) {
			sess, claims, err = h.refreshSession(w, r, sess, claims)
			if err != nil {
				return // the response is already written
			}
		}
		next.ServeHTTP(w, r.WithContext(session.NewContext(r.Context(), sess, claims)))
	})
}

// denylisted is fail-open: an unreachable Valkey lets the request proceed
// (logged + counted); it hardens an already-short access TTL, not the primary control.
func (h *Handlers) denylisted(r *http.Request, jti string) bool {
	hit, err := h.cache.DenylistHas(r.Context(), jti)
	if err != nil {
		h.failOpenEvent(r.Context(), "denylist_check", err)
		return false
	}
	return hit
}

// refreshResult is what a rotation publishes: the sealed cookie plus its
// Max-Age. The cookie is AES-GCM ciphertext, nothing secret in Valkey in the clear.
type refreshResult struct {
	Cookie string `json:"c"`
	MaxAge int    `json:"m"`
}

// refreshSession coordinates concurrent requests to exactly one rotation
// per session (reuse of the same token revokes it); non-error carries the
// session to serve; error means the response was already written.
func (h *Handlers) refreshSession(w http.ResponseWriter, r *http.Request, sess session.Session, claims session.Claims) (session.Session, session.Claims, error) {
	ctx := r.Context()
	key := session.RefreshKey(sess.RefreshToken)
	holder := uuid.NewString()

	locked, err := h.cache.AcquireRefreshLock(ctx, key, holder, lockTTL)
	if err != nil {
		// Valkey down: rotate without coordination; a concurrent tab may trip
		// reuse detection and cost a re-login, but availability wins while the cache is out.
		h.failOpenEvent(ctx, "refresh_lock", err)
		locked, holder = true, ""
	}
	if !locked {
		return h.adoptRefreshResult(w, r, sess, claims, key)
	}
	if holder != "" {
		defer func() {
			if rerr := h.cache.ReleaseRefreshLock(ctx, key, holder); rerr != nil {
				h.failOpenEvent(ctx, "refresh_unlock", rerr)
			}
		}()
	}

	// A prior holder may have already rotated and released the lock before
	// this request arrived, still bearing the consumed token; re-refreshing it
	// trips reuse detection, so adopt the published result (keyed by this token's hash) instead.
	if raw, gerr := h.cache.GetRefreshResult(ctx, key); gerr == nil && raw != "" {
		if newSess, newClaims, res, ok := h.decodeResult(raw); ok {
			http.SetCookie(w, h.codec.Cookie(res.Cookie, res.MaxAge))
			h.refreshEvent(ctx, "adopted")
			return newSess, newClaims, nil
		}
	}

	pair, err := h.auth.Refresh(ctx, sess.RefreshToken)
	var reused *authclient.ReusedError
	switch {
	case err == nil:
		newSess := session.Session{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}
		newClaims, cerr := session.ParseClaims(newSess.AccessToken)
		if cerr != nil {
			h.logger.ErrorContext(ctx, "refresh returned an unparseable access token", "err", cerr)
			h.refreshEvent(ctx, "failed")
			h.clearAndUnauthorized(w, r)
			return session.Session{}, session.Claims{}, cerr
		}
		sealed, serr := h.codec.Seal(newSess)
		if serr != nil {
			h.logger.ErrorContext(ctx, "cookie seal failed", "err", serr)
			h.refreshEvent(ctx, "failed")
			writeProblem(w, r, http.StatusInternalServerError, "internal", "session seal failed")
			return session.Session{}, session.Claims{}, serr
		}
		if body, merr := json.Marshal(refreshResult{Cookie: sealed, MaxAge: int(pair.RefreshExpiresIn)}); merr == nil {
			if perr := h.cache.PutRefreshResult(ctx, key, string(body), resultTTL); perr != nil {
				h.failOpenEvent(ctx, "refresh_publish", perr)
			}
		}
		http.SetCookie(w, h.codec.Cookie(sealed, int(pair.RefreshExpiresIn)))
		h.refreshEvent(ctx, "rotated")
		return newSess, newClaims, nil

	case errors.As(err, &reused):
		// The chain is dead; denylist any reported still-live jtis so stolen access tokens die here too.
		h.logger.WarnContext(ctx, "refresh token reuse detected; session family revoked",
			"sub", claims.Sub, "revoked_jtis", len(reused.RevokeJTIs))
		if derr := h.cache.DenylistAdd(ctx, reused.RevokeJTIs, h.accessTTL+time.Minute); derr != nil {
			h.failOpenEvent(ctx, "denylist_add", derr)
		}
		h.refreshEvent(ctx, "reuse_revoked")
		h.clearAndUnauthorized(w, r)
		return session.Session{}, session.Claims{}, err

	case errors.Is(err, authclient.ErrRefreshRejected):
		h.refreshEvent(ctx, "rejected")
		h.clearAndUnauthorized(w, r)
		return session.Session{}, session.Claims{}, err

	case errors.Is(err, authclient.ErrUserUnavailable):
		// Token was NOT consumed; keep the cookie. Serve on the current token
		// if it still has life, else ask the client to retry.
		if h.now().Before(claims.Exp) {
			h.refreshEvent(ctx, "deferred")
			return sess, claims, nil
		}
		h.refreshEvent(ctx, "failed")
		writeProblem(w, r, http.StatusServiceUnavailable, "user_unavailable", "session refresh blocked on a dependency; retry")
		return session.Session{}, session.Claims{}, err

	default:
		h.logger.WarnContext(ctx, "token refresh failed", "err", err)
		if h.now().Before(claims.Exp) {
			h.refreshEvent(ctx, "deferred")
			return sess, claims, nil
		}
		h.refreshEvent(ctx, "failed")
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "auth service unavailable")
		return session.Session{}, session.Claims{}, err
	}
}

// decodeResult turns a published rotation result into its session, or
// ok=false when malformed or unsealable (any failure means "no usable result").
func (h *Handlers) decodeResult(raw string) (session.Session, session.Claims, refreshResult, bool) {
	var res refreshResult
	if json.Unmarshal([]byte(raw), &res) != nil {
		return session.Session{}, session.Claims{}, res, false
	}
	newSess, err := h.codec.Open(res.Cookie)
	if err != nil {
		return session.Session{}, session.Claims{}, res, false
	}
	newClaims, err := session.ParseClaims(newSess.AccessToken)
	if err != nil {
		return session.Session{}, session.Claims{}, res, false
	}
	return newSess, newClaims, res, true
}

// adoptRefreshResult rides a concurrent rotation: a still-valid token
// doesn't wait (its own response re-seals it); only an expired token polls.
// A timeout returns 401 WITHOUT clearing the cookie so a later request can still adopt the rotation.
func (h *Handlers) adoptRefreshResult(w http.ResponseWriter, r *http.Request, sess session.Session, claims session.Claims, key string) (session.Session, session.Claims, error) {
	if h.now().Before(claims.Exp) {
		return sess, claims, nil
	}
	errAdopt := errors.New("refresh result not adopted")
	deadline := h.now().Add(h.pollBudget)
	for h.now().Before(deadline) {
		raw, err := h.cache.GetRefreshResult(r.Context(), key)
		if err != nil {
			h.failOpenEvent(r.Context(), "refresh_result", err)
			break
		}
		if raw != "" {
			newSess, newClaims, res, ok := h.decodeResult(raw)
			if !ok {
				break
			}
			http.SetCookie(w, h.codec.Cookie(res.Cookie, res.MaxAge))
			h.refreshEvent(r.Context(), "adopted")
			return newSess, newClaims, nil
		}
		select {
		case <-r.Context().Done():
			h.refreshEvent(r.Context(), "adopt_timeout")
			h.unauthorized(w, r)
			return session.Session{}, session.Claims{}, errAdopt
		case <-time.After(h.pollInterval):
		}
	}
	// Reached on budget expiry or a give-up break above; every arrival here got a spurious 401.
	h.logger.WarnContext(r.Context(), "refresh result adoption timed out", "sub", claims.Sub)
	h.refreshEvent(r.Context(), "adopt_timeout")
	h.unauthorized(w, r)
	return session.Session{}, session.Claims{}, errAdopt
}
