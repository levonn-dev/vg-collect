package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
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
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
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
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
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
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
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

// DeleteMe deletes the account everywhere. Order is self-healing:
// data first - collection, then the social graph - then auth, then
// the user row that login resolution anchors on; an interruption
// leaves a login-able account that can retry, and the email fallback
// re-attaches an abandoned partial.
func (h *Handlers) DeleteMe(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
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

// writeRelay serves an upstream answer verbatim (pass-throughs are
// never cached at the bff: one staleness authority per data type).
func writeRelay(w http.ResponseWriter, status int, contentType string, body []byte) {
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// SearchCatalog proxies catalog discovery search to the enrichment
// service with the user's own token.
func (h *Handlers) SearchCatalog(w http.ResponseWriter, r *http.Request, params api.SearchCatalogParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.enrichment.Search(r.Context(), sess.AccessToken, string(params.Type), params.Q)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// GetFx relays the enrichment service's exchange-rate snapshot with
// the user's own token.
func (h *Handlers) GetFx(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.enrichment.FX(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListPlatforms relays the platform catalog for the custom-entry picker.
func (h *Handlers) ListPlatforms(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.enrichment.ListPlatforms(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ResolveProduct proxies find-or-create; the body passes through
// untouched (enrichment owns its validation).
func (h *Handlers) ResolveProduct(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_body", "unreadable body")
		return
	}
	res, err := h.enrichment.Resolve(r.Context(), sess.AccessToken, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// GetProduct proxies a catalog product read.
func (h *Handlers) GetProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.enrichment.Product(r.Context(), sess.AccessToken, productId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListUnmatchedProducts relays the admin worklist. The bff holds no
// role logic for admin routes: enrichment enforces, problems relay.
func (h *Handlers) ListUnmatchedProducts(w http.ResponseWriter, r *http.Request, params api.ListUnmatchedProductsParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	up := &enrichapi.ListUnmatchedProductsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.enrichment.UnmatchedProducts(r.Context(), sess.AccessToken, up)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListCommunityProducts relays the admin community listing. The bff
// holds no role logic for admin routes: enrichment enforces, problems
// relay.
func (h *Handlers) ListCommunityProducts(w http.ResponseWriter, r *http.Request, params api.ListCommunityProductsParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	cp := &enrichapi.ListCommunityProductsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.enrichment.CommunityProducts(r.Context(), sess.AccessToken, cp)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// SetProductMapping relays the moderated mapping correction.
func (h *Handlers) SetProductMapping(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.SetProductMapping(r.Context(), sess.AccessToken, productId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// DeleteProduct is the one orchestrated admin call: only collection
// can see entries, so the bff runs the reference check there before
// relaying enrichment's guarded delete. Collection's 403 relays
// first, which keeps the role gate ahead of any cross-user fact.
func (h *Handlers) DeleteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	refs, err := h.collection.CountProductReferences(r.Context(), sess.AccessToken, productId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	if refs.Status != http.StatusOK {
		writeRelay(w, refs.Status, refs.ContentType, refs.Body)
		return
	}
	var count struct {
		EntryCount int64 `json:"entry_count"`
	}
	if err := json.Unmarshal(refs.Body, &count); err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection answered malformed")
		return
	}
	if count.EntryCount > 0 {
		detail := fmt.Sprintf("%d entries reference this product - repoint or delete those entries first", count.EntryCount)
		if count.EntryCount == 1 {
			detail = "1 entry references this product - repoint or delete it first"
		}
		writeProblem(w, r, http.StatusConflict, "product_referenced", detail)
		return
	}
	res, err := h.enrichment.DeleteProduct(r.Context(), sess.AccessToken, productId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// TriggerRefresh relays the admin's immediate catalog-refresh trigger.
func (h *Handlers) TriggerRefresh(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.enrichment.TriggerRefresh(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// TriggerRematch relays the admin's immediate entry-rematch trigger.
func (h *Handlers) TriggerRematch(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.TriggerRematch(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// CreateSubmission relays a catalog-candidate filing.
func (h *Handlers) CreateSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.CreateSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// GetSubmission relays the latest-submission read.
func (h *Handlers) GetSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.GetSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// CancelSubmission relays a pending-submission cancel.
func (h *Handlers) CancelSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.CancelSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// AckSubmissionResolution relays the approval-banner acknowledgement.
func (h *Handlers) AckSubmissionResolution(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.AckSubmission(r.Context(), sess.AccessToken, entryId)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListSubmissions relays the admin queue; collection enforces the role.
func (h *Handlers) ListSubmissions(w http.ResponseWriter, r *http.Request, params api.ListSubmissionsParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	up := &collectionapi.ListSubmissionsParams{Limit: params.Limit, Offset: params.Offset}
	res, err := h.collection.ListSubmissions(r.Context(), sess.AccessToken, up)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// SubmitVerdict relays an admin verdict; collection enforces the role
// and orchestrates approve_new.
func (h *Handlers) SubmitVerdict(w http.ResponseWriter, r *http.Request, submissionId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.SubmitVerdict(r.Context(), sess.AccessToken, submissionId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// CreateCommunityProduct relays the admin mint; enrichment enforces
// the role.
func (h *Handlers) CreateCommunityProduct(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.CreateCommunityProduct(r.Context(), sess.AccessToken, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// PromoteProduct relays the in-place promotion.
func (h *Handlers) PromoteProduct(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.PromoteProduct(r.Context(), sess.AccessToken, productId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// ListPromoteCandidates relays the sweep worklist.
func (h *Handlers) ListPromoteCandidates(w http.ResponseWriter, r *http.Request, params api.ListPromoteCandidatesParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	up := &enrichapi.ListPromoteCandidatesParams{Limit: params.Limit, Offset: params.Offset, ProductId: params.ProductId}
	res, err := h.enrichment.PromoteCandidates(r.Context(), sess.AccessToken, up)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// DismissPromoteCandidate relays a candidate dismissal.
func (h *Handlers) DismissPromoteCandidate(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.enrichment.DismissPromoteCandidate(r.Context(), sess.AccessToken, productId, body)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// readCapped reads a pass-through body under the standard cap; a
// false return means the 400 was already written.
func readCapped(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_body", "unreadable body")
		return nil, false
	}
	return body, true
}

func castSlice[T ~string, U ~string](in *[]T) *[]U {
	if in == nil {
		return nil
	}
	out := make([]U, len(*in))
	for i, v := range *in {
		out[i] = U(v)
	}
	return &out
}

func castVal[T ~string, U ~string](in *T) *U {
	if in == nil {
		return nil
	}
	u := U(*in)
	return &u
}

// collectionListParams re-types the mirrored query params for the
// generated collection client (the two contracts are byte-identical;
// only the Go package differs).
func collectionListParams(p api.ListEntriesParams) *collectionapi.ListEntriesParams {
	return &collectionapi.ListEntriesParams{
		ItemType:      castSlice[api.ListEntriesParamsItemType, collectionapi.ListEntriesParamsItemType](p.ItemType),
		Status:        castSlice[api.ListEntriesParamsStatus, collectionapi.ListEntriesParamsStatus](p.Status),
		Packaging:     castSlice[api.ListEntriesParamsPackaging, collectionapi.ListEntriesParamsPackaging](p.Packaging),
		Region:        castSlice[api.ListEntriesParamsRegion, collectionapi.ListEntriesParamsRegion](p.Region),
		ItemCondition: castSlice[api.ListEntriesParamsItemCondition, collectionapi.ListEntriesParamsItemCondition](p.ItemCondition),
		PlatformId:    p.PlatformId,
		TagId:         p.TagId,
		Sort:          castVal[api.ListEntriesParamsSort, collectionapi.ListEntriesParamsSort](p.Sort),
		Order:         castVal[api.ListEntriesParamsOrder, collectionapi.ListEntriesParamsOrder](p.Order),
		GroupBy:       castVal[api.ListEntriesParamsGroupBy, collectionapi.ListEntriesParamsGroupBy](p.GroupBy),
		Limit:         p.Limit,
		Offset:        p.Offset,
	}
}

// relayCollection funnels every collection pass-through: session
// check happened at the caller; any client error is an infrastructure
// fault answered 502.
func (h *Handlers) relayCollection(w http.ResponseWriter, r *http.Request, res collectionclient.Result, err error) {
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// relayCollectionMutation relays a mutating collection answer and, on
// success, invalidates the caller's recommendations (their library
// changed under the composition).
func (h *Handlers) relayCollectionMutation(w http.ResponseWriter, r *http.Request, sub string, res collectionclient.Result, err error) {
	if err == nil && res.Status < http.StatusMultipleChoices {
		if cerr := h.cache.InvalidateRecs(r.Context(), sub); cerr != nil {
			h.failOpenEvent(r.Context(), "recs_invalidate", cerr)
		}
	}
	h.relayCollection(w, r, res, err)
}

// GetRecommendations composes the collection library summary with
// enrichment scoring, cached per user for about an hour. The bff owns
// this cache because it owns the composition; the user's own entry
// mutations invalidate it, and a degraded score is never cached (it
// would pin a bad answer for the whole TTL).
func (h *Handlers) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	if body, err := h.cache.GetRecs(r.Context(), claims.Sub); err != nil {
		h.failOpenEvent(r.Context(), "recs_get", err)
		h.cacheLookupEvent(r.Context(), "recs", "miss")
	} else if body != nil {
		h.cacheLookupEvent(r.Context(), "recs", "hit")
		writeRawJSON(w, body)
		return
	} else {
		h.cacheLookupEvent(r.Context(), "recs", "miss")
	}
	lib, err := h.collection.LibrarySummary(r.Context(), sess.AccessToken)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	req := enrichapi.ScoreRequest{Library: make([]enrichapi.LibraryEntry, len(lib.Library))}
	for i, g := range lib.Library {
		req.Library[i] = enrichapi.LibraryEntry{IgdbGameId: g.IgdbGameId, Rating: g.Rating, Status: g.Status}
	}
	body, degraded, err := h.enrichment.Score(r.Context(), sess.AccessToken, req)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	if !degraded {
		if perr := h.cache.PutRecs(r.Context(), claims.Sub, body, h.recsTTL); perr != nil {
			h.failOpenEvent(r.Context(), "recs_put", perr)
		}
	}
	writeRawJSON(w, body)
}

// ListEntries proxies the collection list matrix.
func (h *Handlers) ListEntries(w http.ResponseWriter, r *http.Request, params api.ListEntriesParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.ListEntries(r.Context(), sess.AccessToken, collectionListParams(params))
	h.relayCollection(w, r, res, err)
}

// CreateEntry proxies entry creation.
func (h *Handlers) CreateEntry(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.CreateEntry(r.Context(), sess.AccessToken, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// GetEntry proxies a single entry read.
func (h *Handlers) GetEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.GetEntry(r.Context(), sess.AccessToken, entryId)
	h.relayCollection(w, r, res, err)
}

// UpdateEntry proxies the full-state replace.
func (h *Handlers) UpdateEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.UpdateEntry(r.Context(), sess.AccessToken, entryId, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// DeleteEntry proxies entry deletion.
func (h *Handlers) DeleteEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.DeleteEntry(r.Context(), sess.AccessToken, entryId)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// AckEntryRegionMismatch proxies the region-mismatch banner dismiss.
// A stamp-only ack, not a composition change, so it relays plain
// (no recommendations invalidation) - the same choice as
// AckSubmissionResolution below.
func (h *Handlers) AckEntryRegionMismatch(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.AckRegionMismatch(r.Context(), sess.AccessToken, entryId)
	h.relayCollection(w, r, res, err)
}

// ReorderEntry proxies the backlog drag.
func (h *Handlers) ReorderEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.ReorderEntry(r.Context(), sess.AccessToken, entryId, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// BulkUpdateEntries proxies the transactional bulk tag/status/
// storage-location update (browser body untouched; collection owns
// the guards, the per-entry tag cap, and the all-or-nothing
// transaction). A mutation like every other entry write, so it
// invalidates recommendations the same way.
func (h *Handlers) BulkUpdateEntries(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.BulkUpdateEntries(r.Context(), sess.AccessToken, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// ListTags / CreateTag / RenameTag / DeleteTag proxy the tag surface.
func (h *Handlers) ListTags(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.ListTags(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) CreateTag(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.CreateTag(r.Context(), sess.AccessToken, body)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) RenameTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.RenameTag(r.Context(), sess.AccessToken, tagId, body)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) DeleteTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.DeleteTag(r.Context(), sess.AccessToken, tagId)
	h.relayCollection(w, r, res, err)
}

// publishIfListed fires the social publish event after a successful
// view write whose RESULT landed visibility=listed - the response
// body governs, not the request body: a request that asked for
// listed but the stored view came back some other way must not fire,
// and a request that did not ask for listed but the stored view came
// back listed anyway must. Fail-open: the write itself already
// succeeded in collection; a lost event costs a feed entry until the
// next listed transition, never the write.
func (h *Handlers) publishIfListed(r *http.Request, accessToken string, res collectionclient.Result) {
	if res.Status < http.StatusOK || res.Status >= http.StatusMultipleChoices {
		return
	}
	var view struct {
		Id         uuid.UUID `json:"id"`
		Visibility string    `json:"visibility"`
	}
	if err := json.Unmarshal(res.Body, &view); err != nil || view.Visibility != "listed" || view.Id == uuid.Nil {
		return
	}
	if err := h.social.RecordPublish(r.Context(), accessToken, view.Id); err != nil {
		h.failOpenEvent(r.Context(), "social_publish_event", err)
	}
}

// ListViews / CreateView / UpdateView / DeleteView proxy saved views.
func (h *Handlers) ListViews(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.ListViews(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) CreateView(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.CreateView(r.Context(), sess.AccessToken, body)
	if err == nil {
		h.publishIfListed(r, sess.AccessToken, res)
	}
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) UpdateView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.UpdateView(r.Context(), sess.AccessToken, viewId, body)
	if err == nil {
		h.publishIfListed(r, sess.AccessToken, res)
	}
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) DeleteView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.DeleteView(r.Context(), sess.AccessToken, viewId)
	h.relayCollection(w, r, res, err)
}

// collectionDashboardParams re-types the mirrored dashboard filter
// params for the generated collection client (byte-identical
// contracts; only the Go package differs).
func collectionDashboardParams(p api.GetDashboardParams) *collectionapi.GetDashboardParams {
	return &collectionapi.GetDashboardParams{
		ItemType:      castSlice[api.GetDashboardParamsItemType, collectionapi.GetDashboardParamsItemType](p.ItemType),
		Status:        castSlice[api.GetDashboardParamsStatus, collectionapi.GetDashboardParamsStatus](p.Status),
		Packaging:     castSlice[api.GetDashboardParamsPackaging, collectionapi.GetDashboardParamsPackaging](p.Packaging),
		Region:        castSlice[api.GetDashboardParamsRegion, collectionapi.GetDashboardParamsRegion](p.Region),
		ItemCondition: castSlice[api.GetDashboardParamsItemCondition, collectionapi.GetDashboardParamsItemCondition](p.ItemCondition),
		PlatformId:    p.PlatformId,
		TagId:         p.TagId,
	}
}

// GetDashboard proxies the collection-composed dashboard (cached by
// its owner, never here - one staleness authority per data type),
// forwarding the filter dimensions.
func (h *Handlers) GetDashboard(w http.ResponseWriter, r *http.Request, params api.GetDashboardParams) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.GetDashboard(r.Context(), sess.AccessToken, collectionDashboardParams(params))
	h.relayCollection(w, r, res, err)
}

// GetValueHistory proxies the collection value-over-time series
// (single-source: the collection service owns its cache and
// invalidation; the bff never caches pass-throughs).
func (h *Handlers) GetValueHistory(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.GetValueHistory(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

// proxyOTLP relays a browser OTLP batch to the collector agent
// verbatim; ProxyTraces and ProxyMetrics are thin wrappers selecting
// the signal. Session-gated like every /api route; the body is
// capped; the collector's response status and body pass through so
// the web SDK sees real OTLP semantics. Never cached.
func (h *Handlers) proxyOTLP(w http.ResponseWriter, r *http.Request, signal string) {
	if _, _, ok := session.FromContext(r.Context()); !ok {
		h.unauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_body", "request body unreadable or over 1MiB")
		return
	}
	if h.otlpProxyURL == "" {
		// Accept and drop: telemetry must never break the app.
		w.WriteHeader(http.StatusOK)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.otlpProxyURL+"/v1/"+signal, bytes.NewReader(body))
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collector request could not be built")
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	if enc := r.Header.Get("Content-Encoding"); enc != "" {
		req.Header.Set("Content-Encoding", enc)
	}
	res, err := h.otlpHTTP.Do(req)
	if err != nil {
		// The 502 shows in RED metrics; the line carries the cause
		// (DNS, refused, timeout) that the status alone loses.
		h.logger.WarnContext(r.Context(), "browser telemetry relay failed", "err", err)
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collector unavailable")
		return
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collector response unreadable")
		return
	}
	writeRelay(w, res.StatusCode, res.Header.Get("Content-Type"), out)
}

// ProxyTraces relays browser OTLP trace batches to the collector agent.
func (h *Handlers) ProxyTraces(w http.ResponseWriter, r *http.Request) {
	h.proxyOTLP(w, r, "traces")
}

// ProxyMetrics relays browser OTLP metric batches to the collector agent.
func (h *Handlers) ProxyMetrics(w http.ResponseWriter, r *http.Request) {
	h.proxyOTLP(w, r, "metrics")
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
