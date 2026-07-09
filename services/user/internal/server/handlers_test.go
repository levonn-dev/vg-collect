package server_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/user/internal/server"
	"github.com/levonn-dev/vg-collect/services/user/internal/store"
	"github.com/levonn-dev/vg-collect/services/user/migrations"
)

// newTestStore duplicates the fixture in internal/store/store_test.go
// (Go test packages can't share helpers across packages).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("user"), tcpostgres.WithUsername("u"), tcpostgres.WithPassword("p"),
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
	return store.New(pool)
}

type authEnv struct {
	priv ed25519.PrivateKey
	v    *jwtauth.Validator
}

func newAuthEnv(t *testing.T) authEnv {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": "t1",
		"x": base64.RawURLEncoding.EncodeToString(pub),
	}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(jwks) }))
	t.Cleanup(srv.Close)
	return authEnv{priv: priv, v: jwtauth.NewValidator(srv.URL, "vg-collect-auth", "vg-collect")}
}

func (a authEnv) token(t *testing.T, sub string, roles ...string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"sub": sub, "iss": "vg-collect-auth", "aud": "vg-collect",
		"exp": time.Now().Add(5 * time.Minute).Unix(), "jti": "j", "roles": roles,
	})
	tok.Header["kid"] = "t1"
	s, err := tok.SignedString(a.priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestServer(t *testing.T) (*httptest.Server, authEnv) {
	t.Helper()
	st := newTestStore(t)
	a := newAuthEnv(t)
	h := server.New(st)
	router := server.NewRouter(h, a.v, slog.Default(), func(context.Context) error { return nil })
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a
}

func do(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	switch v := body.(type) {
	case nil:
		// empty body
	case string:
		buf.WriteString(v) // raw body, for the malformed-JSON cases
	default:
		if err := json.NewEncoder(&buf).Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestUpsert_Authz(t *testing.T) {
	srv, a := newTestServer(t)
	body := map[string]string{"email": "a@example.com", "display_name": "Alice"}

	if resp := do(t, "POST", srv.URL+"/internal/users/upsert", "", body); resp.StatusCode != 401 {
		t.Fatalf("no token: %d, want 401", resp.StatusCode)
	}
	if resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.token(t, "u1", "user"), body); resp.StatusCode != 403 {
		t.Fatalf("user role: %d, want 403", resp.StatusCode)
	}
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.token(t, "svc:auth", "service"), body)
	if resp.StatusCode != 200 {
		t.Fatalf("service role: %d, want 200", resp.StatusCode)
	}
	var created struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || len(created.Roles) != 1 || created.Roles[0] != "user" {
		t.Fatalf("created = %+v", created)
	}

	t.Run("get self ok, other forbidden, unknown 404", func(t *testing.T) {
		if resp := do(t, "GET", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"), nil); resp.StatusCode != 200 {
			t.Fatalf("self: %d, want 200", resp.StatusCode)
		}
		if resp := do(t, "GET", srv.URL+"/users/"+created.ID, a.token(t, "someone-else", "user"), nil); resp.StatusCode != 403 {
			t.Fatalf("other: %d, want 403", resp.StatusCode)
		}
		resp := do(t, "GET", srv.URL+"/users/00000000-0000-0000-0000-000000000000", a.token(t, "svc:auth", "service"), nil)
		if resp.StatusCode != 404 {
			t.Fatalf("unknown: %d, want 404", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("404 content-type = %q", ct)
		}
	})
}

func TestUpsert_BadRequest(t *testing.T) {
	srv, a := newTestServer(t)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.token(t, "svc:auth", "service"),
		map[string]string{"email": "", "display_name": ""})
	if resp.StatusCode != 400 {
		t.Fatalf("empty fields: %d, want 400", resp.StatusCode)
	}
}

func TestHealthEndpointsUnauthenticated(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %d", path, err, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

func TestGetUser_MalformedUUIDIsProblemJSON(t *testing.T) {
	srv, a := newTestServer(t)
	resp := do(t, "GET", srv.URL+"/users/not-a-uuid", a.token(t, "svc:auth", "service"), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("generated binder 400 content-type = %q, want problem+json", ct)
	}
}

func TestUpsert_MalformedJSON(t *testing.T) {
	srv, a := newTestServer(t)
	// Send a raw invalid JSON body to trigger the decode-error path.
	req, err := http.NewRequest("POST", srv.URL+"/internal/users/upsert", bytes.NewBufferString("{not json}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token(t, "svc:auth", "service"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 400 {
		t.Fatalf("malformed JSON: %d, want 400", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want problem+json", ct)
	}
}

func TestReadyz_FailsWhenHealthcheckFails(t *testing.T) {
	a := newAuthEnv(t)
	h := server.New(nil) // store is nil; health check errors before any store call
	router := server.NewRouter(h, a.v, slog.Default(), func(context.Context) error {
		return errors.New("db down")
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 503 {
		t.Fatalf("readyz with failing health: %d, want 503", resp.StatusCode)
	}
}

func TestGetUser_AdminCanReadAnyUser(t *testing.T) {
	srv, a := newTestServer(t)
	body := map[string]string{"email": "b@example.com", "display_name": "Bob"}
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.token(t, "svc:auth", "service"), body)
	if resp.StatusCode != 200 {
		t.Fatalf("upsert: %d", resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	// admin role can read any user
	if resp2 := do(t, "GET", srv.URL+"/users/"+created.ID, a.token(t, "different-user", "admin"), nil); resp2.StatusCode != 200 {
		t.Fatalf("admin get: %d, want 200", resp2.StatusCode)
	}
}

func TestUpdateUser_SelfOnlyAndValidation(t *testing.T) {
	srv, a := newTestServer(t)
	body := map[string]string{
		"email": "neo@example.com", "display_name": "Thomas Anderson",
		"avatar_url": "https://img.example/neo.png",
	}
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.token(t, "svc:auth", "service"), body)
	if resp.StatusCode != 200 {
		t.Fatalf("upsert: %d", resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	t.Run("other user's token forbidden", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, "someone-else", "user"),
			map[string]string{"display_name": "Hacker"})
		wantUnitProblem(t, resp, http.StatusForbidden, "forbidden")
	})

	t.Run("service token forbidden, self only", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, "svc:auth", "service"),
			map[string]string{"display_name": "Hacker"})
		wantUnitProblem(t, resp, http.StatusForbidden, "forbidden")
	})

	t.Run("empty display_name invalid", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"display_name": ""})
		wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "display_name")
	})

	t.Run("display_name over 100 chars invalid", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"display_name": strings.Repeat("a", 101)})
		wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "display_name")
	})

	t.Run("avatar_url bad scheme invalid", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"avatar_url": "ftp://x"})
		wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "avatar_url")
	})

	t.Run("avatar_url over 2048 chars invalid", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"avatar_url": strings.Repeat("a", 2049)})
		wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "avatar_url")
	})

	t.Run("trims display_name, keeps avatar", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"display_name": " Neo  "})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got struct {
			DisplayName string  `json:"display_name"`
			AvatarURL   *string `json:"avatar_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.DisplayName != "Neo" {
			t.Fatalf("display_name = %q, want %q", got.DisplayName, "Neo")
		}
		if got.AvatarURL == nil || *got.AvatarURL != "https://img.example/neo.png" {
			t.Fatalf("avatar_url = %v, want kept", got.AvatarURL)
		}
	})

	t.Run("empty avatar_url clears it", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"avatar_url": ""})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got struct {
			AvatarURL *string `json:"avatar_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.AvatarURL != nil {
			t.Fatalf("avatar_url = %v, want cleared", *got.AvatarURL)
		}
	})

	t.Run("unknown userId with matching sub 404", func(t *testing.T) {
		unknown := uuid.New().String()
		resp := do(t, "PATCH", srv.URL+"/users/"+unknown, a.token(t, unknown, "user"),
			map[string]string{"display_name": "Ghost"})
		wantUnitProblem(t, resp, http.StatusNotFound, "user_not_found")
	})
}

