package userclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levonn-dev/vg-collect/services/bff/internal/userclient"
)

func TestGet_ForwardsBearerAndParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer the-users-token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/users/2b1f9c5e-3f47-4d10-9f3e-111111111111" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "2b1f9c5e-3f47-4d10-9f3e-111111111111", "email": "alice@example.test",
			"display_name": "alice", "roles": []string{"user"},
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
		})
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.Get(context.Background(), "2b1f9c5e-3f47-4d10-9f3e-111111111111", "the-users-token")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.test" || len(u.Roles) != 1 {
		t.Fatalf("user = %+v", u)
	}
}

func TestGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Not Found", "status": 404, "code": "user_not_found",
		})
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "2b1f9c5e-3f47-4d10-9f3e-111111111111", "tok")
	if !errors.Is(err, userclient.ErrUserNotFound) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

func TestGet_BadUUID(t *testing.T) {
	c, err := userclient.New("http://unused")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "not-a-uuid", "tok"); err == nil {
		t.Fatal("want error for malformed user id")
	}
}

func TestGet_NonJSON404IsNotNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html>404 from some proxy</html>"))
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "2b1f9c5e-3f47-4d10-9f3e-111111111111", "tok")
	if err == nil {
		t.Fatal("want an error for a 404")
	}
	if errors.Is(err, userclient.ErrUserNotFound) {
		t.Fatal("a non-problem 404 must be transient, not a vanished account")
	}
}
