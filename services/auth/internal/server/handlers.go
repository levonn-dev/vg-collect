package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/services/auth/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/auth/internal/oidc"
	"github.com/levonn-dev/vg-collect/services/auth/internal/store"
	"github.com/levonn-dev/vg-collect/services/auth/internal/token"
	"github.com/levonn-dev/vg-collect/services/auth/internal/userclient"
)

// stateTTL bounds the window between starting a provider login and the
// callback returning. First-time consent screens (account picker, scope
// review, a fresh Twitch login) can take a while; 10 minutes follows
// common practice for human-facing OAuth flows.
const stateTTL = 10 * time.Minute

var _ api.ServerInterface = (*Handlers)(nil)

func (h *Handlers) OauthStart(w http.ResponseWriter, r *http.Request) {
	var req api.StartRequest
	if !decodeBody(w, r, &req) {
		return
	}
	p, ok := h.providers[string(req.Provider)]
	if !ok {
		problem(w, r, http.StatusBadRequest, "unknown_provider", "provider not enabled")
		return
	}
	state := oidc.RandomToken()
	nonce := oidc.RandomToken()
	verifier, challenge := oidc.NewPKCE()
	if err := h.store.CreateState(r.Context(), store.AuthState{
		State: state, PKCEVerifier: verifier, Nonce: nonce,
		Provider: p.Name(), ExpiresAt: time.Now().Add(stateTTL),
	}); err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "could not persist login state")
		return
	}
	authorizeURL, err := p.AuthorizeURL(r.Context(), state, nonce, challenge)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "provider_error", "identity provider unavailable")
		return
	}
	writeJSON(w, http.StatusOK, api.StartResponse{AuthorizeUrl: authorizeURL})
}

func (h *Handlers) OauthCallback(w http.ResponseWriter, r *http.Request) {
	var req api.CallbackRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Code == "" || req.State == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "code and state are required")
		return
	}
	st, err := h.store.ConsumeState(r.Context(), req.State)
	if errors.Is(err, store.ErrStateNotFound) {
		problem(w, r, http.StatusBadRequest, "invalid_state", "unknown, expired, or already-used state")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "state lookup failed")
		return
	}
	p, ok := h.providers[st.Provider]
	if !ok {
		problem(w, r, http.StatusBadRequest, "invalid_state", "provider no longer enabled")
		return
	}
	claims, err := p.Exchange(r.Context(), req.Code, st.PKCEVerifier, st.Nonce)
	if err != nil {
		// Upstream faults (network, provider non-200, malformed token
		// response) are gateway errors; everything else is a failed
		// verification of what came back (bad signature, wrong issuer
		// or audience, expiry, nonce mismatch), which is a bad login
		// attempt, not an outage.
		var pe *oidc.ProviderError
		if errors.As(err, &pe) {
			problem(w, r, http.StatusBadGateway, "provider_error", "identity provider exchange failed")
			return
		}
		problem(w, r, http.StatusBadRequest, "invalid_callback", "ID token verification failed")
		return
	}
	if st.LinkUserID != nil {
		h.completeLink(w, r, st.Provider, claims, *st.LinkUserID)
		return
	}
	h.completeLogin(w, r, st.Provider, claims)
}

// OauthLinkStart begins a provider dance that will attach the
// resulting identity to the caller's account instead of logging in.
// The link target comes from the verified token, never the body, so a
// link flow cannot be aimed at another user.
func (h *Handlers) OauthLinkStart(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req api.LinkStartRequest
	if !decodeBody(w, r, &req) {
		return
	}
	p, ok := h.providers[string(req.Provider)]
	if !ok {
		problem(w, r, http.StatusBadRequest, "unknown_provider", "provider not enabled")
		return
	}
	state := oidc.RandomToken()
	nonce := oidc.RandomToken()
	verifier, challenge := oidc.NewPKCE()
	if err := h.store.CreateState(r.Context(), store.AuthState{
		State: state, PKCEVerifier: verifier, Nonce: nonce,
		Provider: p.Name(), ExpiresAt: time.Now().Add(stateTTL), LinkUserID: &userID,
	}); err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "could not persist link state")
		return
	}
	authorizeURL, err := p.AuthorizeURL(r.Context(), state, nonce, challenge)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "provider_error", "identity provider unavailable")
		return
	}
	writeJSON(w, http.StatusOK, api.StartResponse{AuthorizeUrl: authorizeURL})
}

