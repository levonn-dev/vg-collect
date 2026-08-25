// Tests for saved view CRUD.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

func TestUnitViews(t *testing.T) {
	user := uuid.New()
	params := map[string]any{"filters": map[string]any{"status": []string{"backlog"}}, "view_mode": "grid"}

	t.Run("create round-trips params verbatim", func(t *testing.T) {
		var storedParams []byte
		st := &stubStore{createView: func(_ context.Context, _ uuid.UUID, name string, p []byte, _ string) (store.View, error) {
			storedParams = p
			return store.View{ID: uuid.New(), Name: name, Params: p}, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/views", a.token(t, user.String()),
			jsonBody(map[string]any{"name": "Backlog grid", "params": params}))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
		var got struct {
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.Params["view_mode"] != "grid" || storedParams == nil {
			t.Fatalf("params round-trip: %+v / %s", got.Params, storedParams)
		}
	})

	t.Run("validation", func(t *testing.T) {
		// The empty stubStore proves the store is never reached: a
		// bad request must 400 before any of these get near it.
		srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(map[string]any{"name": "", "params": params})),
			http.StatusBadRequest, "invalid_body")
		// params too large: an ~9KB filler.
		big := map[string]any{"name": "big", "params": map[string]any{"blob": strings.Repeat("x", 9000)}}
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(big)),
			http.StatusBadRequest, "invalid_body")
		// visibility outside {private, unlisted, listed}: the generated enum type
		// has no UnmarshalJSON validation, so this must reject it, or only the DB
		// CHECK would catch it and the client would see a 500 instead of a 400.
		bad := map[string]any{"name": "x", "params": params, "visibility": "public"}
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(bad)),
			http.StatusBadRequest, "invalid_body")
	})

	t.Run("conflicts and not-found map", func(t *testing.T) {
		st := &stubStore{
			createView: func(context.Context, uuid.UUID, string, []byte, string) (store.View, error) {
				return store.View{}, store.ErrNameTaken
			},
			updateView: func(context.Context, uuid.UUID, uuid.UUID, string, []byte, string) (store.View, error) {
				return store.View{}, store.ErrNotFound
			},
			deleteView: func(context.Context, uuid.UUID, uuid.UUID) error { return store.ErrNotFound },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(map[string]any{"name": "x", "params": params})),
			http.StatusConflict, "view_exists")
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/views/"+uuid.NewString(), tok, jsonBody(map[string]any{"name": "x", "params": params})),
			http.StatusNotFound, "view_not_found")
		wantProblem(t, do(t, http.MethodDelete, srv.URL+"/views/"+uuid.NewString(), tok, nil),
			http.StatusNotFound, "view_not_found")
	})

	t.Run("update success 200", func(t *testing.T) {
		viewID := uuid.New()
		newParams := map[string]any{"filters": map[string]any{"status": []string{"completed"}}, "view_mode": "list"}
		paramsJSON, _ := json.Marshal(newParams)
		updatedView := store.View{ID: viewID, Name: "Completed List", Params: paramsJSON}
		st := &stubStore{updateView: func(_ context.Context, _ uuid.UUID, id uuid.UUID, name string, p []byte, _ string) (store.View, error) {
			if id == viewID && name == "Completed List" {
				return updatedView, nil
			}
			return store.View{}, store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/views/"+viewID.String(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "Completed List", "params": newParams}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update: %d", resp.StatusCode)
		}
		var got struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.Name != "Completed List" || got.Params["view_mode"] != "list" {
			t.Fatalf("update response: %+v", got)
		}
	})

	t.Run("update name-taken 409", func(t *testing.T) {
		st := &stubStore{updateView: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string, _ []byte, _ string) (store.View, error) {
			return store.View{}, store.ErrNameTaken
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/views/"+uuid.NewString(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "taken", "params": params})),
			http.StatusConflict, "view_exists")
	})

	t.Run("delete success 204", func(t *testing.T) {
		viewID := uuid.New()
		st := &stubStore{deleteView: func(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
			if id == viewID {
				return nil
			}
			return store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodDelete, srv.URL+"/views/"+viewID.String(), a.token(t, user.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
	})

	t.Run("list seeds defaults on the zero-view case", func(t *testing.T) {
		u := uuid.New()
		var seedCalled bool
		var listCalls int
		defaults := []store.View{
			{ID: uuid.New(), UserID: u, Name: "All Games", Slug: "all_games", Params: []byte(`{}`)},
			{ID: uuid.New(), UserID: u, Name: "Backlog", Slug: "backlog", Params: []byte(`{}`)},
		}
		st := &stubStore{
			listViews: func(context.Context, uuid.UUID) ([]store.View, error) {
				listCalls++
				if listCalls == 1 {
					return nil, nil
				}
				return defaults, nil
			},
			seedDefaultViews: func(context.Context, uuid.UUID) error {
				seedCalled = true
				return nil
			},
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: %d", resp.StatusCode)
		}
		if !seedCalled {
			t.Fatal("the zero-view case must seed the starter shelves")
		}
		if listCalls != 2 {
			t.Fatalf("must re-list after seeding: %d calls", listCalls)
		}
		var got struct {
			Views []struct{ Name string } `json:"views"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if len(got.Views) != 2 {
			t.Fatalf("want the two seeded defaults back: %+v", got.Views)
		}
	})

	t.Run("list does not seed when views already exist", func(t *testing.T) {
		u := uuid.New()
		existing := []store.View{{ID: uuid.New(), UserID: u, Name: "Mine", Slug: "mine", Params: []byte(`{}`)}}
		// seedDefaultViews is intentionally left nil: a call panics
		// loudly, proving the non-empty case never seeds.
		st := &stubStore{listViews: func(context.Context, uuid.UUID) ([]store.View, error) {
			return existing, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: %d", resp.StatusCode)
		}
		var got struct {
			Views []struct{ Name string } `json:"views"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if len(got.Views) != 1 || got.Views[0].Name != "Mine" {
			t.Fatalf("want the existing view unchanged: %+v", got.Views)
		}
	})

	t.Run("list seed failure maps to 500", func(t *testing.T) {
		u := uuid.New()
		st := &stubStore{
			listViews:        func(context.Context, uuid.UUID) ([]store.View, error) { return nil, nil },
			seedDefaultViews: func(context.Context, uuid.UUID) error { return errors.New("seed boom") },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("list re-list failure maps to 500", func(t *testing.T) {
		u := uuid.New()
		var listCalls int
		st := &stubStore{
			listViews: func(context.Context, uuid.UUID) ([]store.View, error) {
				listCalls++
				if listCalls == 1 {
					return nil, nil
				}
				return nil, errors.New("relist boom")
			},
			seedDefaultViews: func(context.Context, uuid.UUID) error { return nil },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}
