package userclient_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/services/auth/internal/token"
	"github.com/levonn-dev/vg-collect/services/auth/internal/userclient"
)

var testSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

func newMinter(t *testing.T) *token.Minter {
	t.Helper()
	m, err := token.NewMinter(testSeed, "vg-collect-auth", "vg-collect", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// newFakeUserService returns a fake that enforces a service-role Bearer
// token (validated via jwtauth against the minter's JWKS) before
// answering with the canned user.
func newFakeUserService(t *testing.T, m *token.Minter, status int, body any) *httptest.Server {
	t.Helper()
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": m.Kid(),
		"x": base64.RawURLEncoding.EncodeToString(m.PublicKey()),
	}}})
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwks)
	}))
	t.Cleanup(jwksSrv.Close)
	v := jwtauth.NewValidator(jwksSrv.URL, "vg-collect-auth", "vg-collect")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			t.Error("no bearer token on user-service call")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		claims, err := v.Validate(r.Context(), raw)
		if err != nil {
			t.Errorf("service token failed jwtauth validation: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !claims.HasRole("service") || claims.Subject != "svc:auth" {
			t.Errorf("claims = %+v, want service role and svc:auth subject", claims)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func userJSON(id uuid.UUID, roles ...string) map[string]any {
	return map[string]any{
		"id": id.String(), "email": "a@example.com", "display_name": "Alice",
		"roles": roles, "created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	}
}

func TestUpsert(t *testing.T) {
	m := newMinter(t)
	id := uuid.New()
	srv := newFakeUserService(t, m, http.StatusOK, userJSON(id, "user"))
	c, err := userclient.New(srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	avatar := "https://img.example/a.png"
	u, err := c.Upsert(context.Background(), "a@example.com", "Alice", &avatar, "de-DE")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != id || len(u.Roles) != 1 || u.Roles[0] != "user" {
		t.Fatalf("u = %+v", u)
	}
}

func TestGet(t *testing.T) {
	m := newMinter(t)
	id := uuid.New()
	srv := newFakeUserService(t, m, http.StatusOK, userJSON(id, "user", "admin"))
	c, err := userclient.New(srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != id || len(u.Roles) != 2 {
		t.Fatalf("u = %+v", u)
	}
}

func TestGet_NotFound(t *testing.T) {
	m := newMinter(t)
	srv := newFakeUserService(t, m, http.StatusNotFound, map[string]any{
		"type": "about:blank", "title": "Not Found", "status": 404,
	})
	c, err := userclient.New(srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), uuid.New()); !errors.Is(err, userclient.ErrUserNotFound) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

func TestUpsert_UpstreamErrorSurfaces(t *testing.T) {
	m := newMinter(t)
	srv := newFakeUserService(t, m, http.StatusInternalServerError, map[string]any{
		"type": "about:blank", "title": "Internal Server Error", "status": 500,
	})
	c, err := userclient.New(srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upsert(context.Background(), "a@example.com", "Alice", nil, ""); err == nil {
		t.Fatal("want error for 500 from user service")
	}
}

func TestGet_ServiceErrorIsNotNotFound(t *testing.T) {
	m := newMinter(t)
	srv := newFakeUserService(t, m, http.StatusInternalServerError, map[string]any{
		"type": "about:blank", "title": "Internal Server Error", "status": 500,
	})
	c, err := userclient.New(srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("want error for 500 from user service")
	}
	// The caller revokes sessions on ErrUserNotFound; a 5xx must never
	// masquerade as it.
	if errors.Is(err, userclient.ErrUserNotFound) {
		t.Fatal("500 masqueraded as ErrUserNotFound")
	}
}

func TestGet_NonJSON404IsNotNotFound(t *testing.T) {
	m := newMinter(t)
	// A 404 that is NOT problem+json (e.g. a proxy error page or a
	// misrouted USER_SERVICE_URL) must not read as "user gone".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html>not the user service</html>"))
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL, m)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), uuid.New())
	if err == nil || errors.Is(err, userclient.ErrUserNotFound) {
		t.Fatalf("non-JSON 404 must be a plain error, got %v", err)
	}
}