// DevLink is the dev provider's one-hop link (no external IdP, so
// start and callback collapse). 404 when disabled, like DevToken.
func (h *Handlers) DevLink(w http.ResponseWriter, r *http.Request) {
	if !h.devEnabled {
		problem(w, r, http.StatusNotFound, "not_found", "not found")
		return
	}
	userID, _, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req api.DevLinkRequest
	if !decodeBody(w, r, &req) {
		return
	}
	claims, ok := oidc.DevClaims(req.User)
	if !ok {
		problem(w, r, http.StatusBadRequest, "unknown_fixture", "no such fixture user")
		return
	}
	h.completeLink(w, r, "dev", claims, userID)
}

// ListIdentities reports a user's linked logins: the account page's
// data source (self), also readable by services composing on behalf
// of users.
func (h *Handlers) ListIdentities(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	callerID, claims, ok := h.requireUserOrService(w, r)
	if !ok {
		return
	}
	if callerID != userId && !claims.HasRole("service") {
		problem(w, r, http.StatusForbidden, "forbidden", "may only read your own identities")
		return
	}
	ids, err := h.store.ListIdentities(r.Context(), userId)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "identity list failed")
		return
	}
	out := api.Identities{Identities: make([]api.Identity, len(ids))}
	for i, id := range ids {
		out.Identities[i] = api.Identity{Id: id.ID, Provider: id.Provider, Email: id.Email, CreatedAt: id.CreatedAt}
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteIdentity unlinks one of the caller's logins; the store guards
// the last one so an account cannot strand itself.
func (h *Handlers) DeleteIdentity(w http.ResponseWriter, r *http.Request, identityId openapi_types.UUID) {
	userID, _, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	err := h.store.DeleteIdentity(r.Context(), userID, identityId)
	switch {
	case errors.Is(err, store.ErrIdentityNotFound):
		problem(w, r, http.StatusNotFound, "identity_not_found", "no such linked login on your account")
	case errors.Is(err, store.ErrLastIdentity):
		problem(w, r, http.StatusConflict, "last_identity", "an account must keep at least one login")
	case err != nil:
		problem(w, r, http.StatusInternalServerError, "internal", "unlink failed")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteUserAuth erases the caller's auth footprint (identities +
// refresh families) as one step of account deletion. Self only: even
// admins do not delete other people's logins here.
func (h *Handlers) DeleteUserAuth(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	userID, _, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if userID != userId {
		problem(w, r, http.StatusForbidden, "forbidden", "may only erase your own account")
		return
	}
	if err := h.store.DeleteUserAuth(r.Context(), userID); err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "account auth erase failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// completeLink is the link-mode tail of a provider dance: bind the
// presented identity to the linking user (insert-only; conflicts are
// rejected, never merged) and answer a fresh session for that user.
func (h *Handlers) completeLink(w http.ResponseWriter, r *http.Request, provider string, claims oidc.IDClaims, linkUserID uuid.UUID) {
	if claims.Email == "" || !claims.EmailVerified {
		problem(w, r, http.StatusForbidden, "link_email_unverified", "provider did not assert a verified email")
		return
	}
	u, err := h.users.Get(r.Context(), linkUserID)
	if errors.Is(err, userclient.ErrUserNotFound) {
		problem(w, r, http.StatusBadRequest, "link_failed", "linking account no longer exists")
		return
	}
	if err != nil {
		problem(w, r, http.StatusBadGateway, "user_service_error", "user lookup failed")
		return
	}
	err = h.store.BindIdentity(r.Context(), provider, claims.Subject, claims.Email, linkUserID)
	if errors.Is(err, store.ErrIdentityTaken) {
		problem(w, r, http.StatusConflict, "identity_already_linked", "that login already belongs to another account")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "identity binding failed")
		return
	}
	pair, ok := h.newSession(w, r, u)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, api.CallbackResponse{
		AccessToken:      pair.AccessToken,
		TokenType:        pair.TokenType,
		ExpiresIn:        pair.ExpiresIn,
		RefreshToken:     pair.RefreshToken,
		RefreshExpiresIn: pair.RefreshExpiresIn,
		LinkedProvider:   &provider,
	})
}

// completeLogin is the shared tail of every login path (real providers
// and the dev provider). Resolution is identity-first: a login that
// was linked to an account stays on that account even when its email
// would match a different one. The email path serves first-time
// identities and heals identities whose user has vanished.
func (h *Handlers) completeLogin(w http.ResponseWriter, r *http.Request, provider string, claims oidc.IDClaims) {
	// Accepting an unverified email would let an attacker claim
	// someone else's future account through the fallback path.
	if claims.Email == "" || !claims.EmailVerified {
		problem(w, r, http.StatusForbidden, "email_unverified", "provider did not assert a verified email")
		return
	}

	localeHint := bestLanguageTag(r.Header.Get("Accept-Language"))

	ident, err := h.store.ResolveIdentity(r.Context(), provider, claims.Subject, claims.Email)
	switch {
	case err == nil:
		u, gerr := h.users.Get(r.Context(), ident.UserID)
		if gerr == nil {
			h.mintLoginSession(w, r, u)
			return
		}
		if !errors.Is(gerr, userclient.ErrUserNotFound) {
			problem(w, r, http.StatusBadGateway, "user_service_error", "user lookup failed")
			return
		}
		// The bound user is gone (an interrupted account deletion):
		// re-anchor by email and move the identity to the survivor.
		u, uerr := h.upsertByEmail(r.Context(), claims, localeHint)
		if uerr != nil {
			problem(w, r, http.StatusBadGateway, "user_service_error", "profile upsert failed")
			return
		}
		if err := h.store.RebindIdentity(r.Context(), provider, claims.Subject, claims.Email, u.ID); err != nil {
			problem(w, r, http.StatusInternalServerError, "internal", "identity binding failed")
			return
		}
		h.mintLoginSession(w, r, u)
		return
	case !errors.Is(err, store.ErrIdentityNotFound):
		problem(w, r, http.StatusInternalServerError, "internal", "identity lookup failed")
		return
	}

	// First-time identity: find-or-create the account by verified email.
	u, err := h.upsertByEmail(r.Context(), claims, localeHint)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "user_service_error", "profile upsert failed")
		return
	}
	err = h.store.BindIdentity(r.Context(), provider, claims.Subject, claims.Email, u.ID)
	if errors.Is(err, store.ErrIdentityTaken) {
		// Lost a race with a concurrent link of this same identity:
		// whoever owns it now is who this login is.
		ident, rerr := h.store.ResolveIdentity(r.Context(), provider, claims.Subject, claims.Email)
		if rerr != nil {
			problem(w, r, http.StatusInternalServerError, "internal", "identity binding failed")
			return
		}
		owner, gerr := h.users.Get(r.Context(), ident.UserID)
		if gerr != nil {
			problem(w, r, http.StatusBadGateway, "user_service_error", "user lookup failed")
			return
		}
		h.mintLoginSession(w, r, owner)
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "identity binding failed")
		return
	}
	h.mintLoginSession(w, r, u)
}

