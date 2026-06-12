package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// --- fake user service (validates service tokens through jwtauth) ---

type userRec struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Roles       []string
}

type fakeUsers struct {
	t   *testing.T
	srv *httptest.Server
	v   *jwtauth.Validator

	mu      sync.Mutex
	byEmail map[string]*userRec
	byID    map[uuid.UUID]*userRec
	fail    bool
}

func newFakeUsers(t *testing.T, m *token.Minter) *fakeUsers {
	t.Helper()
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": m.Kid(),
		"x": base64.RawURLEncoding.EncodeToString(m.PublicKey()),
	}}})
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwks)
	}))
	t.Cleanup(jwksSrv.Close)

	f := &fakeUsers{
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

func (f *fakeUsers) authorize(w http.ResponseWriter, r *http.Request) bool {
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

func (f *fakeUsers) upsert(w http.ResponseWriter, r *http.Request) {
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

func (f *fakeUsers) get(w http.ResponseWriter, r *http.Request) {
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

func (f *fakeUsers) setRoles(email string, roles ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byEmail[email].Roles = roles
}

func (f *fakeUsers) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}

func (f *fakeUsers) remove(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byEmail[email]; ok {
		delete(f.byID, u.ID)
		delete(f.byEmail, email)
	}
}

func (f *fakeUsers) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byEmail)
}

// --- slim fake IdP (discovery + jwks + token endpoint) ---

type fakeIDP struct {
	t     *testing.T
	srv   *httptest.Server
	key   *rsa.PrivateKey
	codes map[string]jwt.MapClaims
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{t: t, key: key, codes: map[string]jwt.MapClaims{}}
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

func (f *fakeIDP) registerCode(code, nonce string, extra jwt.MapClaims) {
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
	users  *fakeUsers
	idp    *fakeIDP
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
	fu := newFakeUsers(t, m)
	uc, err := userclient.New(fu.srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	idp := newFakeIDP(t)
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
	// No code registered at the fake IdP: the exchange 400s upstream.
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
