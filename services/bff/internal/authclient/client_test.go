package authclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
)

// stubAuth answers like the auth service for one canned scenario per path.
func stubAuth(t *testing.T, handler http.HandlerFunc) *authclient.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := authclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeProblem(w http.ResponseWriter, status int, code string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code}
	for k, v := range extra {
		body[k] = v
	}
	_ = json.NewEncoder(w).Encode(body)
}

func pairJSON() map[string]any {
	return map[string]any{
		"access_token": "acc", "token_type": "Bearer", "expires_in": 300,
		"refresh_token": "ref", "refresh_expires_in": 2592000,
	}
}

func TestRefreshSuccess(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token/refresh" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pairJSON())
	})
	pair, err := c.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken != "acc" || pair.RefreshToken != "ref" || pair.RefreshExpiresIn != 2592000 {
		t.Fatalf("pair = %+v", pair)
	}
}

func TestRefreshReused(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusUnauthorized, "refresh_reused",
			map[string]any{"revoke_jtis": []string{"j1", "j2"}})
	})
	_, err := c.Refresh(context.Background(), "stolen")
	var reused *authclient.ReusedError
	if !errors.As(err, &reused) {
		t.Fatalf("want ReusedError, got %v", err)
	}
	if len(reused.RevokeJTIs) != 2 || reused.RevokeJTIs[0] != "j1" {
		t.Fatalf("jtis = %v", reused.RevokeJTIs)
	}
}

func TestRefreshInvalid(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusUnauthorized, "invalid_refresh", nil)
	})
	_, err := c.Refresh(context.Background(), "junk")
	if !errors.Is(err, authclient.ErrRefreshRejected) {
		t.Fatalf("want ErrRefreshRejected, got %v", err)
	}
}

func TestRefreshUserUnavailable(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusServiceUnavailable, "user_unavailable", nil)
	})
	_, err := c.Refresh(context.Background(), "ok-token")
	if !errors.Is(err, authclient.ErrUserUnavailable) {
		t.Fatalf("want ErrUserUnavailable, got %v", err)
	}
}

func TestStartAndCallback(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/start":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"authorize_url": "https://idp/auth?x=1"})
		case "/oauth/callback":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pairJSON())
		default:
			t.Errorf("path = %s", r.URL.Path)
		}
	})
	url, err := c.Start(context.Background(), "google")
	if err != nil || url != "https://idp/auth?x=1" {
		t.Fatalf("url=%q err=%v", url, err)
	}
	pair, err := c.Callback(context.Background(), "code", "state")
	if err != nil || pair.AccessToken != "acc" {
		t.Fatalf("pair=%+v err=%v", pair, err)
	}
}

func TestStartUnknownProvider(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusBadRequest, "unknown_provider", nil)
	})
	_, err := c.Start(context.Background(), "nope")
	if !errors.Is(err, authclient.ErrLoginFailed) {
		t.Fatalf("want ErrLoginFailed, got %v", err)
	}
}

func TestCallbackErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   error
	}{
		{http.StatusBadRequest, "invalid_state", authclient.ErrLoginFailed},
		{http.StatusForbidden, "email_unverified", authclient.ErrEmailUnverified},
		{http.StatusBadGateway, "provider_error", authclient.ErrProviderError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.code, func(t *testing.T) {
			c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
				writeProblem(w, tc.status, tc.code, nil)
			})
			_, err := c.Callback(context.Background(), "c", "s")
			if !errors.Is(err, tc.want) {
				t.Fatalf("code %s: want %v, got %v", tc.code, tc.want, err)
			}
		})
	}
}

func TestDevToken(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["user"] != "alice" {
			writeProblem(w, http.StatusBadRequest, "unknown_fixture", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pairJSON())
	})
	if _, err := c.DevToken(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DevToken(context.Background(), "mallory"); !errors.Is(err, authclient.ErrLoginFailed) {
		t.Fatalf("want ErrLoginFailed, got %v", err)
	}
}

func TestDevTokenDisabled(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusNotFound, "not_found", nil)
	})
	if _, err := c.DevToken(context.Background(), "alice"); !errors.Is(err, authclient.ErrLoginFailed) {
		t.Fatalf("want ErrLoginFailed, got %v", err)
	}
}

func TestProvidersAndRevoke(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/providers":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string][]string{"providers": {"google", "dev"}})
		case "/token/revoke":
			w.WriteHeader(http.StatusNoContent)
		}
	})
	names, err := c.Providers(context.Background())
	if err != nil || len(names) != 2 {
		t.Fatalf("names=%v err=%v", names, err)
	}
	if err := c.Revoke(context.Background(), "ref"); err != nil {
		t.Fatal(err)
	}
}