// upsertByEmail funnels the two email-fallback call sites through one
// claim mapping. localeHint is the best Accept-Language tag from the
// login request, forwarded so the user service can seed a default for
// a brand-new account only.
func (h *Handlers) upsertByEmail(ctx context.Context, claims oidc.IDClaims, localeHint string) (userclient.User, error) {
	var avatar *string
	if claims.AvatarURL != "" {
		avatar = &claims.AvatarURL
	}
	return h.users.Upsert(ctx, claims.Email, claims.DisplayName, avatar, localeHint)
}

// mintLoginSession writes the token response for a resolved login.
func (h *Handlers) mintLoginSession(w http.ResponseWriter, r *http.Request, u userclient.User) {
	pair, ok := h.newSession(w, r, u)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, pair)
}

// newSession mints an access token and starts a refresh family. The
// access token is minted BEFORE the session row is written: a storage
// failure then just drops an unreferenced token instead of leaving a
// session without one.
func (h *Handlers) newSession(w http.ResponseWriter, r *http.Request, u userclient.User) (api.TokenPair, bool) {
	jti := uuid.NewString()
	rawRefresh, hash := token.NewRefreshToken()
	access, err := h.minter.Mint(u.ID.String(), u.Roles, jti)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "token mint failed")
		return api.TokenPair{}, false
	}
	expiresAt := time.Now().Add(h.refreshTTL)
	if err := h.store.CreateSession(r.Context(), hash, u.ID, jti, expiresAt); err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "session creation failed")
		return api.TokenPair{}, false
	}
	return tokenPairResponse(access, rawRefresh, h.minter.TTL(), expiresAt), true
}