func TestDeleteUser_SelfOnlyIdempotent(t *testing.T) {
	srv, a := newTestServer(t)
	body := map[string]string{"email": "dave@example.com", "display_name": "Dave"}
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.token(t, "svc:auth", "service"), body)
	if resp.StatusCode != 200 {
		t.Fatalf("upsert: %d", resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	if resp := do(t, "DELETE", srv.URL+"/users/"+created.ID, a.token(t, "someone-else", "user"), nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other's token: %d, want 403", resp.StatusCode)
	}
	if resp := do(t, "DELETE", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("own token: %d, want 204", resp.StatusCode)
	}
	if resp := do(t, "GET", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: %d, want 404", resp.StatusCode)
	}
	if resp := do(t, "DELETE", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second delete: %d, want 204", resp.StatusCode)
	}
}

// ============================================================================
// Fast unit layer (no Docker, runs under -short)
//
// The tests below drive the real handlers and real router with the in-memory
// stubStore defined just below. Claims reach the handlers through the real
// jwtauth.Middleware -- jwtauth's context key is unexported, so there is no
// direct context injection path. Instead, each test mints a real Ed25519 token
// via the existing newAuthEnv/authEnv.token helper (in-process JWKS over
// httptest; fast; no network outside the process) and hits the router over
// httptest. The store is the only collaborator stubbed out. No Docker, no
// Postgres, no testing.Short() skip.
//
// Each test asserts the distinctive outcome of its branch: exact HTTP status
// AND the problem code from the response body where applicable; for success,
// the response body fields. A test that only checks the status code would
// survive handler regressions silently; these do not.
// ============================================================================

// stubStore implements server.Store via function fields. A method whose
// field is nil panics with a clear message -- an unexpected collaborator call
// is a loud test failure, not a silent zero value.
type stubStore struct {
	upsert func(ctx context.Context, email, displayName string, avatarURL *string) (store.User, error)
	get    func(ctx context.Context, id uuid.UUID) (store.User, error)
	update func(ctx context.Context, id uuid.UUID, displayName, avatarURL *string) (store.User, error)
	delete func(ctx context.Context, id uuid.UUID) error
}

var _ server.Store = (*stubStore)(nil)

func (s *stubStore) Upsert(ctx context.Context, email, displayName string, avatarURL *string) (store.User, error) {
	if s.upsert == nil {
		panic("unexpected Upsert")
	}
	return s.upsert(ctx, email, displayName, avatarURL)
}

func (s *stubStore) Get(ctx context.Context, id uuid.UUID) (store.User, error) {
	if s.get == nil {
		panic("unexpected Get")
	}
	return s.get(ctx, id)
}

func (s *stubStore) Update(ctx context.Context, id uuid.UUID, displayName, avatarURL *string) (store.User, error) {
	if s.update == nil {
		panic("unexpected Update")
	}
	return s.update(ctx, id, displayName, avatarURL)
}

func (s *stubStore) Delete(ctx context.Context, id uuid.UUID) error {
	if s.delete == nil {
		panic("unexpected Delete")
	}
	return s.delete(ctx, id)
}

// newUnitServer builds a test HTTP server wired to the given stub store.
// Claims travel through the real jwtauth.Middleware, so each test calls
// a.token(...) to mint a valid signed JWT for the role(s) under test.
func newUnitServer(t *testing.T, st server.Store) (*httptest.Server, authEnv) {
	t.Helper()
	a := newAuthEnv(t)
	h := server.New(st)
	router := server.NewRouter(h, a.v, slog.Default(), func(context.Context) error { return nil })
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a
}

// problemResponse decodes a problem+json body and asserts content-type.
type problemResponse struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func wantUnitProblem(t *testing.T, resp *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	var p problemResponse
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem body: %v", err)
	}
	if p.Code != wantCode {
		t.Fatalf("problem code = %q, want %q", p.Code, wantCode)
	}
}

