package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauthtest"
	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/user/internal/server"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
	"github.com/levonn-dev/vgkeep/services/user/migrations"
)

// newTestStore duplicates the fixture in internal/store/store_test.go
// (Go test packages can't share helpers across packages).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	return store.New(pgtest.FreshPool(t, migrations.FS, "."))
}

type authEnv struct {
	env *jwtauthtest.Env
	v   *jwtauth.Validator
}

func newAuthEnv(t *testing.T) authEnv {
	t.Helper()
	env := jwtauthtest.NewEnv(t)
	return authEnv{env: env, v: env.Validator}
}

func (a authEnv) token(t *testing.T, sub string, roles ...string) string {
	t.Helper()
	return a.env.Token(t, sub, roles...)
}

// serviceToken mints a JWT carrying token_use=service (no roles) for sub,
// mirroring auth's internal service-token endpoint.
func (a authEnv) serviceToken(t *testing.T, sub string) string {
	t.Helper()
	return a.env.ServiceToken(t, sub)
}

func newTestServer(t *testing.T) (*httptest.Server, authEnv) {
	t.Helper()
	st := newTestStore(t)
	a := newAuthEnv(t)
	h := server.New(st, server.Options{HandleChangeCooldown: time.Hour})
	router, err := server.NewRouter(h, a.v, slog.Default(), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a
}

func do(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	req := reqtest.NewJSONRequest(t, method, url, token, body)
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
	// roles=["service"] with no token_use no longer satisfies the machine gate.
	if resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.token(t, "svc:auth", "service"), body); resp.StatusCode != 403 {
		t.Fatalf("legacy roles-only service claim: %d, want 403", resp.StatusCode)
	}
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"), body)
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
		if resp := do(t, "GET", srv.URL+"/users/"+created.ID, a.token(t, "someone-else", "service"), nil); resp.StatusCode != 403 {
			t.Fatalf("legacy roles-only service claim: %d, want 403", resp.StatusCode)
		}
		resp := do(t, "GET", srv.URL+"/users/00000000-0000-0000-0000-000000000000", a.serviceToken(t, "svc:auth"), nil)
		if resp.StatusCode != 404 {
			t.Fatalf("unknown: %d, want 404", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("404 content-type = %q", ct)
		}
	})
}

// Pins the split between UpsertUser (only auth's own service token may
// upsert) and GetUser (any service token may read); a CronJob-shaped
// token carries token_use=service like auth's, but a different subject.
func TestMachineGate_NonAuthServiceTokenSubjectPin(t *testing.T) {
	srv, a := newTestServer(t)
	cronTok := a.serviceToken(t, "svc:catalog-refresh")
	body := map[string]string{"email": "cron@example.com", "display_name": "Cron"}

	if resp := do(t, "POST", srv.URL+"/internal/users/upsert", cronTok, body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-auth service token upsert: %d, want 403", resp.StatusCode)
	}

	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert: %d", resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	if resp := do(t, "GET", srv.URL+"/users/"+created.ID, cronTok, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("non-auth service token get: %d, want 200", resp.StatusCode)
	}
}

func TestUpsert_BadRequest(t *testing.T) {
	srv, a := newTestServer(t)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "", "display_name": ""})
	if resp.StatusCode != 400 {
		t.Fatalf("empty fields: %d, want 400", resp.StatusCode)
	}
}

// Pins drop-not-fail handling of a bad OIDC picture claim: unlike
// UpdateUser (400s), the login-path upsert has no browser to 400, so an
// invalid avatar_url is stored as absent instead of failing the create.
func TestUpsertUser_InvalidAvatarUrlStoresAvatarless(t *testing.T) {
	srv, a := newTestServer(t)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "badavatar@example.com", "display_name": "Bad Avatar", "avatar_url": "ftp://not-http"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (an invalid avatar_url must never fail the login upsert)", resp.StatusCode)
	}
	var got struct {
		AvatarURL *string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.AvatarURL != nil {
		t.Fatalf("avatar_url = %q, want nil (invalid claim dropped, not stored)", *got.AvatarURL)
	}
}