func tokenPairResponse(access, refresh string, accessTTL time.Duration, refreshExpiresAt time.Time) api.TokenPair {
	return api.TokenPair{
		AccessToken:      access,
		TokenType:        "Bearer",
		ExpiresIn:        int64(accessTTL.Seconds()),
		RefreshToken:     refresh,
		RefreshExpiresIn: int64(time.Until(refreshExpiresAt).Seconds()),
	}
}

// decodeBody reads a small JSON body, writing the 400 problem itself
// on failure. All auth endpoints take tiny bodies; 64KB caps a buggy
// caller.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return false
	}
	return true
}

// RefreshToken rotates a refresh token. Ordering is deliberate: the
// session is peeked and roles fetched BEFORE rotation, so an upstream
// failure returns 503 with the token unconsumed (the client retries
// with the same token instead of tripping reuse detection).
func (h *Handlers) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req api.RefreshRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.RefreshToken == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "refresh_token is required")
		return
	}
	hash := token.HashRefreshToken(req.RefreshToken)

	sess, err := h.store.PeekSession(r.Context(), hash)
	if errors.Is(err, store.ErrRefreshNotFound) {
		problem(w, r, http.StatusUnauthorized, "invalid_refresh", "unknown refresh token")
		return
	}
	if errors.Is(err, store.ErrRefreshRevoked) {
		// The family was already revoked, by an earlier reuse detection, a
		// logout, or a vanished account. Any live jtis were reported at that
		// first reuse detection, or were never a denylist signal by design
		// (logout and account-gone go through direct family revocation);
		// re-presenting the dead token just re-signals reuse so the BFF
		// clears the session.
		writeReuseProblem(w, r, []string{})
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "session lookup failed")
		return
	}

	u, err := h.users.Get(r.Context(), sess.UserID)
	if errors.Is(err, userclient.ErrUserNotFound) {
		// The account is gone; the session dies with it.
		_ = h.store.RevokeFamilyByToken(r.Context(), hash)
		problem(w, r, http.StatusUnauthorized, "invalid_refresh", "user no longer exists")
		return
	}
	if err != nil {
		problem(w, r, http.StatusServiceUnavailable, "user_unavailable", "role source unavailable; retry")
		return
	}

	newJTI := uuid.NewString()
	newRaw, newHash := token.NewRefreshToken()
	access, err := h.minter.Mint(sess.UserID.String(), u.Roles, newJTI)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "token mint failed")
		return
	}

	res, err := h.store.Rotate(r.Context(), hash, newHash, newJTI, h.minter.TTL()+time.Minute)
	var reuse *store.ReuseError
	switch {
	case errors.As(err, &reuse):
		writeReuseProblem(w, r, reuse.RevokedJTIs)
		return
	case errors.Is(err, store.ErrRefreshNotFound), errors.Is(err, store.ErrRefreshExpired):
		problem(w, r, http.StatusUnauthorized, "invalid_refresh", "refresh token expired or unknown")
		return
	case err != nil:
		problem(w, r, http.StatusInternalServerError, "internal", "rotation failed")
		return
	}
	writeJSON(w, http.StatusOK, tokenPairResponse(access, newRaw, h.minter.TTL(), res.ExpiresAt))
}

