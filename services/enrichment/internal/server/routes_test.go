package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/store"
)

// newBareRouter builds a router with nil collaborators: routing and
// auth-gating tests never reach them (the skeleton handlers answer
// before any collaborator call).
// testInternalToken is the accepted internal-caller token across the
// server tests.
const testInternalToken = "test-internal-token"

func newBareRouter(t *testing.T, ready func(context.Context) error) (http.Handler, *authEnv) {
	t.Helper()
	env := newAuthEnv(t)
	h := New(nil, nil, nil, nil, Options{
		InternalRefreshSecrets: []string{testInternalToken},
		Logger:                 slog.New(slog.DiscardHandler),
	})
	if ready == nil {
		ready = func(context.Context) error { return nil }
	}
	return NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), ready), env
}

func TestRoutes_HealthOutsideAuth(t *testing.T) {
	router, _ := newBareRouter(t, nil)
	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
}

func TestRoutes_ReadyzReportsNotReady(t *testing.T) {
	router, _ := newBareRouter(t, func(context.Context) error { return errors.New("mongo down") })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz: %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "not_ready") {
		t.Fatalf("readyz body: %s", body)
	}
}

func TestRoutes_APIRequiresBearer(t *testing.T) {
	router, env := newBareRouter(t, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?type=game&q=zelda", nil))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("tokenless: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}

	// SearchCatalog is implemented now, so the authorized case needs
	// real (stub) collaborators behind the
	// router instead of newBareRouter's nil ones -- a nil Cache panics
	// on the handler's first call rather than answering. Stub a game
	// provider that serves this exact query and require a genuine 200
	// with a non-empty result: that outcome is reachable only once the
	// request has cleared the JWT boundary and been served by the live
	// handler, which still pins this sub-case's original intent (a
	// valid bearer token makes 401 impossible), just proven from the
	// success side instead of via a permanent placeholder status.
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1029, Name: "Zelda"}}, nil
	}}
	h := newUnitHandlers(nil, games, nil, newStubCache())
	tok := env.token(t, "11111111-1111-1111-1111-111111111111", []string{"user"})
	rec = serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=zelda", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized: %d %s", rec.Code, rec.Body.String())
	}
	var res api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Results) == 0 {
		t.Fatalf("authorized: expected non-empty results, got %+v", res)
	}

	req := httptest.NewRequest(http.MethodGet, "/search?type=game&q=zelda", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage token: %d", rec.Code)
	}
}

// The CronJob endpoint must be routed and JWT-exempt (its own guard is
// the internal token, exercised fully in the handler tests). This pins
// routing + the jwtauth bypass without pinning the handler's eventual
// status code: a jwtauth rejection would carry missing_token, which
// must never appear here.
//
// InternalRefresh is implemented now (unlike newBareRouter's other
// callers, which never reach a real handler body): an accepted token
// detaches a real walk, so newBareRouter's nil Store would panic on a
// nil interface once that walk starts. A stub Store with an empty
// catalog stands in instead, keeping the walk a harmless no-op while
// still proving the route lives outside jwtauth.
func TestRoutes_InternalRefreshIsJWTExempt(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{listPriced: func(context.Context) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	router := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/refresh", nil)
	req.Header.Set("X-Internal-Token", testInternalToken)
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("internal refresh must be routed, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "missing_token") {
		t.Fatalf("internal refresh must not sit behind jwtauth: %s", rec.Body.String())
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
}

func TestRoutes_ParamBindingIsProblemJSON(t *testing.T) {
	router, env := newBareRouter(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/search?q=zelda", nil) // type missing
	req.Header.Set("Authorization", "Bearer "+env.token(t, "11111111-1111-1111-1111-111111111111", []string{"user"}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("binding failure: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "invalid_param") {
		t.Fatalf("binding body: %s", body)
	}
}
