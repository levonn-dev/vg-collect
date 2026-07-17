package authclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

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
	if reused.Error() != "authclient: refresh token reuse detected" {
		t.Fatalf("message = %q", reused.Error())
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
		{http.StatusForbidden, "link_email_unverified", authclient.ErrLinkEmailUnverified},
		{http.StatusConflict, "identity_already_linked", authclient.ErrLinkConflict},
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

func TestCallbackLinkedProvider(t *testing.T) {
	google := "google"
	cases := []struct {
		name   string
		linked *string
	}{
		{"plain_login_has_no_linked_provider", nil},
		{"link_flow_names_the_provider", &google},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
				body := pairJSON()
				if tc.linked != nil {
					body["linked_provider"] = *tc.linked
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(body)
			})
			pair, err := c.Callback(context.Background(), "c", "s")
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.linked == nil && pair.LinkedProvider != nil:
				t.Fatalf("want nil LinkedProvider, got %q", *pair.LinkedProvider)
			case tc.linked != nil && (pair.LinkedProvider == nil || *pair.LinkedProvider != *tc.linked):
				t.Fatalf("LinkedProvider = %v, want %q", pair.LinkedProvider, *tc.linked)
			}
		})
	}
}

func TestLinkStart(t *testing.T) {
	var gotAuth, gotPath, gotProvider string
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotProvider = body["provider"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"authorize_url": "https://idp/link?x=1"})
	})
	url, err := c.LinkStart(context.Background(), "twitch", "users-token")
	if err != nil || url != "https://idp/link?x=1" {
		t.Fatalf("url=%q err=%v", url, err)
	}
	if gotPath != "/oauth/link/start" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer users-token" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if gotProvider != "twitch" {
		t.Fatalf("provider = %q", gotProvider)
	}
}

func TestLinkStartFailure(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusBadRequest, "unknown_provider", nil)
	})
	if _, err := c.LinkStart(context.Background(), "nope", "tok"); !errors.Is(err, authclient.ErrLoginFailed) {
		t.Fatalf("want ErrLoginFailed, got %v", err)
	}
}

func TestDevLink(t *testing.T) {
	var gotAuth, gotPath string
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["user"] != "alice" {
			t.Errorf("user = %q", body["user"])
		}
		resp := pairJSON()
		resp["linked_provider"] = "dev"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	pair, err := c.DevLink(context.Background(), "alice", "users-token")
	if err != nil {
		t.Fatal(err)
	}
	if pair.LinkedProvider == nil || *pair.LinkedProvider != "dev" {
		t.Fatalf("LinkedProvider = %v", pair.LinkedProvider)
	}
	if gotPath != "/oauth/dev/link" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer users-token" {
		t.Fatalf("bearer = %q", gotAuth)
	}
}

func TestDevLinkConflict(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusConflict, "identity_already_linked", nil)
	})
	if _, err := c.DevLink(context.Background(), "alice", "tok"); !errors.Is(err, authclient.ErrLinkConflict) {
		t.Fatalf("want ErrLinkConflict, got %v", err)
	}
}

func TestListIdentities(t *testing.T) {
	const uid = "2b1f9c5e-3f47-4d10-9f3e-111111111111"
	var gotAuth, gotPath string
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"identities": []map[string]any{
				{"id": "11111111-1111-1111-1111-111111111111", "provider": "google", "created_at": "2026-01-01T00:00:00Z"},
			},
		})
	})
	ids, err := c.ListIdentities(context.Background(), uid, "users-token")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/users/"+uid+"/identities" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer users-token" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if len(ids) != 1 || ids[0].Provider != "google" {
		t.Fatalf("identities = %+v", ids)
	}
}

func TestListIdentitiesBadUUID(t *testing.T) {
	c, err := authclient.New("http://unused")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListIdentities(context.Background(), "not-a-uuid", "tok"); err == nil {
		t.Fatal("want error for malformed user id")
	}
}

func TestDeleteIdentity(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"unlinked", http.StatusNoContent, nil},
		{"not_found", http.StatusNotFound, authclient.ErrIdentityNotFound},
		{"last_identity", http.StatusConflict, authclient.ErrLastIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth, gotPath, gotMethod string
			c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
				gotAuth, gotPath, gotMethod = r.Header.Get("Authorization"), r.URL.Path, r.Method
				w.WriteHeader(tc.status)
			})
			err := c.DeleteIdentity(context.Background(), id, "users-token")
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if gotMethod != http.MethodDelete || gotPath != "/identities/"+id.String() {
				t.Fatalf("method=%s path=%s", gotMethod, gotPath)
			}
			if gotAuth != "Bearer users-token" {
				t.Fatalf("bearer = %q", gotAuth)
			}
		})
	}
}

func TestDeleteUserAuth(t *testing.T) {
	const uid = "2b1f9c5e-3f47-4d10-9f3e-111111111111"
	var gotAuth, gotPath string
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.DeleteUserAuth(context.Background(), uid, "users-token"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/users/"+uid+"/auth" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer users-token" {
		t.Fatalf("bearer = %q", gotAuth)
	}
}

func TestDeleteUserAuthForbidden(t *testing.T) {
	c := stubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusForbidden, "not_your_account", nil)
	})
	if err := c.DeleteUserAuth(context.Background(), "2b1f9c5e-3f47-4d10-9f3e-111111111111", "tok"); err == nil {
		t.Fatal("want an error for a 403")
	}
}
