// Package server maps HTTP (generated ServerInterface) onto the oidc
// adapters, token minter, store, and user-service client. It owns the
// login and token lifecycle orchestration.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

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

type Handlers struct {
	store      *store.Store
	minter     *token.Minter
	users      *userclient.Client
	providers  map[string]oidc.Provider
	devEnabled bool
	refreshTTL time.Duration
}

func New(st *store.Store, m *token.Minter, users *userclient.Client,
	providers map[string]oidc.Provider, devEnabled bool, refreshTTL time.Duration) *Handlers {
	return &Handlers{
		store: st, minter: m, users: users,
		providers: providers, devEnabled: devEnabled, refreshTTL: refreshTTL,
	}
}

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
	h.completeLogin(w, r, st.Provider, claims)
}

// completeLogin is the shared tail of every login path (real providers
// and the dev provider): verified-email policy, profile upsert,
// identity binding, then a fresh session.
func (h *Handlers) completeLogin(w http.ResponseWriter, r *http.Request, provider string, claims oidc.IDClaims) {
	// The user service keys accounts by email; accepting an unverified
	// email would let an attacker claim someone else's future account.
	if claims.Email == "" || !claims.EmailVerified {
		problem(w, r, http.StatusForbidden, "email_unverified", "provider did not assert a verified email")
		return
	}
	var avatar *string
	if claims.AvatarURL != "" {
		avatar = &claims.AvatarURL
	}
	u, err := h.users.Upsert(r.Context(), claims.Email, claims.DisplayName, avatar)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "user_service_error", "profile upsert failed")
		return
	}
	if err := h.store.BindIdentity(r.Context(), provider, claims.Subject, u.ID); err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "identity binding failed")
		return
	}
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblem(w, r, httpkit.Problem{
		Status: status, Title: http.StatusText(status), Code: code, Detail: detail,
	})
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