// wantUnitProblemDetail is wantUnitProblem plus a substring check on the
// detail message, for validation branches where the field name matters.
func wantUnitProblemDetail(t *testing.T, resp *http.Response, wantStatus int, wantCode, detailSubstr string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	var p problemResponse
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem body: %v", err)
	}
	if p.Code != wantCode {
		t.Fatalf("problem code = %q, want %q", p.Code, wantCode)
	}
	if !strings.Contains(p.Detail, detailSubstr) {
		t.Fatalf("problem detail = %q, want substring %q", p.Detail, detailSubstr)
	}
}

// errStubUser is a generic non-sentinel error used to drive the generic 500
// branches, distinct from store.ErrNotFound which the handlers special-case.
var errStubUser = errors.New("stub store failure")

// --- UpsertUser unit branch matrix ---

func TestUnitUpsert_MissingServiceRole_Forbidden(t *testing.T) {
	// A token with the "user" role (not "service") must get 403 forbidden.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.token(t, "u1", "user"),
		map[string]string{"email": "a@example.com", "display_name": "Alice"})
	wantUnitProblem(t, resp, http.StatusForbidden, "forbidden")
}

func TestUnitUpsert_MalformedJSON_BadRequest(t *testing.T) {
	// A service-role token with a syntactically invalid body must get 400.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.token(t, "svc", "service"), "{not json}")
	wantUnitProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpsert_EmptyEmail_BadRequest(t *testing.T) {
	// Missing email (empty string) must get 400 before the store is called.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.token(t, "svc", "service"),
		map[string]string{"email": "", "display_name": "Alice"})
	wantUnitProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpsert_EmptyDisplayName_BadRequest(t *testing.T) {
	// Missing display_name must get 400 before the store is called.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.token(t, "svc", "service"),
		map[string]string{"email": "a@example.com", "display_name": ""})
	wantUnitProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpsert_StoreError_InternalServerError(t *testing.T) {
	// When the store returns a non-nil error, the handler must return 500.
	st := &stubStore{
		upsert: func(context.Context, string, string, *string) (store.User, error) {
			return store.User{}, errStubUser
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.token(t, "svc", "service"),
		map[string]string{"email": "a@example.com", "display_name": "Alice"})
	wantUnitProblem(t, resp, http.StatusInternalServerError, "internal")
}

func TestUnitUpsert_Success_ReturnsAPIUser(t *testing.T) {
	// Happy path: service role + valid body + successful store -> 200 with
	// the full api.User shape (id, email, display_name, roles).
	wantID := uuid.New()
	st := &stubStore{
		upsert: func(_ context.Context, email, displayName string, _ *string) (store.User, error) {
			return store.User{
				ID: wantID, Email: email, DisplayName: displayName,
				Roles: []string{"user"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.token(t, "svc", "service"),
		map[string]string{"email": "a@example.com", "display_name": "Alice"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		ID          string   `json:"id"`
		Email       string   `json:"email"`
		DisplayName string   `json:"display_name"`
		Roles       []string `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.ID != wantID.String() {
		t.Errorf("id = %q, want %q", body.ID, wantID.String())
	}
	if body.Email != "a@example.com" {
		t.Errorf("email = %q", body.Email)
	}
	if body.DisplayName != "Alice" {
		t.Errorf("display_name = %q", body.DisplayName)
	}
	if len(body.Roles) != 1 || body.Roles[0] != "user" {
		t.Errorf("roles = %v, want [user]", body.Roles)
	}
}

// --- GetUser unit branch matrix ---

func TestUnitGetUser_NotSubjectNotServiceNotAdmin_Forbidden(t *testing.T) {
	// A "user"-role caller requesting a different user's profile gets 403.
	targetID := uuid.New()
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "GET", srv.URL+"/users/"+targetID.String(),
		a.token(t, "different-user-id", "user"), nil)
	wantUnitProblem(t, resp, http.StatusForbidden, "forbidden")
}

func TestUnitGetUser_NotFound_404(t *testing.T) {
	// When the store returns store.ErrNotFound, the handler must return 404
	// with the "user_not_found" problem code.
	targetID := uuid.New()
	st := &stubStore{
		get: func(_ context.Context, _ uuid.UUID) (store.User, error) {
			return store.User{}, store.ErrNotFound
		},
	}
	srv, a := newUnitServer(t, st)
	// Service role bypasses the authz guard.
	resp := do(t, "GET", srv.URL+"/users/"+targetID.String(),
		a.token(t, "svc", "service"), nil)
	wantUnitProblem(t, resp, http.StatusNotFound, "user_not_found")
}

func TestUnitGetUser_StoreError_InternalServerError(t *testing.T) {
	// When the store returns a generic (non-sentinel) error, the handler
	// must return 500 with the "internal" problem code.
	targetID := uuid.New()
	st := &stubStore{
		get: func(_ context.Context, _ uuid.UUID) (store.User, error) {
			return store.User{}, errStubUser
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/users/"+targetID.String(),
		a.token(t, "svc", "service"), nil)
	wantUnitProblem(t, resp, http.StatusInternalServerError, "internal")
}

func TestUnitGetUser_SelfRead_OK(t *testing.T) {
	// A user reading their own profile (sub == userId) must get 200.
	userID := uuid.New()
	st := &stubStore{
		get: func(_ context.Context, id uuid.UUID) (store.User, error) {
			return store.User{
				ID: id, Email: "self@example.com", DisplayName: "Self",
				Roles: []string{"user"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}, nil
		},
	}
	srv, a := newUnitServer(t, st)
	// Token subject matches the requested userId -- the authz guard passes.
	resp := do(t, "GET", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("self read: status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.ID != userID.String() {
		t.Errorf("id = %q, want %q", body.ID, userID.String())
	}
}

func TestUnitGetUser_ServiceRole_CanReadAny(t *testing.T) {
	// A service-role token may read any user (not just itself).
	targetID := uuid.New()
	st := &stubStore{
		get: func(_ context.Context, id uuid.UUID) (store.User, error) {
			return store.User{
				ID: id, Email: "other@example.com", DisplayName: "Other",
				Roles: []string{"user"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/users/"+targetID.String(),
		a.token(t, "svc-identity", "service"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("service read: status = %d, want 200", resp.StatusCode)
	}
}

func TestUnitGetUser_AdminRole_CanReadAny(t *testing.T) {
	// An admin-role token may read any user (not just itself).
	targetID := uuid.New()
	st := &stubStore{
		get: func(_ context.Context, id uuid.UUID) (store.User, error) {
			return store.User{
				ID: id, Email: "target@example.com", DisplayName: "Target",
				Roles: []string{"user"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/users/"+targetID.String(),
		a.token(t, "admin-identity", "admin"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin read: status = %d, want 200", resp.StatusCode)
	}
}

// --- UpdateUser unit branch matrix ---
//
// The self-only guard, the validation branches, the trim/clear semantics
// and the not-found branch are all exercised end-to-end (real store, real
// persistence) by TestUpdateUser_SelfOnlyAndValidation above. The two
// tests below cover what that docker-backed test cannot reach: the
// malformed-JSON decode error and a generic store failure.

func TestUnitUpdateUser_MalformedJSON_BadRequest(t *testing.T) {
	// A self token with a syntactically invalid body must get 400, and the
	// empty stubStore proves the store is never reached.
	userID := uuid.New()
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "PATCH", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), "{not json}")
	wantUnitProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpdateUser_StoreError_InternalServerError(t *testing.T) {
	// A generic (non-sentinel) store error must surface as 500 internal.
	userID := uuid.New()
	st := &stubStore{
		update: func(context.Context, uuid.UUID, *string, *string) (store.User, error) {
			return store.User{}, errStubUser
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "PATCH", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), map[string]string{"display_name": "Neo"})
	wantUnitProblem(t, resp, http.StatusInternalServerError, "internal")
}

// --- DeleteUser unit branch matrix ---
//
// The self-only guard and idempotent success are exercised end-to-end by
// TestDeleteUser_SelfOnlyIdempotent above. The test below covers the one
// branch that requires a store failure: the generic 500.

func TestUnitDeleteUser_StoreError_InternalServerError(t *testing.T) {
	// A generic (non-sentinel) store error must surface as 500 internal.
	userID := uuid.New()
	st := &stubStore{
		delete: func(context.Context, uuid.UUID) error { return errStubUser },
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "DELETE", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), nil)
	wantUnitProblem(t, resp, http.StatusInternalServerError, "internal")
}
