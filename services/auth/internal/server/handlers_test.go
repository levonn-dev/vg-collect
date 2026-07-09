package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/auth/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/auth/internal/oidc"
	"github.com/levonn-dev/vg-collect/services/auth/internal/server"
	"github.com/levonn-dev/vg-collect/services/auth/internal/store"
	"github.com/levonn-dev/vg-collect/services/auth/internal/token"
	"github.com/levonn-dev/vg-collect/services/auth/internal/userclient"
	"github.com/levonn-dev/vg-collect/services/auth/migrations"
)

var testSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("auth"), tcpostgres.WithUsername("a"), tcpostgres.WithPassword("p"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// --- stub user service (validates service tokens through jwtauth) ---

type userRec struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Roles       []string
}

type stubUsersServer struct {
	t   *testing.T
	srv *httptest.Server
	v   *jwtauth.Validator

	mu      sync.Mutex
	byEmail map[string]*userRec
	byID    map[uuid.UUID]*userRec
	fail    bool
}

// newJWKSValidator builds a jwtauth.Validator against a JWKS httptest
// server mirroring m's public key -- the same verification path every
// real service uses to check a vg-collect access token, including the
// auth service validating its own tokens on the self-service endpoints.
func newJWKSValidator(t *testing.T, m *token.Minter) *jwtauth.Validator {
	t.Helper()
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": m.Kid(),
		"x": base64.RawURLEncoding.EncodeToString(m.PublicKey()),
	}}})
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwks)
	}))
	t.Cleanup(jwksSrv.Close)
	return jwtauth.NewValidator(jwksSrv.URL, "vg-collect-auth", "vg-collect")
}

