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

	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/session"
)

// securityCSP is the SPA's content security policy. style-src allows
// inline styles (React style props); img-src allows https + data so
// provider avatars and cover art render; connect-src 'self' covers all
// API calls (same origin by construction).
const securityCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'self'; form-action 'self'"

// SecurityHeaders applies the browser hardening set to every response
// (harmless on JSON, load-bearing on the SPA documents).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", securityCSP)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// CheckOrigin enforces same-origin on mutating methods (the second
// CSRF layer next to SameSite=Lax). Browsers send Origin, or at least
// Sec-Fetch-Site, on cross-site requests; a request bearing neither is
// a non-browser client, which CSRF does not apply to (nothing tricked
// it into attaching a victim's cookie).
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
// without a session. This list is the deliberate seam that later grows
// public pages; everything not under /api (the SPA shell, health) is
// public by construction.
var unauthenticated = map[string]bool{
	"/api/auth/login":     true,
	"/api/auth/callback":  true,
	"/api/auth/logout":    true,
	"/api/auth/providers": true,
}

// Authenticate guards /api/*: open the cookie, check the denylist
// (fail-open), refresh the token pair when it is close to expiry, and
// hand the session to the handler via the request context.
func (h *Handlers) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decide on the cleaned path so a non-normalized request
		// (/./api/me, //api/me) cannot slip past the /api/ guard by
		// relying on the router to normalize it afterwards. This is the
		// public trust boundary; it must not depend on a downstream
		// redirect to be correct.
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

// denylisted is fail-open: when Valkey is unreachable the request
// proceeds (logged + counted). The denylist hardens an already short
// access TTL; it is not the primary control.
func (h *Handlers) denylisted(r *http.Request, jti string) bool {
	hit, err := h.cache.DenylistHas(r.Context(), jti)
	if err != nil {
		h.failOpenEvent(r.Context(), "denylist_check", err)
		return false
	}
	return hit
}

// refreshResult is what a rotation publishes for concurrent requests:
// the sealed cookie plus its Max-Age (the session's remaining life).
// The cookie value is AES-GCM ciphertext, so nothing secret rests in
// Valkey in the clear.
type refreshResult struct {
	Cookie string `json:"c"`
	MaxAge int    `json:"m"`
}

// refreshSession rotates the refresh token, coordinating concurrent
// requests so exactly one rotation happens per session: a second
// rotation of the same token would trip the auth service's reuse
// detection and revoke the whole session. A non-error return carries
// the session to serve this request with; an error means the response
// was written.
func (h *Handlers) refreshSession(w http.ResponseWriter, r *http.Request, sess session.Session, claims session.Claims) (session.Session, session.Claims, error) {
	ctx := r.Context()
	key := session.RefreshKey(sess.RefreshToken)
	holder := uuid.NewString()

	locked, err := h.cache.AcquireRefreshLock(ctx, key, holder, lockTTL)
	if err != nil {
		// Valkey down: rotate without coordination. A concurrent tab
		// might trip reuse detection and cost a re-login; availability
		// wins over that edge while the cache is out.
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

	// A prior holder may have already rotated THIS token and released the
	// lock before our request (still bearing the consumed token) arrived.
	// Re-refreshing a consumed token trips the auth service's reuse
	// detection and revokes the whole session, so adopt the published
	// result instead. It is keyed by this token's hash, so a present
	// result is exactly our rotation's successor. (On a Valkey GET error
	// we fall through and rotate: the SETNX just succeeded, so an error
	// here is a rare blip and at worst costs one reuse.)
	if raw, gerr := h.cache.GetRefreshResult(ctx, key); gerr == nil && raw != "" {
		if newSess, newClaims, res, ok := h.decodeResult(raw); ok {
			http.SetCookie(w, h.codec.Cookie(res.Cookie, res.MaxAge))
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
			h.clearAndUnauthorized(w, r)
			return session.Session{}, session.Claims{}, cerr
		}
		sealed, serr := h.codec.Seal(newSess)
		if serr != nil {
			h.logger.ErrorContext(ctx, "cookie seal failed", "err", serr)
			writeProblem(w, r, http.StatusInternalServerError, "internal", "session seal failed")
			return session.Session{}, session.Claims{}, serr
		}
		if body, merr := json.Marshal(refreshResult{Cookie: sealed, MaxAge: int(pair.RefreshExpiresIn)}); merr == nil {
			if perr := h.cache.PutRefreshResult(ctx, key, string(body), resultTTL); perr != nil {
				h.failOpenEvent(ctx, "refresh_publish", perr)
			}
		}
		http.SetCookie(w, h.codec.Cookie(sealed, int(pair.RefreshExpiresIn)))
		return newSess, newClaims, nil

	case errors.As(err, &reused):
		// The chain is dead; denylist any reported still-live jtis so
		// stolen access tokens die at this edge too.
		if derr := h.cache.DenylistAdd(ctx, reused.RevokeJTIs, h.accessTTL+time.Minute); derr != nil {
			h.failOpenEvent(ctx, "denylist_add", derr)
		}
		h.clearAndUnauthorized(w, r)
		return session.Session{}, session.Claims{}, err

	case errors.Is(err, authclient.ErrRefreshRejected):
		h.clearAndUnauthorized(w, r)
		return session.Session{}, session.Claims{}, err

	case errors.Is(err, authclient.ErrUserUnavailable):
		// The refresh token was NOT consumed; keep the cookie. Serve
		// on the current token if it still has life, else ask the
		// client to retry.
		if h.now().Before(claims.Exp) {
			return sess, claims, nil
		}
		writeProblem(w, r, http.StatusServiceUnavailable, "user_unavailable", "session refresh blocked on a dependency; retry")
		return session.Session{}, session.Claims{}, err

	default:
		h.logger.WarnContext(ctx, "token refresh failed", "err", err)
		if h.now().Before(claims.Exp) {
			return sess, claims, nil
		}
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "auth service unavailable")
		return session.Session{}, session.Claims{}, err
	}
}

// decodeResult turns a published rotation result into the session it
// carries, or ok=false when it is malformed or no longer unsealable
// (any failure is treated as "no usable result").
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

// adoptRefreshResult rides a concurrent rotation. A still-valid token
// does not wait at all (the rotation holder re-seals the cookie on ITS
// response); only a request whose token already expired polls for the
// published result. A timeout returns 401 WITHOUT clearing the cookie:
// the rotation may still land, letting the browser's next request
// adopt it.
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
			return newSess, newClaims, nil
		}
		select {
		case <-r.Context().Done():
			h.unauthorized(w, r)
			return session.Session{}, session.Claims{}, errAdopt
		case <-time.After(h.pollInterval):
		}
	}
	h.unauthorized(w, r)
	return session.Session{}, session.Claims{}, errAdopt
}
