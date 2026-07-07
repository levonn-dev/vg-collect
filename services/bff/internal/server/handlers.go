package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/enrichapi"
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
	} else if body != nil {
		writeRawJSON(w, body)
		return
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

// GetDashboard proxies the collection-composed dashboard (cached by
// its owner, never here - one staleness authority per data type).
func (h *Handlers) GetDashboard(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return
	}
	res, err := h.collection.GetDashboard(r.Context(), sess.AccessToken)
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

// ProxyTraces relays browser OTLP trace batches to the collector
// agent verbatim. Session-gated like every /api route; the body is
// capped; the collector's response status and body pass through so
// the web SDK sees real OTLP semantics. Never cached.
func (h *Handlers) ProxyTraces(w http.ResponseWriter, r *http.Request) {
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
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.otlpProxyURL+"/v1/traces", bytes.NewReader(body))
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