func newStubUsersServer(t *testing.T, m *token.Minter) *stubUsersServer {
	t.Helper()
	f := &stubUsersServer{
		t: t, v: newJWKSValidator(t, m),
		byEmail: map[string]*userRec{}, byID: map[uuid.UUID]*userRec{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/users/upsert", f.upsert)
	mux.HandleFunc("GET /users/{id}", f.get)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubUsersServer) authorize(w http.ResponseWriter, r *http.Request) bool {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		f.t.Error("user-service call without bearer token")
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	claims, err := f.v.Validate(r.Context(), raw)
	if err != nil || !claims.HasRole("service") {
		f.t.Errorf("service token rejected: %v (claims %+v)", err, claims)
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

func (f *stubUsersServer) upsert(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var req struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	u, ok := f.byEmail[req.Email]
	if !ok {
		u = &userRec{ID: uuid.New(), Email: req.Email, Roles: []string{"user"}}
		f.byEmail[req.Email] = u
		f.byID[u.ID] = u
	}
	u.DisplayName = req.DisplayName
	writeUser(w, http.StatusOK, u)
}

func (f *stubUsersServer) get(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	u, ok := f.byID[id]
	if !ok {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not Found","status":404}`))
		return
	}
	writeUser(w, http.StatusOK, u)
}

func writeUser(w http.ResponseWriter, status int, u *userRec) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": u.ID.String(), "email": u.Email, "display_name": u.DisplayName,
		"roles": u.Roles, "created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})
}

func (f *stubUsersServer) setRoles(email string, roles ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byEmail[email].Roles = roles
}

func (f *stubUsersServer) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}

func (f *stubUsersServer) remove(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byEmail[email]; ok {
		delete(f.byID, u.ID)
		delete(f.byEmail, email)
	}
}

func (f *stubUsersServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byEmail)
}

// --- slim stub IdP (discovery + jwks + token endpoint) ---

type stubIDP struct {
	t     *testing.T
	srv   *httptest.Server
	key   *rsa.PrivateKey
	codes map[string]jwt.MapClaims
}

func newStubIDP(t *testing.T) *stubIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &stubIDP{t: t, key: key, codes: map[string]jwt.MapClaims{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint":         f.srv.URL + "/token",
			"jwks_uri":               f.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		pub := &f.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		claims, ok := f.codes[r.PostForm.Get("code")]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString(f.key)
		if err != nil {
			f.t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": s})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubIDP) registerCode(code, nonce string, extra jwt.MapClaims) {
	claims := jwt.MapClaims{
		"iss": f.srv.URL, "aud": "client-1", "nonce": nonce,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	f.codes[code] = claims
}

// --- assembled environment ---

type env struct {
	srv    *httptest.Server
	pool   *pgxpool.Pool
	minter *token.Minter
	users  *stubUsersServer
	idp    *stubIDP
}

func newEnv(t *testing.T, devEnabled bool) *env {
	t.Helper()
	pool := newTestPool(t)
	m, err := token.NewMinter(testSeed, "vg-collect-auth", "vg-collect", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	// The service registers its signing key at boot; the harness mirrors it.
	if err := st.RegisterSigningKey(context.Background(), m.Kid(), m.PublicKey()); err != nil {
		t.Fatal(err)
	}
	fu := newStubUsersServer(t, m)
	uc, err := userclient.New(fu.srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	idp := newStubIDP(t)
	providers := map[string]oidc.Provider{
		"google": oidc.NewGoogle("client-1", "secret-1", "https://app.example/cb", idp.srv.URL),
	}
	// The router under test validates Bearer tokens on its own
	// self-service endpoints exactly as production does: against its
	// own JWKS, mirrored here from the same minter that signs the
	// sessions these tests log in with.
	verifier := newJWKSValidator(t, m)
	h := server.New(st, m, uc, providers, verifier, devEnabled, 30*24*time.Hour)
	router := server.NewRouter(h, slog.Default(), func(context.Context) error { return nil })
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &env{srv: srv, pool: pool, minter: m, users: fu, idp: idp}
}

func post(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", &buf) //nolint:gosec // test helper; url is always from httptest.NewServer
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// postAuth posts body carrying an Authorization header (raw is the
// bearer token; an empty raw omits the header entirely, for the
// missing-Authorization cases).
func postAuth(t *testing.T, url, raw string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if raw != "" {
		req.Header.Set("Authorization", "Bearer "+raw)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// authReq is postAuth for the body-less identity-management endpoints
// (GET/DELETE): raw is the bearer token, empty omits the header
// entirely, for the missing-Authorization cases.
func authReq(t *testing.T, method, url, raw string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		req.Header.Set("Authorization", "Bearer "+raw)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

type tokenPair struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
}

// linkedTokenPair decodes a CallbackResponse: a tokenPair plus the
// linked_provider a link-mode completion carries.
type linkedTokenPair struct {
	tokenPair
	LinkedProvider string `json:"linked_provider"`
}

type problemBody struct {
	Status     int      `json:"status"`
	Code       string   `json:"code"`
	RevokeJTIs []string `json:"revoke_jtis"`
}

// identityDTO decodes one entry of an Identities response.
type identityDTO struct {
	ID        string  `json:"id"`
	Provider  string  `json:"provider"`
	Email     *string `json:"email,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type identitiesDTO struct {
	Identities []identityDTO `json:"identities"`
}

func wantProblem(t *testing.T, resp *http.Response, status int, code string) problemBody {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d", resp.StatusCode, status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want problem+json", ct)
	}
	p := decode[problemBody](t, resp)
	if p.Code != code {
		t.Fatalf("problem code = %q, want %q", p.Code, code)
	}
	return p
}

// accessClaims parses an access token against the minter's own public
// key (full jwtauth round-trips are asserted in the lifecycle tests).
func accessClaims(t *testing.T, e *env, raw string) jwt.MapClaims {
	t.Helper()
	mc := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(raw, mc, func(*jwt.Token) (any, error) {
		return e.minter.PublicKey(), nil
	}, jwt.WithValidMethods([]string{"EdDSA"})); err != nil {
		t.Fatalf("access token invalid: %v", err)
	}
	return mc
}

// --- start/callback ---

func TestOauthFlow_EndToEnd(t *testing.T) {
	e := newEnv(t, false)

	resp := post(t, e.srv.URL+"/oauth/start", map[string]string{"provider": "google"})
	if resp.StatusCode != 200 {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	start := decode[struct {
		AuthorizeURL string `json:"authorize_url"`
	}](t, resp)
	u, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") == "" || q.Get("nonce") == "" ||
		q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize url incomplete: %s", start.AuthorizeURL)
	}

	// The "browser" returns from the provider with a code bound to the
	// nonce the service generated.
	e.idp.registerCode("code-1", q.Get("nonce"), jwt.MapClaims{
		"sub": "google-sub-1", "email": "alice@example.com", "email_verified": true,
		"name": "Alice Google",
	})
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "code-1", "state": q.Get("state")})
	if resp.StatusCode != 200 {
		t.Fatalf("callback: %d", resp.StatusCode)
	}
	pair := decode[tokenPair](t, resp)
	if pair.TokenType != "Bearer" || pair.RefreshToken == "" ||
		pair.ExpiresIn != 300 || pair.RefreshExpiresIn <= 0 {
		t.Fatalf("pair = %+v", pair)
	}
	mc := accessClaims(t, e, pair.AccessToken)
	if mc["sub"] == "" || mc["jti"] == "" {
		t.Fatalf("claims = %v", mc)
	}
	roles, _ := mc["roles"].([]any)
	if len(roles) != 1 || roles[0] != "user" {
		t.Fatalf("roles = %v", mc["roles"])
	}

	// The profile reached the user service and the identity was bound.
	if e.users.count() != 1 {
		t.Fatalf("users upserted = %d, want 1", e.users.count())
	}
	var bound int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM identities WHERE provider = 'google' AND provider_subject = 'google-sub-1'`).
		Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != 1 {
		t.Fatal("identity not bound")
	}

	// The state is single-use: replaying the callback fails.
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "code-1", "state": q.Get("state")})
	wantProblem(t, resp, 400, "invalid_state")
}

// Two dev logins with the same subject stay one user even after the
// user's email-owning row changes hands: bind once as alice, then a
// second login resolves by identity without consulting upsert-by-email.
func TestOauthFlow_IdentityFirstResolution(t *testing.T) {
	e := newEnv(t, true)

	first := devLogin(t, e, "alice")
	firstClaims := accessClaims(t, e, first.AccessToken)
	aliceID, _ := firstClaims["sub"].(string)
	if aliceID == "" {
		t.Fatalf("first login sub missing: %v", firstClaims)
	}

	// Drop alice's email-indexed row but keep the id-indexed one: an
	// email upsert would now mint a brand new user, but the identity
	// bound on the first login must still resolve straight to alice.
	e.users.mu.Lock()
	delete(e.users.byEmail, "alice@example.com")
	e.users.mu.Unlock()

	second := devLogin(t, e, "alice")
	secondClaims := accessClaims(t, e, second.AccessToken)
	if secondClaims["sub"] != aliceID {
		t.Fatalf("second login sub = %v, want the original user %q", secondClaims["sub"], aliceID)
	}
	// The email path never ran a second time: no new byEmail row exists.
	if e.users.count() != 0 {
		t.Fatalf("users indexed by email = %d, want 0 (email upsert must not run)", e.users.count())
	}
	var boundUser uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT user_id FROM identities WHERE provider = 'dev' AND provider_subject = 'dev-alice'`).
		Scan(&boundUser); err != nil {
		t.Fatal(err)
	}
	if boundUser.String() != aliceID {
		t.Fatalf("identity bound to %s, want the original user %s", boundUser, aliceID)
	}
}

func TestOauthStart_UnknownProvider(t *testing.T) {
	e := newEnv(t, false)
	resp := post(t, e.srv.URL+"/oauth/start", map[string]string{"provider": "twitch"})
	wantProblem(t, resp, 400, "unknown_provider")
}

func TestOauthCallback_UnknownState(t *testing.T) {
	e := newEnv(t, false)
	resp := post(t, e.srv.URL+"/oauth/callback", map[string]string{"code": "c", "state": "bogus"})
	wantProblem(t, resp, 400, "invalid_state")
}

func TestOauthCallback_ProviderExchangeFails(t *testing.T) {
	e := newEnv(t, false)
	resp := post(t, e.srv.URL+"/oauth/start", map[string]string{"provider": "google"})
	start := decode[struct {
		AuthorizeURL string `json:"authorize_url"`
	}](t, resp)
	u, _ := url.Parse(start.AuthorizeURL)
	// No code registered at the stub IdP: the exchange 400s upstream.
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "never-issued", "state": u.Query().Get("state")})
	wantProblem(t, resp, 502, "provider_error")
}

func TestOauthCallback_UnverifiedEmailRejected(t *testing.T) {
	e := newEnv(t, false)
	resp := post(t, e.srv.URL+"/oauth/start", map[string]string{"provider": "google"})
	start := decode[struct {
		AuthorizeURL string `json:"authorize_url"`
	}](t, resp)
	u, _ := url.Parse(start.AuthorizeURL)
	q := u.Query()
	e.idp.registerCode("code-u", q.Get("nonce"), jwt.MapClaims{
		"sub": "s", "email": "unverified@example.com", "email_verified": false,
	})
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "code-u", "state": q.Get("state")})
	wantProblem(t, resp, 403, "email_unverified")
	if e.users.count() != 0 {
		t.Fatal("unverified login must not reach the user service")
	}
}

func TestOauthCallback_UserServiceDown(t *testing.T) {
	e := newEnv(t, false)
	resp := post(t, e.srv.URL+"/oauth/start", map[string]string{"provider": "google"})
	start := decode[struct {
		AuthorizeURL string `json:"authorize_url"`
	}](t, resp)
	u, _ := url.Parse(start.AuthorizeURL)
	q := u.Query()
	e.idp.registerCode("code-d", q.Get("nonce"), jwt.MapClaims{
		"sub": "s", "email": "down@example.com", "email_verified": true,
	})
	e.users.setFail(true)
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "code-d", "state": q.Get("state")})
	wantProblem(t, resp, 502, "user_service_error")
}

func TestOauthStart_MalformedBody(t *testing.T) {
	e := newEnv(t, false)
	resp, err := http.Post(e.srv.URL+"/oauth/start", "application/json",
		strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	wantProblem(t, resp, 400, "invalid_body")
}

func TestOauthCallback_VerificationFailureIs400(t *testing.T) {
	e := newEnv(t, false)
	resp := post(t, e.srv.URL+"/oauth/start", map[string]string{"provider": "google"})
	start := decode[struct {
		AuthorizeURL string `json:"authorize_url"`
	}](t, resp)
	u, _ := url.Parse(start.AuthorizeURL)
	q := u.Query()
	// The provider answers 200 with a well-signed token bound to the
	// WRONG nonce: an invalid login attempt, not a provider outage.
	e.idp.registerCode("code-n", "not-the-nonce", jwt.MapClaims{
		"sub": "s", "email": "n@example.com", "email_verified": true,
	})
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "code-n", "state": q.Get("state")})
	wantProblem(t, resp, 400, "invalid_callback")
	if e.users.count() != 0 {
		t.Fatal("failed verification must not reach the user service")
	}
}

// --- account link (link/start plus the shared callback, real provider) ---

func TestOauthLink_StartAndCallbackBindsToCaller(t *testing.T) {
	e := newEnv(t, true)
	alice := devLogin(t, e, "alice")
	aliceSub, _ := accessClaims(t, e, alice.AccessToken)["sub"].(string)

	resp := postAuth(t, e.srv.URL+"/oauth/link/start", alice.AccessToken,
		map[string]string{"provider": "google"})
	if resp.StatusCode != 200 {
		t.Fatalf("link start: %d", resp.StatusCode)
	}
	start := decode[struct {
		AuthorizeURL string `json:"authorize_url"`
	}](t, resp)
	u, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()

	// The fake provider asserts a verified email DIFFERENT from alice's;
	// identity-first binding must still land on alice's account.
	e.idp.registerCode("link-code-1", q.Get("nonce"), jwt.MapClaims{
		"sub": "g-sub-1", "email": "other@example.com", "email_verified": true,
	})
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "link-code-1", "state": q.Get("state")})
	if resp.StatusCode != 200 {
		t.Fatalf("callback: %d", resp.StatusCode)
	}
	linked := decode[linkedTokenPair](t, resp)
	if linked.LinkedProvider != "google" {
		t.Fatalf("linked_provider = %q, want google", linked.LinkedProvider)
	}
	linkedSub, _ := accessClaims(t, e, linked.AccessToken)["sub"].(string)
	if linkedSub != aliceSub {
		t.Fatalf("linked session sub = %s, want alice's own user %s (even though the email differs)",
			linkedSub, aliceSub)
	}

	// A fresh login through google with the SAME subject resolves straight
	// to alice's user: identity-first, proven through a real provider path.
	resp = post(t, e.srv.URL+"/oauth/start", map[string]string{"provider": "google"})
	start2 := decode[struct {
		AuthorizeURL string `json:"authorize_url"`
	}](t, resp)
	u2, err := url.Parse(start2.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q2 := u2.Query()
	e.idp.registerCode("login-code-1", q2.Get("nonce"), jwt.MapClaims{
		"sub": "g-sub-1", "email": "other@example.com", "email_verified": true,
	})
	resp = post(t, e.srv.URL+"/oauth/callback",
		map[string]string{"code": "login-code-1", "state": q2.Get("state")})
	if resp.StatusCode != 200 {
		t.Fatalf("relogin callback: %d", resp.StatusCode)
	}
	relogin := decode[tokenPair](t, resp)
	reloginSub, _ := accessClaims(t, e, relogin.AccessToken)["sub"].(string)
	if reloginSub != aliceSub {
		t.Fatalf("relogin sub = %s, want alice's user %s", reloginSub, aliceSub)
	}
}

func TestOauthLinkStart_UnknownProviderAndMissingBearer(t *testing.T) {
	e := newEnv(t, true)
	alice := devLogin(t, e, "alice")

	// The schema enum is not runtime-enforced (this router does its own
	// manual body decoding); an unrecognised provider name reaches the
	// handler and answers 400 the same way OauthStart's does.
	resp := postAuth(t, e.srv.URL+"/oauth/link/start", alice.AccessToken,
		map[string]string{"provider": "nope"})
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	resp = postAuth(t, e.srv.URL+"/oauth/link/start", "", map[string]string{"provider": "google"})
	wantProblem(t, resp, 401, "missing_token")
}

// --- token lifecycle ---

func devLogin(t *testing.T, e *env, user string) tokenPair {
	t.Helper()
	resp := post(t, e.srv.URL+"/oauth/dev/token", map[string]string{"user": user})
	if resp.StatusCode != 200 {
		t.Fatalf("dev login: %d", resp.StatusCode)
	}
	return decode[tokenPair](t, resp)
}

func refresh(t *testing.T, e *env, refreshToken string) *http.Response {
	t.Helper()
	return post(t, e.srv.URL+"/token/refresh", map[string]string{"refresh_token": refreshToken})
}

func TestDevToken_DisabledIs404(t *testing.T) {
	e := newEnv(t, false)
	resp := post(t, e.srv.URL+"/oauth/dev/token", map[string]string{"user": "alice"})
	wantProblem(t, resp, 404, "not_found")
}

func TestDevToken_UnknownFixture(t *testing.T) {
	e := newEnv(t, true)
	resp := post(t, e.srv.URL+"/oauth/dev/token", map[string]string{"user": "someone-real"})
	wantProblem(t, resp, 400, "unknown_fixture")
}

func TestDevToken_MintsSessionValidatedByJwtauth(t *testing.T) {
	e := newEnv(t, true)
	pair := devLogin(t, e, "alice")

	// The keystone cross-check: a token minted here must validate via
	// the shared jwtauth library against this service's own JWKS
	// endpoint, exactly as every other service will validate it.
	v := jwtauth.NewValidator(e.srv.URL+"/.well-known/jwks.json", "vg-collect-auth", "vg-collect")
	claims, err := v.Validate(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatalf("jwtauth rejected our token: %v", err)
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		t.Fatalf("sub = %q, want the upserted user's uuid", claims.Subject)
	}
	if !claims.HasRole("user") || claims.JTI == "" {
		t.Fatalf("claims = %+v", claims)
	}
}

// --- dev-link (account link via the dev provider; start and callback
// collapse into one hop, so these test the whole round trip directly) ---

func TestDevLink_LinksFixtureToCaller(t *testing.T) {
	e := newEnv(t, true)
	alice := devLogin(t, e, "alice")
	aliceSub, _ := accessClaims(t, e, alice.AccessToken)["sub"].(string)

	resp := postAuth(t, e.srv.URL+"/oauth/dev/link", alice.AccessToken, map[string]string{"user": "bob"})
	if resp.StatusCode != 200 {
		t.Fatalf("link: %d", resp.StatusCode)
	}
	linked := decode[linkedTokenPair](t, resp)
	if linked.LinkedProvider != "dev" {
		t.Fatalf("linked_provider = %q, want dev", linked.LinkedProvider)
	}
	linkedSub, _ := accessClaims(t, e, linked.AccessToken)["sub"].(string)
	if linkedSub != aliceSub {
		t.Fatalf("linked session sub = %s, want the caller's own user %s", linkedSub, aliceSub)
	}

	// bob now signs in as alice's user: the fixture identity moved.
	bobLogin := devLogin(t, e, "bob")
	bobSub, _ := accessClaims(t, e, bobLogin.AccessToken)["sub"].(string)
	if bobSub != aliceSub {
		t.Fatalf("bob login sub = %s, want alice's user %s", bobSub, aliceSub)
	}
}

func TestDevLink_ConflictWhenBoundElsewhere(t *testing.T) {
	e := newEnv(t, true)
	alice := devLogin(t, e, "alice") // binds dev-alice to U1
	bob := devLogin(t, e, "bob")     // binds dev-bob to U2 FIRST
	bobSub, _ := accessClaims(t, e, bob.AccessToken)["sub"].(string)

	resp := postAuth(t, e.srv.URL+"/oauth/dev/link", alice.AccessToken, map[string]string{"user": "bob"})
	wantProblem(t, resp, 409, "identity_already_linked")

	// bob still logs into U2: the failed link attempt moved nothing.
	bobAgain := devLogin(t, e, "bob")
	bobAgainSub, _ := accessClaims(t, e, bobAgain.AccessToken)["sub"].(string)
	if bobAgainSub != bobSub {
		t.Fatalf("bob's user changed after a failed link attempt: %s -> %s", bobSub, bobAgainSub)
	}
}

func TestDevLink_RequiresBearerAndEnabledProvider(t *testing.T) {
	e := newEnv(t, true)
	resp := postAuth(t, e.srv.URL+"/oauth/dev/link", "", map[string]string{"user": "bob"})
	wantProblem(t, resp, 401, "missing_token")

	// A malformed/garbage token is a distinct rejection from a missing
	// one: it reaches the verifier and fails there.
	resp = postAuth(t, e.srv.URL+"/oauth/dev/link", "not-a-real-jwt", map[string]string{"user": "bob"})
	wantProblem(t, resp, 401, "invalid_token")

	svcTok, err := e.minter.ServiceToken()
	if err != nil {
		t.Fatal(err)
	}
	resp = postAuth(t, e.srv.URL+"/oauth/dev/link", svcTok, map[string]string{"user": "bob"})
	wantProblem(t, resp, 403, "forbidden")

	disabled := newEnv(t, false)
	resp = postAuth(t, disabled.srv.URL+"/oauth/dev/link", "", map[string]string{"user": "bob"})
	wantProblem(t, resp, 404, "not_found")
}

func TestDevLink_SameUserIsIdempotent(t *testing.T) {
	e := newEnv(t, true)
	alice := devLogin(t, e, "alice")
	for i := 0; i < 2; i++ {
		resp := postAuth(t, e.srv.URL+"/oauth/dev/link", alice.AccessToken, map[string]string{"user": "bob"})
		if resp.StatusCode != 200 {
			t.Fatalf("link attempt %d: %d", i+1, resp.StatusCode)
		}
	}
}

func TestJwks_ServesRegisteredKey(t *testing.T) {
	e := newEnv(t, false)
	resp, err := http.Get(e.srv.URL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != 200 {
		t.Fatalf("jwks: %d", resp.StatusCode)
	}
	doc := decode[struct {
		Keys []struct {
			Kty, Crv, Kid, X string
		} `json:"keys"`
	}](t, resp)
	if len(doc.Keys) != 1 {
		t.Fatalf("keys = %+v", doc.Keys)
	}
	k := doc.Keys[0]
	if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid != e.minter.Kid() {
		t.Fatalf("key = %+v", k)
	}
	raw, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil || len(raw) != 32 {
		t.Fatalf("x not a raw Ed25519 key: %v (%d bytes)", err, len(raw))
	}
}

func TestRefresh_RotatesAndRereadsRoles(t *testing.T) {
	e := newEnv(t, true)
	pair := devLogin(t, e, "alice")

	// Role changes take effect at the next rotation, not session end.
	e.users.setRoles("alice@example.com", "user", "admin")

	resp := refresh(t, e, pair.RefreshToken)
	if resp.StatusCode != 200 {
		t.Fatalf("refresh: %d", resp.StatusCode)
	}
	next := decode[tokenPair](t, resp)
	if next.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if next.RefreshExpiresIn > pair.RefreshExpiresIn {
		t.Fatal("expiry must be absolute: the rotated token cannot outlive the family")
	}
	mc := accessClaims(t, e, next.AccessToken)
	roles, _ := mc["roles"].([]any)
	if len(roles) != 2 {
		t.Fatalf("roles = %v, want the re-read pair", mc["roles"])
	}
}

func TestRefresh_ReuseRevokesChainAndReportsJTIs(t *testing.T) {
	e := newEnv(t, true)
	pair := devLogin(t, e, "alice")

	resp := refresh(t, e, pair.RefreshToken)
	if resp.StatusCode != 200 {
		t.Fatalf("first refresh: %d", resp.StatusCode)
	}
	next := decode[tokenPair](t, resp)

	// Replaying the consumed token is reuse: 401 with the jtis the BFF
	// must denylist.
	p := wantProblem(t, refresh(t, e, pair.RefreshToken), 401, "refresh_reused")
	if len(p.RevokeJTIs) < 2 {
		t.Fatalf("revoke_jtis = %v, want both session jtis", p.RevokeJTIs)
	}

	// The whole family died, including the fresh tip.
	wantProblem(t, refresh(t, e, next.RefreshToken), 401, "refresh_reused")
}

func TestRefresh_UnknownToken(t *testing.T) {
	e := newEnv(t, true)
	wantProblem(t, refresh(t, e, "never-issued"), 401, "invalid_refresh")
}

func TestRefresh_UserGoneRevokesFamily(t *testing.T) {
	e := newEnv(t, true)
	pair := devLogin(t, e, "bob")
	e.users.remove("bob@example.com")

	wantProblem(t, refresh(t, e, pair.RefreshToken), 401, "invalid_refresh")
	// The family was revoked, so a retry hits the revoked path.
	wantProblem(t, refresh(t, e, pair.RefreshToken), 401, "refresh_reused")
}

func TestRefresh_UserServiceDownDoesNotConsumeToken(t *testing.T) {
	e := newEnv(t, true)
	pair := devLogin(t, e, "alice")

	e.users.setFail(true)
	wantProblem(t, refresh(t, e, pair.RefreshToken), 503, "user_unavailable")

	// Roles are read BEFORE rotation, so the failed attempt must not
	// have consumed the token: the retry succeeds.
	e.users.setFail(false)
	if resp := refresh(t, e, pair.RefreshToken); resp.StatusCode != 200 {
		t.Fatalf("retry after outage: %d, want 200", resp.StatusCode)
	}
}

func TestRevoke_LogoutKillsFamilyAndIsIdempotent(t *testing.T) {
	e := newEnv(t, true)
	pair := devLogin(t, e, "alice")

	resp := post(t, e.srv.URL+"/token/revoke", map[string]string{"refresh_token": pair.RefreshToken})
	if resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	wantProblem(t, refresh(t, e, pair.RefreshToken), 401, "refresh_reused")

	// Revoking again (or revoking garbage) stays 204: logout never fails.
	resp = post(t, e.srv.URL+"/token/revoke", map[string]string{"refresh_token": pair.RefreshToken})
	if resp.StatusCode != 204 {
		t.Fatalf("re-revoke: %d", resp.StatusCode)
	}
	resp = post(t, e.srv.URL+"/token/revoke", map[string]string{"refresh_token": "unknown"})
	if resp.StatusCode != 204 {
		t.Fatalf("revoke unknown: %d", resp.StatusCode)
	}
}

// --- identity management (list/unlink) and account auth wipe ---

func TestIdentities_ListUnlinkGuardsAndWipe(t *testing.T) {
	e := newEnv(t, true)
	alice := devLogin(t, e, "alice")
	aliceID, _ := accessClaims(t, e, alice.AccessToken)["sub"].(string)

	linkResp := postAuth(t, e.srv.URL+"/oauth/dev/link", alice.AccessToken, map[string]string{"user": "bob"})
	if linkResp.StatusCode != 200 {
		t.Fatalf("dev/link bob: %d", linkResp.StatusCode)
	}

	resp := authReq(t, http.MethodGet, e.srv.URL+"/users/"+aliceID+"/identities", alice.AccessToken)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	list := decode[identitiesDTO](t, resp)
	if len(list.Identities) != 2 {
		t.Fatalf("identities = %+v, want 2", list.Identities)
	}
	var bobRowID, aliceRowID string
	emails := map[string]bool{}
	for _, id := range list.Identities {
		if id.Provider != "dev" {
			t.Fatalf("provider = %q, want dev", id.Provider)
		}
		if _, err := uuid.Parse(id.ID); err != nil {
			t.Fatalf("id = %q, not a uuid", id.ID)
		}
		if id.Email == nil {
			t.Fatalf("email missing on identity %+v", id)
		}
		emails[*id.Email] = true
		switch *id.Email {
		case "bob@example.com":
			bobRowID = id.ID
		case "alice@example.com":
			aliceRowID = id.ID
		}
	}
	if !emails["alice@example.com"] || !emails["bob@example.com"] {
		t.Fatalf("emails = %v, want alice@example.com and bob@example.com", emails)
	}

	// Reading another account's identities is refused even for the
	// account's own owner token.
	resp = authReq(t, http.MethodGet, e.srv.URL+"/users/"+uuid.NewString()+"/identities", alice.AccessToken)
	wantProblem(t, resp, http.StatusForbidden, "forbidden")

	// A service token may read any user's identities.
	svcTok, err := e.minter.ServiceToken()
	if err != nil {
		t.Fatal(err)
	}
	resp = authReq(t, http.MethodGet, e.srv.URL+"/users/"+aliceID+"/identities", svcTok)
	if resp.StatusCode != 200 {
		t.Fatalf("service-token list: %d", resp.StatusCode)
	}

	// Unlinking bob's identity drops the list to one row.
	resp = authReq(t, http.MethodDelete, e.srv.URL+"/identities/"+bobRowID, alice.AccessToken)
	if resp.StatusCode != 204 {
		t.Fatalf("delete bob identity: %d", resp.StatusCode)
	}
	resp = authReq(t, http.MethodGet, e.srv.URL+"/users/"+aliceID+"/identities", alice.AccessToken)
	list = decode[identitiesDTO](t, resp)
	if len(list.Identities) != 1 {
		t.Fatalf("identities after unlink = %+v, want 1", list.Identities)
	}

	// The account's last login cannot be removed.
	resp = authReq(t, http.MethodDelete, e.srv.URL+"/identities/"+aliceRowID, alice.AccessToken)
	wantProblem(t, resp, http.StatusConflict, "last_identity")

	// An identity id that does not exist on the caller's account is not found.
	resp = authReq(t, http.MethodDelete, e.srv.URL+"/identities/"+uuid.NewString(), alice.AccessToken)
	wantProblem(t, resp, http.StatusNotFound, "identity_not_found")

	// Wiping the account's auth footprint empties the identity list and
	// kills the outstanding refresh family.
	resp = authReq(t, http.MethodDelete, e.srv.URL+"/users/"+aliceID+"/auth", alice.AccessToken)
	if resp.StatusCode != 204 {
		t.Fatalf("delete user auth: %d", resp.StatusCode)
	}
	resp = authReq(t, http.MethodGet, e.srv.URL+"/users/"+aliceID+"/identities", alice.AccessToken)
	list = decode[identitiesDTO](t, resp)
	if resp.StatusCode != 200 || len(list.Identities) != 0 {
		t.Fatalf("identities after wipe: status=%d list=%+v, want 200 empty", resp.StatusCode, list.Identities)
	}
	wantProblem(t, refresh(t, e, alice.RefreshToken), http.StatusUnauthorized, "refresh_reused")

	// The wipe is self-only too.
	resp = authReq(t, http.MethodDelete, e.srv.URL+"/users/"+uuid.NewString()+"/auth", alice.AccessToken)
	wantProblem(t, resp, http.StatusForbidden, "forbidden")
}

func TestIdentities_RequireBearer(t *testing.T) {
	e := newEnv(t, false)
	u := uuid.NewString()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/users/" + u + "/identities"},
		{http.MethodDelete, "/identities/" + uuid.NewString()},
		{http.MethodDelete, "/users/" + u + "/auth"},
	} {
		resp := authReq(t, tc.method, e.srv.URL+tc.path, "")
		wantProblem(t, resp, http.StatusUnauthorized, "missing_token")
	}
}

func TestHealthEndpoints(t *testing.T) {
	e := newEnv(t, false)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %d", path, err, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

func TestListProviders(t *testing.T) {
	cases := []struct {
		name       string
		providers  map[string]oidc.Provider
		devEnabled bool
		want       []string
	}{
		{"none enabled", map[string]oidc.Provider{}, false, []string{}},
		{"dev only", map[string]oidc.Provider{}, true, []string{"dev"}},
		{"google plus dev", map[string]oidc.Provider{
			"google": oidc.NewGoogle("id", "secret", "http://cb", "http://issuer"),
		}, true, []string{"google", "dev"}},
		{"both real, no dev", map[string]oidc.Provider{
			"google": oidc.NewGoogle("id", "secret", "http://cb", "http://issuer"),
			"twitch": oidc.NewTwitch("id", "secret", "http://cb", "http://issuer"),
		}, false, []string{"google", "twitch"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := server.New(nil, nil, nil, tc.providers, &stubVerifier{}, tc.devEnabled, 0)
			rec := httptest.NewRecorder()
			h.ListProviders(rec, httptest.NewRequest(http.MethodGet, "/providers", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var body api.Providers
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body.Providers, tc.want) {
				t.Fatalf("providers = %v, want %v", body.Providers, tc.want)
			}
		})
	}
}

// ============================================================================
// Fast unit layer (no Docker, runs under -short)
//
// The tests below drive the real handlers with the in-memory doubles
// (stubStore, stubMinter, stubUserService, stubProvider) defined just below.
// They cover the full branch matrix of every handler -- each asserts the
// branch's distinctive outcome (exact status + problem code, and for the
// security branches the side effects), not merely a 200. They take no
// Postgres and no network, so unlike the integration tests above they do
// NOT skip on -short; they are the auth-side parity for the bff's fast
// handler/middleware unit layer.
//
// ListProviders' fast-layer coverage already lives in TestListProviders
// above: it touches no store, so it runs Docker-free today.
// ============================================================================

// In-memory doubles for the fast, Docker-free unit layer. Each implements
// one of the server ports (server.Store, server.Minter,
// server.UserService) or oidc.Provider directly, with function fields a
// table-driven test sets to drive a specific branch. A method whose field
// is left nil panics: an unexpected collaborator call is a loud test
// failure, not a silent zero value, which keeps each branch's wiring
// honest. Counters record the side effects the security branches turn on
// (e.g. whether Rotate ran, how many times RevokeFamilyByToken was
// called) so a test can assert them, not just the status code.
//
// These are distinct from the httptest-backed server stubs above
// (stubUsersServer, stubIDP): those exercise the real
// userclient/oidc adapters over HTTP in the integration layer; these
// satisfy the interfaces directly with no network.

// stubStore implements server.Store.
type stubStore struct {
	createState         func(ctx context.Context, st store.AuthState) error
	consumeState        func(ctx context.Context, state string) (store.AuthState, error)
	resolveIdentity     func(ctx context.Context, provider, subject, email string) (store.Identity, error)
	bindIdentity        func(ctx context.Context, provider, subject, email string, userID uuid.UUID) error
	rebindIdentity      func(ctx context.Context, provider, subject, email string, userID uuid.UUID) error
	listIdentities      func(ctx context.Context, userID uuid.UUID) ([]store.Identity, error)
	deleteIdentity      func(ctx context.Context, userID, identityID uuid.UUID) error
	deleteUserAuth      func(ctx context.Context, userID uuid.UUID) error
	createSession       func(ctx context.Context, tokenHash string, userID uuid.UUID, accessJTI string, expiresAt time.Time) error
	peekSession         func(ctx context.Context, tokenHash string) (store.Session, error)
	rotate              func(ctx context.Context, presentedHash, newHash, newAccessJTI string, jtiWindow time.Duration) (store.RotateResult, error)
	revokeFamilyByToken func(ctx context.Context, tokenHash string) error
	activeSigningKeys   func(ctx context.Context) ([]store.SigningKey, error)

	// Observed side effects, for the genuine-guard assertions.
	rotateCalls int
	revokeCalls int
	revokedHash string
}

var _ server.Store = (*stubStore)(nil)

func (s *stubStore) CreateState(ctx context.Context, st store.AuthState) error {
	if s.createState == nil {
		panic("unexpected CreateState")
	}
	return s.createState(ctx, st)
}

func (s *stubStore) ConsumeState(ctx context.Context, state string) (store.AuthState, error) {
	if s.consumeState == nil {
		panic("unexpected ConsumeState")
	}
	return s.consumeState(ctx, state)
}

func (s *stubStore) ResolveIdentity(ctx context.Context, provider, subject, email string) (store.Identity, error) {
	if s.resolveIdentity == nil {
		panic("unexpected ResolveIdentity")
	}
	return s.resolveIdentity(ctx, provider, subject, email)
}

func (s *stubStore) BindIdentity(ctx context.Context, provider, subject, email string, userID uuid.UUID) error {
	if s.bindIdentity == nil {
		panic("unexpected BindIdentity")
	}
	return s.bindIdentity(ctx, provider, subject, email, userID)
}

func (s *stubStore) RebindIdentity(ctx context.Context, provider, subject, email string, userID uuid.UUID) error {
	if s.rebindIdentity == nil {
		panic("unexpected RebindIdentity")
	}
	return s.rebindIdentity(ctx, provider, subject, email, userID)
}

func (s *stubStore) ListIdentities(ctx context.Context, userID uuid.UUID) ([]store.Identity, error) {
	if s.listIdentities == nil {
		panic("unexpected ListIdentities")
	}
	return s.listIdentities(ctx, userID)
}

func (s *stubStore) DeleteIdentity(ctx context.Context, userID, identityID uuid.UUID) error {
	if s.deleteIdentity == nil {
		panic("unexpected DeleteIdentity")
	}
	return s.deleteIdentity(ctx, userID, identityID)
}

func (s *stubStore) DeleteUserAuth(ctx context.Context, userID uuid.UUID) error {
	if s.deleteUserAuth == nil {
		panic("unexpected DeleteUserAuth")
	}
	return s.deleteUserAuth(ctx, userID)
}

func (s *stubStore) CreateSession(ctx context.Context, tokenHash string, userID uuid.UUID, accessJTI string, expiresAt time.Time) error {
	if s.createSession == nil {
		panic("unexpected CreateSession")
	}
	return s.createSession(ctx, tokenHash, userID, accessJTI, expiresAt)
}

func (s *stubStore) PeekSession(ctx context.Context, tokenHash string) (store.Session, error) {
	if s.peekSession == nil {
		panic("unexpected PeekSession")
	}
	return s.peekSession(ctx, tokenHash)
}

func (s *stubStore) Rotate(ctx context.Context, presentedHash, newHash, newAccessJTI string, jtiWindow time.Duration) (store.RotateResult, error) {
	s.rotateCalls++
	if s.rotate == nil {
		panic("unexpected Rotate")
	}
	return s.rotate(ctx, presentedHash, newHash, newAccessJTI, jtiWindow)
}

func (s *stubStore) RevokeFamilyByToken(ctx context.Context, tokenHash string) error {
	s.revokeCalls++
	s.revokedHash = tokenHash
	if s.revokeFamilyByToken == nil {
		panic("unexpected RevokeFamilyByToken")
	}
	return s.revokeFamilyByToken(ctx, tokenHash)
}

func (s *stubStore) ActiveSigningKeys(ctx context.Context) ([]store.SigningKey, error) {
	if s.activeSigningKeys == nil {
		panic("unexpected ActiveSigningKeys")
	}
	return s.activeSigningKeys(ctx)
}

// stubMinter implements server.Minter. token is the access token Mint
// returns; mintErr, when set, makes Mint fail (the mint-error branch).
type stubMinter struct {
	token   string
	mintErr error
	ttl     time.Duration
}

var _ server.Minter = (*stubMinter)(nil)

func (m *stubMinter) Mint(_ string, _ []string, _ string) (string, error) {
	if m.mintErr != nil {
		return "", m.mintErr
	}
	return m.token, nil
}

func (m *stubMinter) TTL() time.Duration { return m.ttl }

// stubUserService implements server.UserService.
type stubUserService struct {
	upsert func(ctx context.Context, email, displayName string, avatarURL *string) (userclient.User, error)
	get    func(ctx context.Context, id uuid.UUID) (userclient.User, error)
}

var _ server.UserService = (*stubUserService)(nil)

func (u *stubUserService) Upsert(ctx context.Context, email, displayName string, avatarURL *string) (userclient.User, error) {
	if u.upsert == nil {
		panic("unexpected Upsert")
	}
	return u.upsert(ctx, email, displayName, avatarURL)
}

func (u *stubUserService) Get(ctx context.Context, id uuid.UUID) (userclient.User, error) {
	if u.get == nil {
		panic("unexpected Get")
	}
	return u.get(ctx, id)
}

// stubProvider implements oidc.Provider. authorizeURL and exchange drive
// the AuthorizeURL/Exchange branches (success, *oidc.ProviderError, or a
// plain verification error); name backs Name().
type stubProvider struct {
	name         string
	authorizeURL func(ctx context.Context, state, nonce, challenge string) (string, error)
	exchange     func(ctx context.Context, code, verifier, nonce string) (oidc.IDClaims, error)
}

var _ oidc.Provider = (*stubProvider)(nil)

func (p *stubProvider) Name() string {
	if p.name == "" {
		return "google"
	}
	return p.name
}

func (p *stubProvider) AuthorizeURL(ctx context.Context, state, nonce, challenge string) (string, error) {
	if p.authorizeURL == nil {
		panic("unexpected AuthorizeURL")
	}
	return p.authorizeURL(ctx, state, nonce, challenge)
}

func (p *stubProvider) Exchange(ctx context.Context, code, verifier, nonce string) (oidc.IDClaims, error) {
	if p.exchange == nil {
		panic("unexpected Exchange")
	}
	return p.exchange(ctx, code, verifier, nonce)
}

// errStub is a generic non-typed error used to drive the "some other
// error" (500) branches, distinct from the sentinel/typed errors the
// handlers special-case.
var errStub = errors.New("stub failure")

const unitRefreshTTL = 30 * 24 * time.Hour

// stubAccessJWT is the canned access token the stub minter returns; the
// success assertions check the handler hands exactly this value back. It
// is an opaque placeholder, never validated; named without a
// credential-like word so static analysis does not mistake it for a real
// secret.
const stubAccessJWT = "stub.access.jwt"

// newUnit builds Handlers wired to the given stubs for a single test.
func newUnit(st server.Store, m server.Minter, users server.UserService,
	providers map[string]oidc.Provider, verifier server.Verifier, devEnabled bool) *server.Handlers {
	return server.New(st, m, users, providers, verifier, devEnabled, unitRefreshTTL)
}

// stubVerifier implements server.Verifier. validate is nil by default,
// so a test that reaches Validate without wiring it panics -- every
// unit test above drives handlers that never call requireUser, so
// &stubVerifier{} is wired and never invoked.
type stubVerifier struct {
	validate func(ctx context.Context, raw string) (jwtauth.Claims, error)
}

var _ server.Verifier = (*stubVerifier)(nil)

func (v *stubVerifier) Validate(ctx context.Context, raw string) (jwtauth.Claims, error) {
	if v.validate == nil {
		panic("unexpected Validate")
	}
	return v.validate(ctx, raw)
}

// jsonReq builds a request whose body is the JSON encoding of v (or a raw
// string when v is a string, for the malformed-body cases).
func jsonReq(t *testing.T, method, target string, v any) *http.Request {
	t.Helper()
	if raw, ok := v.(string); ok {
		return httptest.NewRequest(method, target, strings.NewReader(raw))
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewRequest(method, target, bytes.NewReader(b))
}

// wantProblemRec is wantProblem for a recorder-driven handler call: it
// asserts the status, the problem+json content type, and the machine code.
func wantProblemRec(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) problemBody {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want problem+json", ct)
	}
	var p problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v (%s)", err, rec.Body.String())
	}
	if p.Code != code {
		t.Fatalf("problem code = %q, want %q", p.Code, code)
	}
	return p
}

// wantPairRec asserts a 200 carrying a well-formed TokenPair whose access
// token is the one the stub minter produced.
func wantPairRec(t *testing.T, rec *httptest.ResponseRecorder, wantAccess string) tokenPair {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var pair tokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &pair); err != nil {
		t.Fatalf("decode pair: %v (%s)", err, rec.Body.String())
	}
	if pair.AccessToken != wantAccess || pair.TokenType != "Bearer" ||
		pair.RefreshToken == "" || pair.ExpiresIn != 300 || pair.RefreshExpiresIn <= 0 {
		t.Fatalf("pair = %+v", pair)
	}
	return pair
}

// unitMinter returns a stub minter with a canned access token and the
// 5-minute access TTL the production minter uses (so ExpiresIn == 300).
func unitMinter() *stubMinter {
	return &stubMinter{token: stubAccessJWT, ttl: 5 * time.Minute}
}

// verifiedClaims is a valid completed-login assertion (verified email).
func verifiedClaims() oidc.IDClaims {
	return oidc.IDClaims{
		Subject: "google-sub-1", Email: "alice@example.com",
		EmailVerified: true, DisplayName: "Alice",
	}
}

// upsertedUser is the canonical user an Upsert resolves to.
func upsertedUser() userclient.User {
	return userclient.User{ID: uuid.New(), Roles: []string{"user"}}
}

// providerMap wraps a single stub provider under its name.
func providerMap(p *stubProvider) map[string]oidc.Provider {
	return map[string]oidc.Provider{p.Name(): p}
}

// --- OauthStart ---

func TestUnitOauthStart_InvalidBody(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start", "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitOauthStart_UnknownProvider(t *testing.T) {
	// Valid JSON, but the provider is not in the enabled map.
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{},
		map[string]oidc.Provider{}, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start",
		api.StartRequest{Provider: "twitch"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "unknown_provider")
}

func TestUnitOauthStart_CreateStateError(t *testing.T) {
	st := &stubStore{createState: func(context.Context, store.AuthState) error { return errStub }}
	p := &stubProvider{name: "google"}
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start",
		api.StartRequest{Provider: "google"}))
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitOauthStart_AuthorizeProviderError(t *testing.T) {
	st := &stubStore{createState: func(context.Context, store.AuthState) error { return nil }}
	p := &stubProvider{
		name: "google",
		authorizeURL: func(context.Context, string, string, string) (string, error) {
			return "", &oidc.ProviderError{Op: "discovery", Status: 503}
		},
	}
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start",
		api.StartRequest{Provider: "google"}))
	wantProblemRec(t, rec, http.StatusBadGateway, "provider_error")
}

func TestUnitOauthStart_Success(t *testing.T) {
	var savedState store.AuthState
	st := &stubStore{createState: func(_ context.Context, s store.AuthState) error {
		savedState = s
		return nil
	}}
	p := &stubProvider{
		name: "google",
		authorizeURL: func(_ context.Context, state, nonce, challenge string) (string, error) {
			// The handler must persist exactly what it sends to the provider.
			if state != savedState.State || nonce != savedState.Nonce {
				t.Errorf("authorize args state=%q nonce=%q vs saved %+v", state, nonce, savedState)
			}
			if challenge == "" {
				t.Error("missing PKCE challenge")
			}
			return "https://idp.example/authorize?state=" + state, nil
		},
	}
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start",
		api.StartRequest{Provider: "google"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var resp api.StartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.AuthorizeUrl != "https://idp.example/authorize?state="+savedState.State {
		t.Fatalf("authorize_url = %q", resp.AuthorizeUrl)
	}
	if savedState.Provider != "google" {
		t.Fatalf("persisted provider = %q", savedState.Provider)
	}
}

// --- OauthCallback ---

func TestUnitOauthCallback_InvalidBody(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback", "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitOauthCallback_MissingCodeOrState(t *testing.T) {
	cases := []api.CallbackRequest{
		{Code: "", State: "s"},
		{Code: "c", State: ""},
		{Code: "", State: ""},
	}
	for _, body := range cases {
		h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
		rec := httptest.NewRecorder()
		h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback", body))
		// Missing params never reach the store: ConsumeState would panic.
		wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
	}
}

func TestUnitOauthCallback_StateNotFound(t *testing.T) {
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{}, store.ErrStateNotFound
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "bogus"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_state")
}

func TestUnitOauthCallback_ConsumeStateError(t *testing.T) {
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{}, errStub
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitOauthCallback_ProviderNoLongerEnabled(t *testing.T) {
	// The state resolves to a provider that is no longer in the map.
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{Provider: "google"}, nil
	}}
	h := newUnit(st, unitMinter(), &stubUserService{},
		map[string]oidc.Provider{}, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_state")
}

func TestUnitOauthCallback_ExchangeProviderError(t *testing.T) {
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{Provider: "google"}, nil
	}}
	p := &stubProvider{
		name: "google",
		exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
			return oidc.IDClaims{}, &oidc.ProviderError{Op: "token exchange", Status: 500}
		},
	}
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	wantProblemRec(t, rec, http.StatusBadGateway, "provider_error")
}

func TestUnitOauthCallback_ExchangeVerificationError(t *testing.T) {
	// A non-ProviderError from Exchange is a failed verification: a bad
	// login attempt (400), not an upstream outage.
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{Provider: "google"}, nil
	}}
	p := &stubProvider{
		name: "google",
		exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
			return oidc.IDClaims{}, errors.New("nonce mismatch")
		},
	}
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_callback")
}

// --- completeLogin (the shared tail of OauthCallback and DevToken) ---

// callbackInto runs OauthCallback with a state that resolves to provider
// "google" whose Exchange yields claims, so the request lands in
// completeLogin. It returns the recorder for branch assertions.
func callbackInto(t *testing.T, st server.Store, m server.Minter, users server.UserService, claims oidc.IDClaims) *httptest.ResponseRecorder {
	t.Helper()
	if st == nil {
		st = &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
			return store.AuthState{Provider: "google"}, nil
		}}
	}
	p := &stubProvider{
		name:     "google",
		exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) { return claims, nil },
	}
	h := newUnit(st, m, users, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	return rec
}

// consumeGoogle is the stubStore wiring shared by the completeLogin tests
// reached through the callback: a one-shot state for provider "google",
// with resolveIdentity defaulted to "first-time identity" so these tests
// keep exercising the email fallback path unchanged.
func consumeGoogle() *stubStore {
	return &stubStore{
		consumeState: func(context.Context, string) (store.AuthState, error) {
			return store.AuthState{Provider: "google"}, nil
		},
		resolveIdentity: func(context.Context, string, string, string) (store.Identity, error) {
			return store.Identity{}, store.ErrIdentityNotFound
		},
	}
}

func TestUnitCompleteLogin_EmailUnverified(t *testing.T) {
	cases := []struct {
		name   string
		claims oidc.IDClaims
	}{
		{"missing email", oidc.IDClaims{Subject: "s", Email: "", EmailVerified: true}},
		{"unverified email", oidc.IDClaims{Subject: "s", Email: "a@example.com", EmailVerified: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Upsert/BindIdentity must never run for an unverified login:
			// the unwired stubs would panic if reached.
			rec := callbackInto(t, consumeGoogle(), unitMinter(), &stubUserService{}, tc.claims)
			wantProblemRec(t, rec, http.StatusForbidden, "email_unverified")
		})
	}
}

func TestUnitCompleteLogin_UpsertError(t *testing.T) {
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return userclient.User{}, errStub
		},
	}
	rec := callbackInto(t, consumeGoogle(), unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusBadGateway, "user_service_error")
}

func TestUnitCompleteLogin_BindIdentityError(t *testing.T) {
	st := consumeGoogle()
	st.bindIdentity = func(context.Context, string, string, string, uuid.UUID) error { return errStub }
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return upsertedUser(), nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitCompleteLogin_MintError(t *testing.T) {
	st := consumeGoogle()
	st.bindIdentity = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return upsertedUser(), nil
		},
	}
	m := &stubMinter{mintErr: errStub, ttl: 5 * time.Minute}
	rec := callbackInto(t, st, m, users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitCompleteLogin_CreateSessionError(t *testing.T) {
	st := consumeGoogle()
	st.bindIdentity = func(context.Context, string, string, string, uuid.UUID) error { return nil }
	st.createSession = func(context.Context, string, uuid.UUID, string, time.Time) error { return errStub }
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return upsertedUser(), nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitCompleteLogin_SuccessViaCallback(t *testing.T) {
	u := upsertedUser()
	st := consumeGoogle()
	var boundProvider, boundSubject string
	var boundUser uuid.UUID
	st.bindIdentity = func(_ context.Context, provider, subject, _ string, userID uuid.UUID) error {
		boundProvider, boundSubject, boundUser = provider, subject, userID
		return nil
	}
	var sessUser uuid.UUID
	var sessJTI string
	st.createSession = func(_ context.Context, _ string, userID uuid.UUID, accessJTI string, _ time.Time) error {
		sessUser, sessJTI = userID, accessJTI
		return nil
	}
	var upsertEmail, upsertName string
	users := &stubUserService{
		upsert: func(_ context.Context, email, displayName string, _ *string) (userclient.User, error) {
			upsertEmail, upsertName = email, displayName
			return u, nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantPairRec(t, rec, stubAccessJWT)

	// The verified claims flowed to the user service, the identity was
	// bound to the upserted user, and the session row was keyed to it.
	if upsertEmail != "alice@example.com" || upsertName != "Alice" {
		t.Fatalf("upsert(%q, %q)", upsertEmail, upsertName)
	}
	if boundProvider != "google" || boundSubject != "google-sub-1" || boundUser != u.ID {
		t.Fatalf("bind(%q, %q, %v) vs user %v", boundProvider, boundSubject, boundUser, u.ID)
	}
	if sessUser != u.ID || sessJTI == "" {
		t.Fatalf("session user=%v jti=%q", sessUser, sessJTI)
	}
}

// --- completeLogin identity-first resolution ---

// A known identity signs in as its bound user even though the email
// upsert would have answered a different account.
func TestUnitCompleteLogin_KnownIdentityWinsOverEmail(t *testing.T) {
	boundUser := uuid.New()
	var upsertCalled bool
	var sessUser uuid.UUID
	st := &stubStore{
		resolveIdentity: func(_ context.Context, provider, subject, email string) (store.Identity, error) {
			if provider != "dev" || subject != "dev-alice" || email != "alice@example.com" {
				t.Fatalf("resolve args = %s %s %s", provider, subject, email)
			}
			return store.Identity{ID: uuid.New(), UserID: boundUser}, nil
		},
		createSession: func(_ context.Context, _ string, userID uuid.UUID, _ string, _ time.Time) error {
			sessUser = userID
			return nil
		},
	}
	users := &stubUserService{
		get: func(_ context.Context, id uuid.UUID) (userclient.User, error) {
			if id != boundUser {
				t.Fatalf("get id = %s, want the bound user %s", id, boundUser)
			}
			return userclient.User{ID: boundUser, Roles: []string{"user"}}, nil
		},
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			upsertCalled = true
			return userclient.User{}, nil
		},
	}
	// Drive via the dev-token route, like TestUnitDevToken_SuccessDelegatesToCompleteLogin.
	h := newUnit(st, unitMinter(), users, nil, &stubVerifier{}, true)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token", api.DevTokenRequest{User: "alice"}))
	wantPairRec(t, rec, stubAccessJWT)
	if sessUser != boundUser {
		t.Fatalf("session user = %v, want the identity's bound user %v", sessUser, boundUser)
	}
	if upsertCalled {
		t.Fatal("email upsert must not run once the identity resolves")
	}
}

// A known identity whose user lookup fails for a reason other than
// "not found" is an upstream fault, not a healing opportunity.
func TestUnitCompleteLogin_KnownIdentityUserLookupError(t *testing.T) {
	st := consumeGoogle()
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		return store.Identity{ID: uuid.New(), UserID: uuid.New()}, nil
	}
	users := &stubUserService{
		get: func(context.Context, uuid.UUID) (userclient.User, error) {
			return userclient.User{}, errStub
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusBadGateway, "user_service_error")
}

// First-time identity: the email fallback creates/finds the user and
// the identity is bound insert-only.
func TestUnitCompleteLogin_NewIdentityFallsBackToEmail(t *testing.T) {
	u := upsertedUser()
	st := consumeGoogle() // resolveIdentity defaults to ErrIdentityNotFound
	var boundProvider, boundSubject, boundEmail string
	var boundUser uuid.UUID
	st.bindIdentity = func(_ context.Context, provider, subject, email string, userID uuid.UUID) error {
		boundProvider, boundSubject, boundEmail, boundUser = provider, subject, email, userID
		return nil
	}
	var sessUser uuid.UUID
	st.createSession = func(_ context.Context, _ string, userID uuid.UUID, _ string, _ time.Time) error {
		sessUser = userID
		return nil
	}
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return u, nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantPairRec(t, rec, stubAccessJWT)
	if boundProvider != "google" || boundSubject != "google-sub-1" ||
		boundEmail != "alice@example.com" || boundUser != u.ID {
		t.Fatalf("bind(%q, %q, %q, %v), want (google, google-sub-1, alice@example.com, %v)",
			boundProvider, boundSubject, boundEmail, boundUser, u.ID)
	}
	if sessUser != u.ID {
		t.Fatalf("session user = %v, want the upserted user %v", sessUser, u.ID)
	}
}

// Identity points at a vanished user: heal through the email path and
// rebind explicitly (never through the insert-only BindIdentity).
func TestUnitCompleteLogin_DeadUserHealsViaEmail(t *testing.T) {
	dead := uuid.New()
	fresh := upsertedUser()
	st := consumeGoogle()
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		return store.Identity{ID: uuid.New(), UserID: dead}, nil
	}
	var rebindProvider, rebindSubject, rebindEmail string
	var rebindUser uuid.UUID
	st.rebindIdentity = func(_ context.Context, provider, subject, email string, userID uuid.UUID) error {
		rebindProvider, rebindSubject, rebindEmail, rebindUser = provider, subject, email, userID
		return nil
	}
	var sessUser uuid.UUID
	st.createSession = func(_ context.Context, _ string, userID uuid.UUID, _ string, _ time.Time) error {
		sessUser = userID
		return nil
	}
	users := &stubUserService{
		get: func(_ context.Context, id uuid.UUID) (userclient.User, error) {
			if id != dead {
				t.Fatalf("get id = %s, want the dead user %s", id, dead)
			}
			return userclient.User{}, userclient.ErrUserNotFound
		},
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return fresh, nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantPairRec(t, rec, stubAccessJWT)
	if rebindProvider != "google" || rebindSubject != "google-sub-1" ||
		rebindEmail != "alice@example.com" || rebindUser != fresh.ID {
		t.Fatalf("rebind(%q, %q, %q, %v), want (google, google-sub-1, alice@example.com, %v)",
			rebindProvider, rebindSubject, rebindEmail, rebindUser, fresh.ID)
	}
	if sessUser != fresh.ID {
		t.Fatalf("session user = %v, want the healed user %v", sessUser, fresh.ID)
	}
}

// Healing a dead identity whose email upsert itself fails surfaces the
// same gateway error as the first-time-identity upsert failure.
func TestUnitCompleteLogin_HealUpsertError(t *testing.T) {
	st := consumeGoogle()
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		return store.Identity{ID: uuid.New(), UserID: uuid.New()}, nil
	}
	users := &stubUserService{
		get: func(context.Context, uuid.UUID) (userclient.User, error) {
			return userclient.User{}, userclient.ErrUserNotFound
		},
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return userclient.User{}, errStub
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusBadGateway, "user_service_error")
}

// Healing a dead identity whose RebindIdentity call fails is an
// internal error: the survivor was found but the move did not stick.
func TestUnitCompleteLogin_HealRebindError(t *testing.T) {
	st := consumeGoogle()
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		return store.Identity{ID: uuid.New(), UserID: uuid.New()}, nil
	}
	st.rebindIdentity = func(context.Context, string, string, string, uuid.UUID) error {
		return errStub
	}
	users := &stubUserService{
		get: func(context.Context, uuid.UUID) (userclient.User, error) {
			return userclient.User{}, userclient.ErrUserNotFound
		},
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return upsertedUser(), nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

// A bind that loses a race to a concurrent link resolves once more and
// signs in as the new owner, instead of failing the login.
func TestUnitCompleteLogin_BindRaceResolvesOnce(t *testing.T) {
	winner := uuid.New()
	st := consumeGoogle()
	var resolveCalls int
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		resolveCalls++
		if resolveCalls == 1 {
			return store.Identity{}, store.ErrIdentityNotFound
		}
		return store.Identity{ID: uuid.New(), UserID: winner}, nil
	}
	st.bindIdentity = func(context.Context, string, string, string, uuid.UUID) error {
		return store.ErrIdentityTaken
	}
	var sessUser uuid.UUID
	st.createSession = func(_ context.Context, _ string, userID uuid.UUID, _ string, _ time.Time) error {
		sessUser = userID
		return nil
	}
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return upsertedUser(), nil
		},
		get: func(_ context.Context, id uuid.UUID) (userclient.User, error) {
			if id != winner {
				t.Fatalf("get id = %s, want the race winner %s", id, winner)
			}
			return userclient.User{ID: winner, Roles: []string{"user"}}, nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantPairRec(t, rec, stubAccessJWT)
	if resolveCalls != 2 {
		t.Fatalf("ResolveIdentity called %d times, want 2 (initial miss + post-race resolve)", resolveCalls)
	}
	if sessUser != winner {
		t.Fatalf("session user = %v, want the race winner %v", sessUser, winner)
	}
}

// A bind race whose post-race resolve itself fails is an internal
// error: the login cannot tell who won.
func TestUnitCompleteLogin_BindRaceResolveError(t *testing.T) {
	st := consumeGoogle()
	var resolveCalls int
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		resolveCalls++
		if resolveCalls == 1 {
			return store.Identity{}, store.ErrIdentityNotFound
		}
		return store.Identity{}, errStub
	}
	st.bindIdentity = func(context.Context, string, string, string, uuid.UUID) error {
		return store.ErrIdentityTaken
	}
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return upsertedUser(), nil
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

// A bind race whose winner resolves but whose user lookup fails is a
// gateway error, not an internal one: the user service is the fault.
func TestUnitCompleteLogin_BindRaceUserLookupError(t *testing.T) {
	st := consumeGoogle()
	var resolveCalls int
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		resolveCalls++
		if resolveCalls == 1 {
			return store.Identity{}, store.ErrIdentityNotFound
		}
		return store.Identity{ID: uuid.New(), UserID: uuid.New()}, nil
	}
	st.bindIdentity = func(context.Context, string, string, string, uuid.UUID) error {
		return store.ErrIdentityTaken
	}
	users := &stubUserService{
		upsert: func(context.Context, string, string, *string) (userclient.User, error) {
			return upsertedUser(), nil
		},
		get: func(context.Context, uuid.UUID) (userclient.User, error) {
			return userclient.User{}, errStub
		},
	}
	rec := callbackInto(t, st, unitMinter(), users, verifiedClaims())
	wantProblemRec(t, rec, http.StatusBadGateway, "user_service_error")
}

// Store failures on the resolve path stay internal errors.
func TestUnitCompleteLogin_ResolveStoreError(t *testing.T) {
	st := consumeGoogle()
	st.resolveIdentity = func(context.Context, string, string, string) (store.Identity, error) {
		return store.Identity{}, errStub
	}
	rec := callbackInto(t, st, unitMinter(), &stubUserService{}, verifiedClaims())
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

// --- completeLink (the shared tail of a link-mode OauthCallback and DevLink) ---

func TestUnitCompleteLink_EmailUnverified(t *testing.T) {
	linkUser := uuid.New()
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{Provider: "google", LinkUserID: &linkUser}, nil
	}}
	p := &stubProvider{
		name: "google",
		exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
			return oidc.IDClaims{Subject: "s", Email: "a@example.com", EmailVerified: false}, nil
		},
	}
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	wantProblemRec(t, rec, http.StatusForbidden, "link_email_unverified")
}

func TestUnitCompleteLink_UserGone(t *testing.T) {
	linkUser := uuid.New()
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{Provider: "google", LinkUserID: &linkUser}, nil
	}}
	p := &stubProvider{
		name:     "google",
		exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) { return verifiedClaims(), nil },
	}
	users := &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
		return userclient.User{}, userclient.ErrUserNotFound
	}}
	h := newUnit(st, unitMinter(), users, providerMap(p), &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "link_failed")
}

// --- RefreshToken ---

// peekOK is a stubStore whose PeekSession resolves to a fixed user, the
// starting point for the post-peek RefreshToken branches.
func peekOK(userID uuid.UUID) *stubStore {
	return &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
		return store.Session{UserID: userID}, nil
	}}
}

// getOK is a stubUserService whose Get returns a fixed user with roles.
func getOK(userID uuid.UUID) *stubUserService {
	return &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
		return userclient.User{ID: userID, Roles: []string{"user"}}, nil
	}}
}

func refreshReq(t *testing.T, body any) *http.Request {
	t.Helper()
	return jsonReq(t, http.MethodPost, "/token/refresh", body)
}

func TestUnitRefresh_InvalidBody(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitRefresh_MissingToken(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: ""}))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitRefresh_TokenNotFound(t *testing.T) {
	st := &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
		return store.Session{}, store.ErrRefreshNotFound
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "nope"}))
	wantProblemRec(t, rec, http.StatusUnauthorized, "invalid_refresh")
}

func TestUnitRefresh_FamilyAlreadyRevoked(t *testing.T) {
	// A peek that finds the family already revoked short-circuits to
	// refresh_reused WITHOUT going through Rotate, and reports an empty
	// (but present) revoke_jtis array: the live jtis were already signaled
	// at the original reuse detection.
	st := &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
		return store.Session{}, store.ErrRefreshRevoked
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "dead"}))
	p := wantProblemRec(t, rec, http.StatusUnauthorized, "refresh_reused")
	if p.RevokeJTIs == nil || len(p.RevokeJTIs) != 0 {
		t.Fatalf("revoke_jtis = %v, want an empty present array", p.RevokeJTIs)
	}
	if st.rotateCalls != 0 {
		t.Fatalf("Rotate ran %d times; the revoked-peek path must short-circuit", st.rotateCalls)
	}
}

func TestUnitRefresh_PeekOtherError(t *testing.T) {
	st := &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
		return store.Session{}, errStub
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "x"}))
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitRefresh_UserNotFoundRevokesFamily(t *testing.T) {
	uid := uuid.New()
	st := peekOK(uid)
	st.revokeFamilyByToken = func(context.Context, string) error { return nil }
	users := &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
		return userclient.User{}, userclient.ErrUserNotFound
	}}
	h := newUnit(st, unitMinter(), users, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "x"}))
	wantProblemRec(t, rec, http.StatusUnauthorized, "invalid_refresh")
	// The vanished account's family is revoked, and the token is NOT
	// rotated.
	if st.revokeCalls != 1 {
		t.Fatalf("RevokeFamilyByToken ran %d times, want 1", st.revokeCalls)
	}
	if st.rotateCalls != 0 {
		t.Fatalf("Rotate ran %d times, want 0", st.rotateCalls)
	}
}

func TestUnitRefresh_UserServiceDownLeavesTokenUnconsumed(t *testing.T) {
	uid := uuid.New()
	st := peekOK(uid)
	users := &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
		return userclient.User{}, errStub // transient, not ErrUserNotFound
	}}
	h := newUnit(st, unitMinter(), users, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "x"}))
	wantProblemRec(t, rec, http.StatusServiceUnavailable, "user_unavailable")
	// The distinctive guard: roles are read BEFORE rotation, so an
	// upstream failure must leave the token unconsumed (no Rotate, no
	// family revoke) -- the client safely retries the same token.
	if st.rotateCalls != 0 {
		t.Fatalf("Rotate ran %d times; the token must stay unconsumed on user-service outage", st.rotateCalls)
	}
	if st.revokeCalls != 0 {
		t.Fatalf("RevokeFamilyByToken ran %d times; a transient outage must not revoke", st.revokeCalls)
	}
}

func TestUnitRefresh_MintError(t *testing.T) {
	uid := uuid.New()
	st := peekOK(uid)
	m := &stubMinter{mintErr: errStub, ttl: 5 * time.Minute}
	h := newUnit(st, m, getOK(uid), nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "x"}))
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
	if st.rotateCalls != 0 {
		t.Fatalf("Rotate ran %d times; a mint failure precedes rotation", st.rotateCalls)
	}
}

func TestUnitRefresh_RotateReuseReportsJTIs(t *testing.T) {
	uid := uuid.New()
	st := peekOK(uid)
	st.rotate = func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
		return store.RotateResult{}, &store.ReuseError{RevokedJTIs: []string{"jA", "jB"}}
	}
	h := newUnit(st, unitMinter(), getOK(uid), nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "x"}))
	p := wantProblemRec(t, rec, http.StatusUnauthorized, "refresh_reused")
	// The distinctive guard: a Rotate-detected reuse carries the live
	// jtis the BFF must denylist.
	if len(p.RevokeJTIs) != 2 || p.RevokeJTIs[0] != "jA" || p.RevokeJTIs[1] != "jB" {
		t.Fatalf("revoke_jtis = %v, want [jA jB]", p.RevokeJTIs)
	}
}

func TestUnitRefresh_RotateExpiredOrUnknown(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"not found", store.ErrRefreshNotFound},
		{"expired", store.ErrRefreshExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uid := uuid.New()
			st := peekOK(uid)
			st.rotate = func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
				return store.RotateResult{}, tc.err
			}
			h := newUnit(st, unitMinter(), getOK(uid), nil, &stubVerifier{}, false)
			rec := httptest.NewRecorder()
			h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "x"}))
			wantProblemRec(t, rec, http.StatusUnauthorized, "invalid_refresh")
		})
	}
}

func TestUnitRefresh_RotateOtherError(t *testing.T) {
	uid := uuid.New()
	st := peekOK(uid)
	st.rotate = func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
		return store.RotateResult{}, errStub
	}
	h := newUnit(st, unitMinter(), getOK(uid), nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "x"}))
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitRefresh_Success(t *testing.T) {
	uid := uuid.New()
	st := peekOK(uid)
	// Use a distinctive near-term expiry (90s) so the assertion can verify the
	// handler echoes the store's absolute ExpiresAt rather than recomputing
	// from its own refreshTTL (~30 days).
	expiry := time.Now().Add(90 * time.Second)
	var rotPresented, rotNew string
	st.rotate = func(_ context.Context, presentedHash, newHash, _ string, _ time.Duration) (store.RotateResult, error) {
		rotPresented, rotNew = presentedHash, newHash
		return store.RotateResult{UserID: uid, ExpiresAt: expiry}, nil
	}
	h := newUnit(st, unitMinter(), getOK(uid), nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: "old-raw"}))
	pair := wantPairRec(t, rec, stubAccessJWT)
	// A real rotation swaps the presented hash for a fresh one.
	if rotPresented == "" || rotNew == "" || rotPresented == rotNew {
		t.Fatalf("rotate hashes presented=%q new=%q", rotPresented, rotNew)
	}
	// Verify the handler propagated the store's absolute ExpiresAt (90s window)
	// rather than recomputing from its own refreshTTL. Allow a small tolerance
	// for execution time.
	if pair.RefreshExpiresIn < 80 || pair.RefreshExpiresIn > 90 {
		t.Fatalf("refresh_expires_in = %d, want 80..90 (handler must echo store ExpiresAt, not recompute from refreshTTL)", pair.RefreshExpiresIn)
	}
}

// --- RevokeToken ---

func TestUnitRevoke_InvalidBody(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RevokeToken(rec, jsonReq(t, http.MethodPost, "/token/revoke", "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitRevoke_StoreError(t *testing.T) {
	st := &stubStore{revokeFamilyByToken: func(context.Context, string) error { return errStub }}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.RevokeToken(rec, jsonReq(t, http.MethodPost, "/token/revoke",
		api.RevokeRequest{RefreshToken: "x"}))
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitRevoke_UnknownTokenIsIdempotent(t *testing.T) {
	// The store treats an unknown token as a no-op (nil error); logout
	// stays 204. Same observable outcome as a known token, so they share
	// this assertion path.
	for _, name := range []string{"unknown token", "known token"} {
		t.Run(name, func(t *testing.T) {
			st := &stubStore{revokeFamilyByToken: func(context.Context, string) error { return nil }}
			h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
			rec := httptest.NewRecorder()
			h.RevokeToken(rec, jsonReq(t, http.MethodPost, "/token/revoke",
				api.RevokeRequest{RefreshToken: "some-token"}))
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("204 body = %q, want empty", rec.Body.String())
			}
			if st.revokeCalls != 1 {
				t.Fatalf("RevokeFamilyByToken ran %d times, want 1", st.revokeCalls)
			}
		})
	}
}

// --- GetJwks ---

func TestUnitGetJwks_StoreError(t *testing.T) {
	st := &stubStore{activeSigningKeys: func(context.Context) ([]store.SigningKey, error) {
		return nil, errStub
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.GetJwks(rec, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitGetJwks_Success(t *testing.T) {
	st := &stubStore{activeSigningKeys: func(context.Context) ([]store.SigningKey, error) {
		return []store.SigningKey{
			{Kid: "kid-old", PublicKeyB64: "AAAAoldkey"},
			{Kid: "kid-new", PublicKeyB64: "AAAAnewkey"},
		}, nil
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.GetJwks(rec, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var doc api.Jwks
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Keys) != 2 {
		t.Fatalf("keys = %+v", doc.Keys)
	}
	// Every key is an Ed25519 OKP key whose x is the stored public key,
	// in the order the store returned them.
	for i, want := range []struct{ kid, x string }{
		{"kid-old", "AAAAoldkey"}, {"kid-new", "AAAAnewkey"},
	} {
		k := doc.Keys[i]
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid != want.kid || k.X != want.x {
			t.Fatalf("key[%d] = %+v, want kid=%q x=%q", i, k, want.kid, want.x)
		}
	}
}

// --- DevToken ---

func TestUnitDevToken_DisabledIs404(t *testing.T) {
	// dev disabled: the response is a plain 404, indistinguishable from an
	// unmounted route. The body is never decoded (DevToken short-circuits),
	// so the unwired stubs would panic if reached.
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token",
		api.DevTokenRequest{User: "alice"}))
	wantProblemRec(t, rec, http.StatusNotFound, "not_found")
}

func TestUnitDevToken_InvalidBody(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, true)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token", "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitDevToken_UnknownFixture(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, true)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token",
		api.DevTokenRequest{User: "someone-real"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "unknown_fixture")
}

func TestUnitDevToken_SuccessDelegatesToCompleteLogin(t *testing.T) {
	// A known fixture delegates to completeLogin with provider "dev" and
	// the fixture's verified claims, producing a real TokenPair.
	st := &stubStore{
		resolveIdentity: func(context.Context, string, string, string) (store.Identity, error) {
			return store.Identity{}, store.ErrIdentityNotFound
		},
		createSession: func(context.Context, string, uuid.UUID, string, time.Time) error { return nil },
	}
	var boundProvider, boundSubject string
	st.bindIdentity = func(_ context.Context, provider, subject, _ string, _ uuid.UUID) error {
		boundProvider, boundSubject = provider, subject
		return nil
	}
	u := upsertedUser()
	users := &stubUserService{
		upsert: func(_ context.Context, email, _ string, _ *string) (userclient.User, error) {
			if email != "alice@example.com" {
				t.Errorf("dev upsert email = %q, want the alice fixture", email)
			}
			return u, nil
		},
	}
	h := newUnit(st, unitMinter(), users, nil, &stubVerifier{}, true)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token",
		api.DevTokenRequest{User: "alice"}))
	wantPairRec(t, rec, stubAccessJWT)
	// completeLogin bound the dev fixture's identity, proving the shared
	// tail ran from the dev entry point too.
	if boundProvider != "dev" || boundSubject != "dev-alice" {
		t.Fatalf("bind(%q, %q), want (dev, dev-alice)", boundProvider, boundSubject)
	}
}

// --- ListIdentities / DeleteIdentity / DeleteUserAuth ---

// claimsVerifier is a stubVerifier that answers every Validate call with
// a fixed Claims regardless of the raw token presented: these tests
// drive the ownership/role branches downstream of authentication, not
// token parsing itself.
func claimsVerifier(sub string, roles ...string) *stubVerifier {
	return &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
		return jwtauth.Claims{Subject: sub, Roles: roles}, nil
	}}
}

// bearerReq builds a body-less request carrying some Bearer value;
// claimsVerifier decides what it validates to, so the raw value itself
// is never inspected.
func bearerReq(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer whatever")
	return req
}

func TestUnitListIdentities_Forbidden(t *testing.T) {
	target := uuid.New()
	caller := uuid.New() // a different account, no service role
	// stubStore is unwired: ListIdentities must never reach the store
	// once the ownership check rejects the caller.
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, claimsVerifier(caller.String(), "user"), false)
	rec := httptest.NewRecorder()
	h.ListIdentities(rec, bearerReq(http.MethodGet, "/users/"+target.String()+"/identities"), target)
	wantProblemRec(t, rec, http.StatusForbidden, "forbidden")
}

func TestUnitListIdentities_ServiceRoleReadsAnyUser(t *testing.T) {
	target := uuid.New()
	var queried uuid.UUID
	st := &stubStore{listIdentities: func(_ context.Context, userID uuid.UUID) ([]store.Identity, error) {
		queried = userID
		return nil, nil
	}}
	// The service token's subject ("svc:auth") does not parse as a uuid,
	// so requireUserOrService hands back uuid.Nil for the caller; the
	// role check is what must let the request through to the target user.
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, claimsVerifier("svc:auth", "service"), false)
	rec := httptest.NewRecorder()
	h.ListIdentities(rec, bearerReq(http.MethodGet, "/users/"+target.String()+"/identities"), target)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if queried != target {
		t.Fatalf("ListIdentities queried %v, want the path user %v", queried, target)
	}
}

func TestUnitListIdentities_StoreError(t *testing.T) {
	target := uuid.New()
	st := &stubStore{listIdentities: func(context.Context, uuid.UUID) ([]store.Identity, error) {
		return nil, errStub
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, claimsVerifier(target.String(), "user"), false)
	rec := httptest.NewRecorder()
	h.ListIdentities(rec, bearerReq(http.MethodGet, "/users/"+target.String()+"/identities"), target)
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitListIdentities_Success(t *testing.T) {
	target := uuid.New()
	rowID := uuid.New()
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	email := "alice@example.com"
	st := &stubStore{listIdentities: func(context.Context, uuid.UUID) ([]store.Identity, error) {
		return []store.Identity{{ID: rowID, Provider: "dev", Email: &email, UserID: target, CreatedAt: created}}, nil
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, claimsVerifier(target.String(), "user"), false)
	rec := httptest.NewRecorder()
	h.ListIdentities(rec, bearerReq(http.MethodGet, "/users/"+target.String()+"/identities"), target)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var body api.Identities
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Identities) != 1 {
		t.Fatalf("identities = %+v, want 1", body.Identities)
	}
	got := body.Identities[0]
	if got.Id != rowID || got.Provider != "dev" || got.Email == nil || *got.Email != email || !got.CreatedAt.Equal(created) {
		t.Fatalf("identity = %+v", got)
	}
}

func TestUnitDeleteIdentity_Outcomes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string // empty marks the 204 success case
	}{
		{"not found", store.ErrIdentityNotFound, http.StatusNotFound, "identity_not_found"},
		{"last identity", store.ErrLastIdentity, http.StatusConflict, "last_identity"},
		{"other error", errStub, http.StatusInternalServerError, "internal"},
		{"success", nil, http.StatusNoContent, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller := uuid.New()
			identityID := uuid.New()
			var gotUser, gotIdentity uuid.UUID
			st := &stubStore{deleteIdentity: func(_ context.Context, userID, idArg uuid.UUID) error {
				gotUser, gotIdentity = userID, idArg
				return tc.err
			}}
			h := newUnit(st, unitMinter(), &stubUserService{}, nil, claimsVerifier(caller.String(), "user"), false)
			rec := httptest.NewRecorder()
			h.DeleteIdentity(rec, bearerReq(http.MethodDelete, "/identities/"+identityID.String()), identityID)
			if tc.wantCode == "" {
				if rec.Code != tc.wantStatus {
					t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
				}
			} else {
				wantProblemRec(t, rec, tc.wantStatus, tc.wantCode)
			}
			if gotUser != caller || gotIdentity != identityID {
				t.Fatalf("DeleteIdentity(%v, %v), want (%v, %v)", gotUser, gotIdentity, caller, identityID)
			}
		})
	}
}

func TestUnitDeleteUserAuth_Forbidden(t *testing.T) {
	target := uuid.New()
	caller := uuid.New()
	// stubStore is unwired: DeleteUserAuth must never reach the store
	// once the self-only check rejects the caller.
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, claimsVerifier(caller.String(), "user"), false)
	rec := httptest.NewRecorder()
	h.DeleteUserAuth(rec, bearerReq(http.MethodDelete, "/users/"+target.String()+"/auth"), target)
	wantProblemRec(t, rec, http.StatusForbidden, "forbidden")
}

func TestUnitDeleteUserAuth_StoreError(t *testing.T) {
	target := uuid.New()
	st := &stubStore{deleteUserAuth: func(context.Context, uuid.UUID) error { return errStub }}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, claimsVerifier(target.String(), "user"), false)
	rec := httptest.NewRecorder()
	h.DeleteUserAuth(rec, bearerReq(http.MethodDelete, "/users/"+target.String()+"/auth"), target)
	wantProblemRec(t, rec, http.StatusInternalServerError, "internal")
}

func TestUnitDeleteUserAuth_Success(t *testing.T) {
	target := uuid.New()
	var erased uuid.UUID
	st := &stubStore{deleteUserAuth: func(_ context.Context, userID uuid.UUID) error {
		erased = userID
		return nil
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, claimsVerifier(target.String(), "user"), false)
	rec := httptest.NewRecorder()
	h.DeleteUserAuth(rec, bearerReq(http.MethodDelete, "/users/"+target.String()+"/auth"), target)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if erased != target {
		t.Fatalf("DeleteUserAuth erased %v, want %v", erased, target)
	}
}