func TestHealthEndpointsUnauthenticated(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

func TestGetUser_MalformedUUIDIsProblemJSON(t *testing.T) {
	srv, a := newTestServer(t)
	resp := do(t, "GET", srv.URL+"/users/not-a-uuid", a.serviceToken(t, "svc:auth"), nil)
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
	req.Header.Set("Authorization", "Bearer "+a.serviceToken(t, "svc:auth"))
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
	h := server.New(nil, server.Options{HandleChangeCooldown: time.Hour}) // store is nil; health check errors before any store call
	router, err := server.NewRouter(h, a.v, slog.Default(), func(context.Context) error {
		return errors.New("db down")
	})
	if err != nil {
		t.Fatal(err)
	}
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
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"), body)
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
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"), body)
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
			map[string]string{"handle": "Hacker"})
		reqtest.AssertProblem(t, resp, http.StatusForbidden, "forbidden")
	})

	t.Run("service token forbidden, self only", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.serviceToken(t, "svc:auth"),
			map[string]string{"handle": "Hacker"})
		reqtest.AssertProblem(t, resp, http.StatusForbidden, "forbidden")
	})

	t.Run("empty handle invalid", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"handle": ""})
		wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "handle")
	})

	t.Run("handle over 30 chars invalid", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"handle": strings.Repeat("a", 31)})
		wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "handle")
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

	// common.yaml's Handle pattern (^[a-zA-Z0-9](?:[a-zA-Z0-9_]{0,28}[a-zA-Z0-9])?$)
	// requires alnum first/last chars, so whitespace-padded handles 400 instead of trimming.
	t.Run("clean handle updates, keeps avatar", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"handle": "Neo"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got struct {
			Handle    string  `json:"handle"`
			AvatarURL *string `json:"avatar_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Handle != "Neo" {
			t.Fatalf("handle = %q, want %q", got.Handle, "Neo")
		}
		if got.AvatarURL == nil || *got.AvatarURL != "https://img.example/neo.png" {
			t.Fatalf("avatar_url = %v, want kept", got.AvatarURL)
		}
	})

	t.Run("whitespace-padded handle is now rejected, not trimmed", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"handle": " Neo  "})
		wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "handle")
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

	t.Run("valid preferred_currency updates and persists", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"preferred_currency": "EUR"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got struct {
			PreferredCurrency string `json:"preferred_currency"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.PreferredCurrency != "EUR" {
			t.Fatalf("preferred_currency = %q, want %q", got.PreferredCurrency, "EUR")
		}
	})

	t.Run("valid landing_page updates and persists", func(t *testing.T) {
		resp := do(t, "PATCH", srv.URL+"/users/"+created.ID, a.token(t, created.ID, "user"),
			map[string]string{"landing_page": "collection"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got struct {
			LandingPage string `json:"landing_page"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.LandingPage != "collection" {
			t.Fatalf("landing_page = %q, want %q", got.LandingPage, "collection")
		}
	})

	t.Run("unknown userId with matching sub 404", func(t *testing.T) {
		unknown := uuid.New().String()
		resp := do(t, "PATCH", srv.URL+"/users/"+unknown, a.token(t, unknown, "user"),
			map[string]string{"handle": "Ghost"})
		reqtest.AssertProblem(t, resp, http.StatusNotFound, "user_not_found")
	})
}

func TestDeleteUser_SelfOnlyIdempotent(t *testing.T) {
	srv, a := newTestServer(t)
	body := map[string]string{"email": "dave@example.com", "display_name": "Dave"}
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"), body)
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

// ---- Fast unit layer (no Docker, runs under -short) ----
//
// jwtauth's context key is unexported, so each test mints a real Ed25519
// token via newAuthEnv/authEnv.token rather than injecting claims directly;
// the store is the only stubbed collaborator. Each test asserts exact
// status AND problem code, not just status, so regressions can't hide.

// stubStore implements server.Store via function fields; a nil field
// panics with a clear message instead of a silent zero value.
type stubStore struct {
	upsert       func(ctx context.Context, email, displayName string, avatarURL *string, preferredCurrency string) (store.User, bool, error)
	get          func(ctx context.Context, id uuid.UUID) (store.User, error)
	update       func(ctx context.Context, id uuid.UUID, handle, avatarURL, preferredCurrency, profileVisibility, landingPage *string, cooldown time.Duration) (store.User, error)
	delete       func(ctx context.Context, id uuid.UUID) (bool, error)
	getByHandle  func(ctx context.Context, foldedHandle string) (store.User, error)
	getByIDs     func(ctx context.Context, ids []uuid.UUID) ([]store.User, error)
	searchListed func(ctx context.Context, foldedQuery string, limit int) ([]store.User, error)
}

var _ server.Store = (*stubStore)(nil)

func (s *stubStore) Upsert(ctx context.Context, email, displayName string, avatarURL *string, preferredCurrency string) (store.User, bool, error) {
	if s.upsert == nil {
		panic("unexpected Upsert")
	}
	return s.upsert(ctx, email, displayName, avatarURL, preferredCurrency)
}

func (s *stubStore) Get(ctx context.Context, id uuid.UUID) (store.User, error) {
	if s.get == nil {
		panic("unexpected Get")
	}
	return s.get(ctx, id)
}

func (s *stubStore) Update(ctx context.Context, id uuid.UUID, handle, avatarURL, preferredCurrency, profileVisibility, landingPage *string, cooldown time.Duration) (store.User, error) {
	if s.update == nil {
		panic("unexpected Update")
	}
	return s.update(ctx, id, handle, avatarURL, preferredCurrency, profileVisibility, landingPage, cooldown)
}

func (s *stubStore) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	if s.delete == nil {
		panic("unexpected Delete")
	}
	return s.delete(ctx, id)
}

func (s *stubStore) GetByHandle(ctx context.Context, foldedHandle string) (store.User, error) {
	if s.getByHandle == nil {
		panic("unexpected GetByHandle")
	}
	return s.getByHandle(ctx, foldedHandle)
}

func (s *stubStore) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]store.User, error) {
	if s.getByIDs == nil {
		panic("unexpected GetByIDs")
	}
	return s.getByIDs(ctx, ids)
}

func (s *stubStore) SearchListed(ctx context.Context, foldedQuery string, limit int) ([]store.User, error) {
	if s.searchListed == nil {
		panic("unexpected SearchListed")
	}
	return s.searchListed(ctx, foldedQuery, limit)
}

// newUnitServer wires a test HTTP server to the stub store; claims travel
// through the real jwtauth.Middleware, so tests mint a real JWT via a.token.
func newUnitServer(t *testing.T, st server.Store) (*httptest.Server, authEnv) {
	t.Helper()
	a := newAuthEnv(t)
	h := server.New(st, server.Options{HandleChangeCooldown: time.Hour})
	router, err := server.NewRouter(h, a.v, slog.Default(), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a
}

// wantUnitProblemDetail is reqtest.AssertProblem plus a substring check on
// detail, for validation branches where the field name matters.
func wantUnitProblemDetail(t *testing.T, resp *http.Response, wantStatus int, wantCode, detailSubstr string) {
	t.Helper()
	p := reqtest.AssertProblem(t, resp, wantStatus, wantCode)
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
	reqtest.AssertProblem(t, resp, http.StatusForbidden, "forbidden")
}

func TestUnitUpsert_MalformedJSON_BadRequest(t *testing.T) {
	// A service-role token with a syntactically invalid body must get 400.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.serviceToken(t, "svc:auth"), "{not json}")
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpsert_EmptyEmail_BadRequest(t *testing.T) {
	// Missing email (empty string) must get 400 before the store is called.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "", "display_name": "Alice"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpsert_EmptyDisplayName_BadRequest(t *testing.T) {
	// Missing display_name must get 400 before the store is called.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "a@example.com", "display_name": ""})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpsert_StoreError_InternalServerError(t *testing.T) {
	// When the store returns a non-nil error, the handler must return 500.
	st := &stubStore{
		upsert: func(context.Context, string, string, *string, string) (store.User, bool, error) {
			return store.User{}, false, errStubUser
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "a@example.com", "display_name": "Alice"})
	reqtest.AssertProblem(t, resp, http.StatusInternalServerError, "internal")
}

func TestUnitUpsert_Success_ReturnsAPIUser(t *testing.T) {
	// Happy path: service role + valid body + successful store -> 200 with
	// the full api.User shape (id, email, handle, roles).
	wantID := uuid.New()
	st := &stubStore{
		upsert: func(_ context.Context, email, _ string, _ *string, _ string) (store.User, bool, error) {
			return store.User{
				ID: wantID, Email: email, Handle: "Alice", ProfileVisibility: "private",
				Roles: []string{"user"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}, true, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "a@example.com", "display_name": "Alice"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		ID     string   `json:"id"`
		Email  string   `json:"email"`
		Handle string   `json:"handle"`
		Roles  []string `json:"roles"`
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
	if body.Handle != "Alice" {
		t.Errorf("handle = %q", body.Handle)
	}
	if len(body.Roles) != 1 || body.Roles[0] != "user" {
		t.Errorf("roles = %v, want [user]", body.Roles)
	}
}

// Pins that the handler maps the hint and the store receives the derived currency.
func TestUnitUpsert_LocaleHintSeedsCurrency(t *testing.T) {
	var gotCurrency string
	st := &stubStore{
		upsert: func(_ context.Context, email, name string, _ *string, preferredCurrency string) (store.User, bool, error) {
			gotCurrency = preferredCurrency
			return store.User{Email: email, Handle: name, PreferredCurrency: preferredCurrency, Roles: []string{"user"}}, true, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert",
		a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "d@example.com", "display_name": "Dora", "locale_hint": "de-DE"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if gotCurrency != "EUR" {
		t.Fatalf("currency reaching the store: %q, want EUR", gotCurrency)
	}
	var got struct {
		PreferredCurrency string `json:"preferred_currency"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PreferredCurrency != "EUR" {
		t.Fatalf("preferred_currency in response: %q", got.PreferredCurrency)
	}
}

// --- GetUser unit branch matrix ---

func TestUnitGetUser_NotSubjectNotServiceNotAdmin_Forbidden(t *testing.T) {
	// A "user"-role caller requesting a different user's profile gets 403.
	targetID := uuid.New()
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "GET", srv.URL+"/users/"+targetID.String(),
		a.token(t, "different-user-id", "user"), nil)
	reqtest.AssertProblem(t, resp, http.StatusForbidden, "forbidden")
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
		a.serviceToken(t, "svc"), nil)
	reqtest.AssertProblem(t, resp, http.StatusNotFound, "user_not_found")
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
		a.serviceToken(t, "svc"), nil)
	reqtest.AssertProblem(t, resp, http.StatusInternalServerError, "internal")
}

func TestUnitGetUser_SelfRead_OK(t *testing.T) {
	// A user reading their own profile (sub == userId) must get 200.
	userID := uuid.New()
	st := &stubStore{
		get: func(_ context.Context, id uuid.UUID) (store.User, error) {
			return store.User{
				ID: id, Email: "self@example.com", Handle: "Self",
				Roles: []string{"user"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}, nil
		},
	}
	srv, a := newUnitServer(t, st)
	// Token subject matches the requested userId; the authz guard passes.
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
				ID: id, Email: "other@example.com", Handle: "Other",
				Roles: []string{"user"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/users/"+targetID.String(),
		a.serviceToken(t, "svc-identity"), nil)
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
				ID: id, Email: "target@example.com", Handle: "Target",
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
// TestUpdateUser_SelfOnlyAndValidation above covers the self-only guard,
// validation, and trim/clear semantics end-to-end; the two tests below
// cover what that docker-backed test cannot: malformed JSON and a generic store failure.

func TestUnitUpdateUser_MalformedJSON_BadRequest(t *testing.T) {
	// A self token with a syntactically invalid body must get 400, and the
	// empty stubStore proves the store is never reached.
	userID := uuid.New()
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "PATCH", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), "{not json}")
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpdateUser_StoreError_InternalServerError(t *testing.T) {
	// A generic (non-sentinel) store error must surface as 500 internal.
	userID := uuid.New()
	st := &stubStore{
		update: func(context.Context, uuid.UUID, *string, *string, *string, *string, *string, time.Duration) (store.User, error) {
			return store.User{}, errStubUser
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "PATCH", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), map[string]string{"handle": "Neo"})
	reqtest.AssertProblem(t, resp, http.StatusInternalServerError, "internal")
}

func TestUnitUpdateUser_HandleTaken_Conflict(t *testing.T) {
	// store.ErrHandleTaken must surface as 409 handle_taken.
	userID := uuid.New()
	st := &stubStore{
		update: func(context.Context, uuid.UUID, *string, *string, *string, *string, *string, time.Duration) (store.User, error) {
			return store.User{}, store.ErrHandleTaken
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "PATCH", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), map[string]string{"handle": "taken_handle"})
	reqtest.AssertProblem(t, resp, http.StatusConflict, "handle_taken")
}

func TestUnitUpdateUser_HandleCooldown_TooManyRequests(t *testing.T) {
	// store.ErrHandleCooldown must surface as 429 handle_cooldown.
	userID := uuid.New()
	st := &stubStore{
		update: func(context.Context, uuid.UUID, *string, *string, *string, *string, *string, time.Duration) (store.User, error) {
			return store.User{}, store.ErrHandleCooldown
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "PATCH", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), map[string]string{"handle": "new_handle"})
	reqtest.AssertProblem(t, resp, http.StatusTooManyRequests, "handle_cooldown")
}

func TestUnitUpdateUser_PreferredCurrencyValidation(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid),
		map[string]string{"preferred_currency": "eur"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

func TestUnitUpdateUser_InvalidProfileVisibility(t *testing.T) {
	// profile_visibility outside {private, unlisted, listed} must 400 before
	// the store call (empty stubStore proves it), or the DB CHECK would 500 it.
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid),
		map[string]string{"profile_visibility": "public"})
	wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "profile_visibility")
}

func TestUnitUpdateUser_InvalidLandingPage(t *testing.T) {
	// landing_page outside {collection, feed, explore} must 400 before the
	// store call (empty stubStore proves it), or the DB CHECK would 500 it.
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid),
		map[string]string{"landing_page": "homepage"})
	wantUnitProblemDetail(t, resp, http.StatusBadRequest, "invalid_body", "landing_page")
}

// --- DeleteUser unit branch matrix ---
//
// TestDeleteUser_SelfOnlyIdempotent above covers the self-only guard and
// idempotent success end-to-end; the test below covers the generic 500.

func TestUnitDeleteUser_StoreError_InternalServerError(t *testing.T) {
	// A generic (non-sentinel) store error must surface as 500 internal.
	userID := uuid.New()
	st := &stubStore{
		delete: func(context.Context, uuid.UUID) (bool, error) { return false, errStubUser },
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "DELETE", srv.URL+"/users/"+userID.String(),
		a.token(t, userID.String(), "user"), nil)
	reqtest.AssertProblem(t, resp, http.StatusInternalServerError, "internal")
}