// RevokeToken is logout: the presented token's whole family dies.
// Idempotent by design; revoking an unknown token is still a 204.
func (h *Handlers) RevokeToken(w http.ResponseWriter, r *http.Request) {
	var req api.RevokeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := h.store.RevokeFamilyByToken(r.Context(), token.HashRefreshToken(req.RefreshToken)); err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "revocation failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetJwks serves every non-retired verification key. Old keys keep
// being served after a rotation until an operator retires them, so
// in-flight tokens stay verifiable.
func (h *Handlers) GetJwks(w http.ResponseWriter, r *http.Request) {
	keys, err := h.store.ActiveSigningKeys(r.Context())
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "key lookup failed")
		return
	}
	doc := api.Jwks{Keys: make([]api.Jwk, len(keys))}
	for i, k := range keys {
		doc.Keys[i] = api.Jwk{Kty: "OKP", Crv: "Ed25519", Kid: k.Kid, X: k.PublicKeyB64}
	}
	writeJSON(w, http.StatusOK, doc)
}

// DevToken mints a session for a dev fixture. When the dev provider is
// disabled the response is a plain 404, indistinguishable from an
// unmounted route, so production deployments do not advertise it.
func (h *Handlers) DevToken(w http.ResponseWriter, r *http.Request) {
	if !h.devEnabled {
		problem(w, r, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req api.DevTokenRequest
	if !decodeBody(w, r, &req) {
		return
	}
	claims, ok := oidc.DevClaims(req.User)
	if !ok {
		problem(w, r, http.StatusBadRequest, "unknown_fixture", "no such fixture user")
		return
	}
	h.completeLogin(w, r, "dev", claims)
}

// ListProviders reports which login options exist right now, so a login
// page renders exactly the buttons that can succeed. Order is stable:
// real providers first, dev last.
func (h *Handlers) ListProviders(w http.ResponseWriter, r *http.Request) {
	names := []string{}
	for _, name := range []string{"google", "twitch"} {
		if _, ok := h.providers[name]; ok {
			names = append(names, name)
		}
	}
	if h.devEnabled {
		names = append(names, "dev")
	}
	writeJSON(w, http.StatusOK, api.Providers{Providers: names})
}

// writeReuseProblem emits the 401 problem+json carrying the revoked
// chain's possibly-live jtis (the refresh response is the only channel
// back to the BFF's denylist; no new call direction).
func writeReuseProblem(w http.ResponseWriter, r *http.Request, jtis []string) {
	if jtis == nil {
		jtis = []string{}
	}
	body := struct {
		httpkit.Problem
		RevokeJTIs []string `json:"revoke_jtis"`
	}{
		Problem: httpkit.Problem{
			Type: "about:blank", Title: http.StatusText(http.StatusUnauthorized),
			Status: http.StatusUnauthorized, Instance: r.URL.Path,
			Code: "refresh_reused", Detail: "refresh token reuse detected; the session chain is revoked",
		},
		RevokeJTIs: jtis,
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(body)
}
