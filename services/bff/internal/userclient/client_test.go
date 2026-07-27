package userclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

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

func TestUpdate_ForwardsBearerAndBodyAndRelaysResult(t *testing.T) {
	const id = "2b1f9c5e-3f47-4d10-9f3e-111111111111"
	var gotAuth, gotMethod, gotPath, gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod, gotPath = r.Header.Get("Authorization"), r.Method, r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + id + `","display_name":"new-name"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Update(context.Background(), id, "the-users-token", []byte(`{"display_name":"new-name"}`))
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/users/"+id {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer the-users-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotContentType != "application/json" || string(gotBody) != `{"display_name":"new-name"}` {
		t.Fatalf("contentType=%q body=%s", gotContentType, gotBody)
	}
	if res.Status != http.StatusOK || res.ContentType != "application/json" ||
		string(res.Body) != `{"id":"`+id+`","display_name":"new-name"}` {
		t.Fatalf("result = %+v", res)
	}
}

func TestUpdate_RelaysValidationProblemVerbatim(t *testing.T) {
	const id = "2b1f9c5e-3f47-4d10-9f3e-111111111111"
	const problemBody = `{"type":"about:blank","title":"Bad Request","status":400,"code":"invalid_body","detail":"display_name"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(problemBody))
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Update(context.Background(), id, "tok", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusBadRequest || res.ContentType != "application/problem+json" || string(res.Body) != problemBody {
		t.Fatalf("result = %+v", res)
	}
}

func TestUpdate_BadUUID(t *testing.T) {
	c, err := userclient.New("http://unused")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Update(context.Background(), "not-a-uuid", "tok", []byte(`{}`)); err == nil {
		t.Fatal("want error for malformed user id")
	}
}

func TestDelete_ForwardsBearerAndSucceeds(t *testing.T) {
	const id = "2b1f9c5e-3f47-4d10-9f3e-111111111111"
	var gotAuth, gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod, gotPath = r.Header.Get("Authorization"), r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), id, "the-users-token"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/users/"+id {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer the-users-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestDelete_NonNoContentIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Forbidden", "status": 403,
		})
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "2b1f9c5e-3f47-4d10-9f3e-111111111111", "tok"); err == nil {
		t.Fatal("want an error for a non-204 status")
	}
}

func TestDelete_BadUUID(t *testing.T) {
	c, err := userclient.New("http://unused")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "not-a-uuid", "tok"); err == nil {
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

func TestSharedProfile_ForwardsBearerAndDecodes(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id": "2b1f9c5e-3f47-4d10-9f3e-111111111111", "handle": "alice",
			"profile_visibility": "unlisted",
		})
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	card, err := c.SharedProfile(context.Background(), "the-users-token", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/profiles/alice" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer the-users-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if card.Handle != "alice" || string(card.ProfileVisibility) != "unlisted" {
		t.Fatalf("card = %+v", card)
	}
}

func TestSharedProfile_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Not Found", "status": 404, "code": "profile_not_found",
		})
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SharedProfile(context.Background(), "tok", "nobody")
	if !errors.Is(err, userclient.ErrProfileNotFound) {
		t.Fatalf("want ErrProfileNotFound, got %v", err)
	}
}

// TestSharedProfile_PrivateHandleAlsoErrProfileNotFound pins the
// deliberate ambiguity: a private handle's problem+json 404 is
// indistinguishable from an unknown one, both ErrProfileNotFound.
func TestSharedProfile_PrivateHandleAlsoErrProfileNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Not Found", "status": 404, "code": "profile_not_found",
			"detail": "private",
		})
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SharedProfile(context.Background(), "tok", "private-handle")
	if !errors.Is(err, userclient.ErrProfileNotFound) {
		t.Fatalf("want ErrProfileNotFound, got %v", err)
	}
}

func TestSharedProfile_NonJSON404IsNotProfileNotFound(t *testing.T) {
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
	_, err = c.SharedProfile(context.Background(), "tok", "alice")
	if err == nil {
		t.Fatal("want an error for a 404")
	}
	if errors.Is(err, userclient.ErrProfileNotFound) {
		t.Fatal("a non-problem 404 must be transient, not a resolved private/unknown handle")
	}
}

func TestSharedCardsByIDs_ForwardsBearerAndDecodes(t *testing.T) {
	const id = "2b1f9c5e-3f47-4d10-9f3e-111111111111"
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"profiles":[{"user_id":"` + id + `","handle":"alice","profile_visibility":"listed"}]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cards, err := c.SharedCardsByIDs(context.Background(), "tok", []uuid.UUID{uuid.MustParse(id)})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/profiles/by-ids" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if len(cards) != 1 || cards[0].Handle != "alice" {
		t.Fatalf("cards = %+v", cards)
	}
}

func TestSharedCardsByIDs_NonOKIsErrUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SharedCardsByIDs(context.Background(), "tok", []uuid.UUID{uuid.New()})
	if !errors.Is(err, userclient.ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

func TestSearchProfiles_ForwardsBearerQueryAndRelaysBody(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"profiles":[{"handle":"alice"}]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SearchProfiles(context.Background(), "the-users-token", "ali")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/profiles/search" || gotQuery != "q=ali" {
		t.Fatalf("path=%s query=%s", gotPath, gotQuery)
	}
	if gotAuth != "Bearer the-users-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if res.Status != http.StatusOK || res.ContentType != "application/json" || string(res.Body) != `{"profiles":[{"handle":"alice"}]}` {
		t.Fatalf("result = %+v", res)
	}
}

func TestSearchProfiles_NonOKIsErrUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "Unauthorized", "status": 401})
	}))
	t.Cleanup(srv.Close)
	c, err := userclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SearchProfiles(context.Background(), "tok", "ali")
	if !errors.Is(err, userclient.ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}
