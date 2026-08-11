// Tests for tag CRUD.

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

func TestUnitTags(t *testing.T) {
	user := uuid.New()
	tag := store.Tag{ID: uuid.New(), Name: "rpg", EntryCount: 2}

	t.Run("list", func(t *testing.T) {
		st := &stubStore{listTags: func(context.Context, uuid.UUID) ([]store.Tag, error) {
			return []store.Tag{tag}, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/tags", a.token(t, user.String()), nil)
		var got struct {
			Tags []struct {
				Name       string `json:"name"`
				EntryCount int    `json:"entry_count"`
			} `json:"tags"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if resp.StatusCode != http.StatusOK || len(got.Tags) != 1 || got.Tags[0].EntryCount != 2 {
			t.Fatalf("list: %d %+v", resp.StatusCode, got)
		}
	})

	t.Run("create 201, duplicate 409, invalid 400", func(t *testing.T) {
		st := &stubStore{createTag: func(_ context.Context, _ uuid.UUID, name string) (store.Tag, error) {
			if name == "taken" {
				return store.Tag{}, store.ErrNameTaken
			}
			return store.Tag{ID: uuid.New(), Name: name}, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		if resp := do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": "rpg"})); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": "taken"})),
			http.StatusConflict, "tag_exists")
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": "   "})),
			http.StatusBadRequest, "invalid_body")
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": strings.Repeat("x", 51)})),
			http.StatusBadRequest, "invalid_body")
	})

	// TestUnitTags/create_cap_exceeded_429 pins the per-user tag cap's
	// status, code, and detail (the delegated status/code choice mirrors
	// the social service's edge caps: 429 code cap_exceeded).
	t.Run("create cap exceeded 429", func(t *testing.T) {
		st := &stubStore{createTag: func(context.Context, uuid.UUID, string) (store.Tag, error) {
			return store.Tag{}, store.ErrUserTagCapExceeded
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/tags", a.token(t, user.String()), jsonBody(map[string]any{"name": "one too many"}))
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Code != "cap_exceeded" {
			t.Fatalf("code: got %q, want cap_exceeded", p.Code)
		}
		if p.Detail != "at most 200 tags per user; delete a tag to create another" {
			t.Fatalf("detail: got %q", p.Detail)
		}
	})

	t.Run("rename + delete map sentinels", func(t *testing.T) {
		st := &stubStore{
			renameTag: func(context.Context, uuid.UUID, uuid.UUID, string) (store.Tag, error) {
				return store.Tag{}, store.ErrNotFound
			},
			deleteTag: func(context.Context, uuid.UUID, uuid.UUID) error { return store.ErrNotFound },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/tags/"+uuid.NewString(), tok, jsonBody(map[string]any{"name": "x"})),
			http.StatusNotFound, "tag_not_found")
		wantProblem(t, do(t, http.MethodDelete, srv.URL+"/tags/"+uuid.NewString(), tok, nil),
			http.StatusNotFound, "tag_not_found")
	})

	t.Run("rename success 200", func(t *testing.T) {
		tagID := uuid.New()
		renamedTag := store.Tag{ID: tagID, Name: "adventure", EntryCount: 5}
		st := &stubStore{renameTag: func(_ context.Context, _ uuid.UUID, id uuid.UUID, name string) (store.Tag, error) {
			if id == tagID && name == "adventure" {
				return renamedTag, nil
			}
			return store.Tag{}, store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/tags/"+tagID.String(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "adventure"}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rename: %d", resp.StatusCode)
		}
		var got struct {
			Name       string `json:"name"`
			EntryCount int    `json:"entry_count"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.Name != "adventure" || got.EntryCount != 5 {
			t.Fatalf("rename response: %+v", got)
		}
	})

	t.Run("rename name-taken 409", func(t *testing.T) {
		st := &stubStore{renameTag: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) (store.Tag, error) {
			return store.Tag{}, store.ErrNameTaken
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/tags/"+uuid.NewString(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "existing"})),
			http.StatusConflict, "tag_exists")
	})

	t.Run("delete success 204", func(t *testing.T) {
		tagID := uuid.New()
		st := &stubStore{deleteTag: func(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
			if id == tagID {
				return nil
			}
			return store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodDelete, srv.URL+"/tags/"+tagID.String(), a.token(t, user.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
	})
}

// TestUnitCreateTag_NameCapCountsRunesNotBytes pins that the 50-character
// cap counts runes, not bytes: 50 multibyte characters (each of these
// three bytes in UTF-8) is exactly at the cap and must pass; 51 fails.
func TestUnitCreateTag_NameCapCountsRunesNotBytes(t *testing.T) {
	st := &stubStore{createTag: func(_ context.Context, _ uuid.UUID, name string) (store.Tag, error) {
		return store.Tag{ID: uuid.New(), Name: name}, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	tok := a.token(t, uuid.NewString())

	at50 := strings.Repeat("\u3042", 50) // 50 runes, 150 bytes
	resp := do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": at50}))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("50 runes at the cap must pass under rune counting: %d: %s", resp.StatusCode, body)
	}

	over50 := strings.Repeat("\u3042", 51)
	resp = do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": over50}))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}
