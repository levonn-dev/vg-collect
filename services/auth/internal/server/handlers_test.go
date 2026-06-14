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

func newStubUsersServer(t *testing.T, m *token.Minter) *stubUsersServer {
	t.Helper()
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": m.Kid(),
		"x": base64.RawURLEncoding.EncodeToString(m.PublicKey()),
	}}})
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwks)
	}))
	t.Cleanup(jwksSrv.Close)

	f := &stubUsersServer{
		t: t, v: jwtauth.NewValidator(jwksSrv.URL, "vg-collect-auth", "vg-collect"),
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
	h := server.New(st, m, uc, providers, devEnabled, 30*24*time.Hour)
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

type problemBody struct {
	Status     int      `json:"status"`
	Code       string   `json:"code"`
	RevokeJTIs []string `json:"revoke_jtis"`
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
			h := server.New(nil, nil, nil, tc.providers, tc.devEnabled, 0)
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
	bindIdentity        func(ctx context.Context, provider, subject string, userID uuid.UUID) error
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

func (s *stubStore) BindIdentity(ctx context.Context, provider, subject string, userID uuid.UUID) error {
	if s.bindIdentity == nil {
		panic("unexpected BindIdentity")
	}
	return s.bindIdentity(ctx, provider, subject, userID)
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
	providers map[string]oidc.Provider, devEnabled bool) *server.Handlers {
	return server.New(st, m, users, providers, devEnabled, unitRefreshTTL)
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
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, false)
	rec := httptest.NewRecorder()
	h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start", "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitOauthStart_UnknownProvider(t *testing.T) {
	// Valid JSON, but the provider is not in the enabled map.
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{},
		map[string]oidc.Provider{}, false)
	rec := httptest.NewRecorder()
	h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start",
		api.StartRequest{Provider: "twitch"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "unknown_provider")
}

func TestUnitOauthStart_CreateStateError(t *testing.T) {
	st := &stubStore{createState: func(context.Context, store.AuthState) error { return errStub }}
	p := &stubProvider{name: "google"}
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), false)
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
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, false)
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
		h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "bogus"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_state")
}

func TestUnitOauthCallback_ConsumeStateError(t *testing.T) {
	st := &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{}, errStub
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
		map[string]oidc.Provider{}, false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), false)
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
	h := newUnit(st, m, users, providerMap(p), false)
	rec := httptest.NewRecorder()
	h.OauthCallback(rec, jsonReq(t, http.MethodPost, "/oauth/callback",
		api.CallbackRequest{Code: "c", State: "s"}))
	return rec
}

// consumeGoogle is the stubStore wiring shared by the completeLogin tests
// reached through the callback: a one-shot state for provider "google".
func consumeGoogle() *stubStore {
	return &stubStore{consumeState: func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{Provider: "google"}, nil
	}}
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
	st.bindIdentity = func(context.Context, string, string, uuid.UUID) error { return errStub }
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
	st.bindIdentity = func(context.Context, string, string, uuid.UUID) error { return nil }
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
	st.bindIdentity = func(context.Context, string, string, uuid.UUID) error { return nil }
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
	st.bindIdentity = func(_ context.Context, provider, subject string, userID uuid.UUID) error {
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
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitRefresh_MissingToken(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, false)
	rec := httptest.NewRecorder()
	h.RefreshToken(rec, refreshReq(t, api.RefreshRequest{RefreshToken: ""}))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitRefresh_TokenNotFound(t *testing.T) {
	st := &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
		return store.Session{}, store.ErrRefreshNotFound
	}}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
	h := newUnit(st, unitMinter(), users, nil, false)
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
	h := newUnit(st, unitMinter(), users, nil, false)
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
	h := newUnit(st, m, getOK(uid), nil, false)
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
	h := newUnit(st, unitMinter(), getOK(uid), nil, false)
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
			h := newUnit(st, unitMinter(), getOK(uid), nil, false)
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
	h := newUnit(st, unitMinter(), getOK(uid), nil, false)
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
	h := newUnit(st, unitMinter(), getOK(uid), nil, false)
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
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, false)
	rec := httptest.NewRecorder()
	h.RevokeToken(rec, jsonReq(t, http.MethodPost, "/token/revoke", "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitRevoke_StoreError(t *testing.T) {
	st := &stubStore{revokeFamilyByToken: func(context.Context, string) error { return errStub }}
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
			h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
	h := newUnit(st, unitMinter(), &stubUserService{}, nil, false)
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
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, false)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token",
		api.DevTokenRequest{User: "alice"}))
	wantProblemRec(t, rec, http.StatusNotFound, "not_found")
}

func TestUnitDevToken_InvalidBody(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, true)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token", "{not json"))
	wantProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

func TestUnitDevToken_UnknownFixture(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, true)
	rec := httptest.NewRecorder()
	h.DevToken(rec, jsonReq(t, http.MethodPost, "/oauth/dev/token",
		api.DevTokenRequest{User: "someone-real"}))
	wantProblemRec(t, rec, http.StatusBadRequest, "unknown_fixture")
}

func TestUnitDevToken_SuccessDelegatesToCompleteLogin(t *testing.T) {
	// A known fixture delegates to completeLogin with provider "dev" and
	// the fixture's verified claims, producing a real TokenPair.
	st := &stubStore{
		createSession: func(context.Context, string, uuid.UUID, string, time.Time) error { return nil },
	}
	var boundProvider, boundSubject string
	st.bindIdentity = func(_ context.Context, provider, subject string, _ uuid.UUID) error {
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
	h := newUnit(st, unitMinter(), users, nil, true)
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
