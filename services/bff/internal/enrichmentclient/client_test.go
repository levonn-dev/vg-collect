package enrichmentclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSearch_RelaysBodyBearerAndStatus(t *testing.T) {
	var gotAuth, gotURL string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"degraded":false,"results":[]}`))
	})
	res, err := c.Search(context.Background(), "tok-123", "game", "zelda")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || string(res.Body) != `{"degraded":false,"results":[]}` || res.ContentType != "application/json" {
		t.Fatalf("relay: %+v", res)
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("bearer: %q", gotAuth)
	}
	if !strings.Contains(gotURL, "type=game") || !strings.Contains(gotURL, "q=zelda") {
		t.Fatalf("params: %s", gotURL)
	}
}

func TestRelay_DeclaredProblemPassesUndeclaredFails(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/products/resolve") {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"unknown_game"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})

	res, err := c.Resolve(context.Background(), "tok", []byte(`{"type":"game","igdb_game_id":1,"platform_igdb_id":2}`))
	if err != nil || res.Status != 404 || !strings.Contains(string(res.Body), "unknown_game") ||
		res.ContentType != "application/problem+json" {
		t.Fatalf("declared problem must relay: %+v, %v", res, err)
	}

	// An upstream 401 is NOT relayed: the bff owns authentication.
	if _, err := c.Product(context.Background(), "tok", uuid.New()); !errors.Is(err, ErrUpstream) {
		t.Fatalf("undeclared status must be ErrUpstream, got %v", err)
	}
}

func TestTransportErrorSurfaces(t *testing.T) {
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "tok", "game", "zelda"); err == nil {
		t.Fatal("want transport error")
	}
}
