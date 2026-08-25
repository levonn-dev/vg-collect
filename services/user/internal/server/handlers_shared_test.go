package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
)

func TestUnitSharedProfile_VisibilityGate(t *testing.T) {
	listed := store.User{ID: uuid.New(), Handle: "Alice_Prime", ProfileVisibility: "listed"}
	private := store.User{ID: uuid.New(), Handle: "Bob_Fixture", ProfileVisibility: "private"}

	t.Run("listed resolves with folded lookup", func(t *testing.T) {
		var askedFold string
		st := &stubStore{getByHandle: func(_ context.Context, folded string) (store.User, error) {
			askedFold = folded
			return listed, nil
		}}
		srv, a := newUnitServer(t, st)
		resp := do(t, "GET", srv.URL+"/shared/profiles/ALICE__PRIME", a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if askedFold != "aliceprime" {
			t.Fatalf("lookup fold = %q, want aliceprime", askedFold)
		}
		var card struct {
			UserID            string `json:"user_id"`
			Handle            string `json:"handle"`
			ProfileVisibility string `json:"profile_visibility"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&card)
		if card.Handle != "Alice_Prime" || card.ProfileVisibility != "listed" {
			t.Fatalf("card = %+v", card)
		}
	})

	t.Run("private and unknown are the same 404", func(t *testing.T) {
		stPrivate := &stubStore{getByHandle: func(context.Context, string) (store.User, error) { return private, nil }}
		stMissing := &stubStore{getByHandle: func(context.Context, string) (store.User, error) { return store.User{}, store.ErrNotFound }}
		type problemBody struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		bodies := map[string]problemBody{}
		for name, st := range map[string]*stubStore{"private": stPrivate, "missing": stMissing} {
			srv, a := newUnitServer(t, st)
			resp := do(t, "GET", srv.URL+"/shared/profiles/whoever", a.token(t, "viewer"), nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s: status = %d, want 404", name, resp.StatusCode)
			}
			var p problemBody
			if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
				t.Fatalf("%s: decode problem body: %v", name, err)
			}
			bodies[name] = p
		}
		// private and unknown must be indistinguishable by the response body,
		// not just status - a differing body would let a caller detect existence.
		if bodies["private"] != bodies["missing"] {
			t.Fatalf("problem bodies differ: private=%+v missing=%+v", bodies["private"], bodies["missing"])
		}
		if bodies["private"].Code != "profile_not_found" || bodies["private"].Detail != "no such profile" {
			t.Fatalf("unexpected problem body: %+v", bodies["private"])
		}
	})

	t.Run("unlisted resolves by exact handle (link-only)", func(t *testing.T) {
		unlisted := store.User{ID: uuid.New(), Handle: "Quiet_One", ProfileVisibility: "unlisted"}
		st := &stubStore{getByHandle: func(context.Context, string) (store.User, error) { return unlisted, nil }}
		srv, a := newUnitServer(t, st)
		resp := do(t, "GET", srv.URL+"/shared/profiles/Quiet_One", a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
}

func TestUnitSharedByIds_ReturnsAllVisibilities(t *testing.T) {
	// Attribution rule: by-ids hydration returns cards regardless of
	// visibility; page access is gated elsewhere.
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	st := &stubStore{getByIDs: func(_ context.Context, got []uuid.UUID) ([]store.User, error) {
		if len(got) != 2 {
			t.Fatalf("ids = %v", got)
		}
		return []store.User{
			{ID: ids[0], Handle: "Open_Alice", ProfileVisibility: "listed"},
			{ID: ids[1], Handle: "Hidden_Bob", ProfileVisibility: "private"},
		}, nil
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/by-ids?ids="+ids[0].String()+"&ids="+ids[1].String(),
		a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Profiles []struct {
			Handle string `json:"handle"`
		} `json:"profiles"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Profiles) != 2 {
		t.Fatalf("profiles = %+v, want both visibilities", body.Profiles)
	}
}

func TestUnitSharedSearch_FoldsQuery(t *testing.T) {
	var askedFold string
	st := &stubStore{searchListed: func(_ context.Context, folded string, limit int) ([]store.User, error) {
		askedFold = folded
		if limit != 20 {
			t.Fatalf("limit = %d, want 20", limit)
		}
		return []store.User{{ID: uuid.New(), Handle: "Alice_Prime", ProfileVisibility: "listed"}}, nil
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/search?q=Alice_P", a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if askedFold != "alicep" {
		t.Fatalf("query fold = %q, want alicep", askedFold)
	}
}

func TestUnitSharedSearch_FoldedEmptyQuery_SkipsStore(t *testing.T) {
	// A query that folds to "" (NormalizeHandle strips underscores) short-
	// circuits before the store is touched; the empty stubStore
	// (searchListed nil) would panic if it were, proving the branch fires.
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "GET", srv.URL+"/shared/profiles/search?q=_", a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Profiles []struct {
			Handle string `json:"handle"`
		} `json:"profiles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Profiles) != 0 {
		t.Fatalf("profiles = %+v, want empty", body.Profiles)
	}
}

func TestUnitSharedSearch_StoreError_InternalServerError(t *testing.T) {
	// A generic (non-sentinel) store error must surface as 500 internal.
	st := &stubStore{searchListed: func(context.Context, string, int) ([]store.User, error) {
		return nil, errStubUser
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/search?q=Alice_P", a.token(t, "viewer"), nil)
	reqtest.AssertProblem(t, resp, http.StatusInternalServerError, "internal")
}

// Pins that the 500 branch logs its cause via h.internalError, like every
// handler in handlers.go, not a bare problem() call with no server trace.
func TestUnitSharedSearch_HandlerErrorLog(t *testing.T) {
	_, logs := captureTelemetry(t)
	st := &stubStore{searchListed: func(context.Context, string, int) ([]store.User, error) {
		return nil, errStubUser
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/search?q=Alice_P", a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	_, attrs, ok := logs.find("handler error")
	if !ok || attrs["op"] != "shared_search" || attrs["err"] == "" {
		t.Fatalf("handler error record = %v (found %v), want op=shared_search with err", attrs, ok)
	}
}

func TestUnitSharedSearch_QueryTooLong_BadRequest(t *testing.T) {
	// maxLength: 64 on q (api/user.yaml); specval rejects 65+ bytes before
	// this handler runs (the empty stubStore proves it).
	q := strings.Repeat("a", 65)
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "GET", srv.URL+"/shared/profiles/search?q="+q, a.token(t, "viewer"), nil)
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_param")
}

func TestUnitSharedProfile_StoreError_InternalServerError(t *testing.T) {
	// A generic (non-sentinel) store error must surface as 500 internal.
	st := &stubStore{getByHandle: func(context.Context, string) (store.User, error) {
		return store.User{}, errStubUser
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/whoever", a.token(t, "viewer"), nil)
	reqtest.AssertProblem(t, resp, http.StatusInternalServerError, "internal")
}

// Pins that the 500 branch logs its cause via h.internalError, like every
// handler in handlers.go, not a bare problem() call with no server trace.
func TestUnitSharedProfile_HandlerErrorLog(t *testing.T) {
	_, logs := captureTelemetry(t)
	st := &stubStore{getByHandle: func(context.Context, string) (store.User, error) {
		return store.User{}, errStubUser
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/whoever", a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	_, attrs, ok := logs.find("handler error")
	if !ok || attrs["op"] != "shared_profile" || attrs["err"] == "" {
		t.Fatalf("handler error record = %v (found %v), want op=shared_profile with err", attrs, ok)
	}
}

func TestUnitSharedByIds_StoreError_InternalServerError(t *testing.T) {
	// A generic (non-sentinel) store error must surface as 500 internal.
	st := &stubStore{getByIDs: func(context.Context, []uuid.UUID) ([]store.User, error) {
		return nil, errStubUser
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/by-ids?ids="+uuid.New().String(), a.token(t, "viewer"), nil)
	reqtest.AssertProblem(t, resp, http.StatusInternalServerError, "internal")
}

// Pins that the 500 branch logs its cause via h.internalError, like every
// handler in handlers.go, not a bare problem() call with no server trace.
func TestUnitSharedByIds_HandlerErrorLog(t *testing.T) {
	_, logs := captureTelemetry(t)
	st := &stubStore{getByIDs: func(context.Context, []uuid.UUID) ([]store.User, error) {
		return nil, errStubUser
	}}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/shared/profiles/by-ids?ids="+uuid.New().String(), a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	_, attrs, ok := logs.find("handler error")
	if !ok || attrs["op"] != "shared_by_ids" || attrs["err"] == "" {
		t.Fatalf("handler error record = %v (found %v), want op=shared_by_ids with err", attrs, ok)
	}
}

func TestUnitSharedByIds_TooManyIds_BadRequest(t *testing.T) {
	// maxItems: 100 on ids (api/user.yaml); specval rejects 101+ entries
	// before this handler runs (the empty stubStore proves it).
	q := url.Values{}
	for range 101 {
		q.Add("ids", uuid.New().String())
	}
	srv, a := newUnitServer(t, &stubStore{})
	resp := do(t, "GET", srv.URL+"/shared/profiles/by-ids?"+q.Encode(), a.token(t, "viewer"), nil)
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_param")
}
