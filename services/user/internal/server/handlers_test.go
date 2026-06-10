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
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
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
