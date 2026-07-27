package collectionclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/collectionapi"
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

// TestRelayMethods_RouteBearerStatusAndBody drives every Result-returning
// method against a stub that answers the status the test asks for and
// echoes a canned body, proving each method reaches the right verb+path,
// forwards the caller's own bearer, and relays the upstream status,
// content type, and body verbatim.
func TestRelayMethods_RouteBearerStatusAndBody(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	var status int
	var gotMethod, gotPath, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = w.Write([]byte(`{"echo":true}`))
		}
	})

	cases := []struct {
		name         string
		call         func() (Result, error)
		method, path string
		okStatus     int
	}{
		{"ListEntries", func() (Result, error) {
			return c.ListEntries(context.Background(), "tok", &collectionapi.ListEntriesParams{})
		}, "GET", "/entries", http.StatusOK},
		{"CreateEntry", func() (Result, error) {
			return c.CreateEntry(context.Background(), "tok", []byte(`{}`))
		}, "POST", "/entries", http.StatusCreated},
		{"GetEntry", func() (Result, error) {
			return c.GetEntry(context.Background(), "tok", id)
		}, "GET", "/entries/" + id.String(), http.StatusOK},
		{"UpdateEntry", func() (Result, error) {
			return c.UpdateEntry(context.Background(), "tok", id, []byte(`{}`))
		}, "PUT", "/entries/" + id.String(), http.StatusOK},
		{"DeleteEntry", func() (Result, error) {
			return c.DeleteEntry(context.Background(), "tok", id)
		}, "DELETE", "/entries/" + id.String(), http.StatusNoContent},
		{"ReorderEntry", func() (Result, error) {
			return c.ReorderEntry(context.Background(), "tok", id, []byte(`{}`))
		}, "POST", "/entries/" + id.String() + "/reorder", http.StatusOK},
		{"BulkUpdateEntries", func() (Result, error) {
			return c.BulkUpdateEntries(context.Background(), "tok", []byte(`{}`))
		}, "POST", "/entries/bulk-update", http.StatusOK},
		{"ListTags", func() (Result, error) {
			return c.ListTags(context.Background(), "tok")
		}, "GET", "/tags", http.StatusOK},
		{"CreateTag", func() (Result, error) {
			return c.CreateTag(context.Background(), "tok", []byte(`{}`))
		}, "POST", "/tags", http.StatusCreated},
		{"RenameTag", func() (Result, error) {
			return c.RenameTag(context.Background(), "tok", id, []byte(`{}`))
		}, "PUT", "/tags/" + id.String(), http.StatusOK},
		{"DeleteTag", func() (Result, error) {
			return c.DeleteTag(context.Background(), "tok", id)
		}, "DELETE", "/tags/" + id.String(), http.StatusNoContent},
		{"ListViews", func() (Result, error) {
			return c.ListViews(context.Background(), "tok")
		}, "GET", "/views", http.StatusOK},
		{"CreateView", func() (Result, error) {
			return c.CreateView(context.Background(), "tok", []byte(`{}`))
		}, "POST", "/views", http.StatusCreated},
		{"UpdateView", func() (Result, error) {
			return c.UpdateView(context.Background(), "tok", id, []byte(`{}`))
		}, "PUT", "/views/" + id.String(), http.StatusOK},
		{"DeleteView", func() (Result, error) {
			return c.DeleteView(context.Background(), "tok", id)
		}, "DELETE", "/views/" + id.String(), http.StatusNoContent},
		{"GetDashboard", func() (Result, error) {
			return c.GetDashboard(context.Background(), "tok", nil)
		}, "GET", "/dashboard", http.StatusOK},
		{"GetValueHistory", func() (Result, error) {
			return c.GetValueHistory(context.Background(), "tok")
		}, "GET", "/dashboard/value-history", http.StatusOK},
		{"PurgeUserData", func() (Result, error) {
			return c.PurgeUserData(context.Background(), "tok")
		}, "DELETE", "/user-data", http.StatusNoContent},
		{"CountProductReferences", func() (Result, error) {
			return c.CountProductReferences(context.Background(), "tok", id)
		}, "GET", "/admin/products/" + id.String() + "/references", http.StatusOK},
		{"CreateSubmission", func() (Result, error) {
			return c.CreateSubmission(context.Background(), "tok", id)
		}, "POST", "/entries/" + id.String() + "/submission", http.StatusCreated},
		{"GetSubmission", func() (Result, error) {
			return c.GetSubmission(context.Background(), "tok", id)
		}, "GET", "/entries/" + id.String() + "/submission", http.StatusOK},
		{"CancelSubmission", func() (Result, error) {
			return c.CancelSubmission(context.Background(), "tok", id)
		}, "DELETE", "/entries/" + id.String() + "/submission", http.StatusNoContent},
		{"AckSubmission", func() (Result, error) {
			return c.AckSubmission(context.Background(), "tok", id)
		}, "POST", "/entries/" + id.String() + "/submission/ack", http.StatusNoContent},
		{"ListSubmissions", func() (Result, error) {
			return c.ListSubmissions(context.Background(), "tok", &collectionapi.ListSubmissionsParams{})
		}, "GET", "/admin/submissions", http.StatusOK},
		{"SubmitVerdict", func() (Result, error) {
			return c.SubmitVerdict(context.Background(), "tok", id, []byte(`{}`))
		}, "POST", "/admin/submissions/" + id.String() + "/verdict", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status = tc.okStatus
			res, err := tc.call()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != tc.method || gotPath != tc.path {
				t.Fatalf("%s: routed %s %s, want %s %s", tc.name, gotMethod, gotPath, tc.method, tc.path)
			}
			if gotAuth != "Bearer tok" {
				t.Fatalf("%s: bearer = %q", tc.name, gotAuth)
			}
			if res.Status != tc.okStatus || res.ContentType != "application/json" {
				t.Fatalf("%s: relay = %+v", tc.name, res)
			}
			if tc.okStatus != http.StatusNoContent && string(res.Body) != `{"echo":true}` {
				t.Fatalf("%s: body = %s", tc.name, res.Body)
			}
		})
	}
}

// TestRelay_UndeclaredStatusIsErrUpstream proves the shared relay() gate:
// a status outside a method's declared allow-list is an infrastructure
// fault (ErrUpstream), never relayed to the browser.
func TestRelay_UndeclaredStatusIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := c.GetEntry(context.Background(), "tok", uuid.New()); !errors.Is(err, ErrUpstream) {
		t.Fatalf("undeclared status must be ErrUpstream, got %v", err)
	}
}

// TestCreateTag_Relays429ForTheUserTagCap pins that CreateTag's
// allow-list includes 429 (the per-user tag cap's status): it must
// relay verbatim, not collapse into ErrUpstream like an undeclared
// status would.
func TestCreateTag_Relays429ForTheUserTagCap(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"cap_exceeded"}`))
	})
	res, err := c.CreateTag(context.Background(), "tok", []byte(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusTooManyRequests || string(res.Body) != `{"code":"cap_exceeded"}` {
		t.Fatalf("relay: %+v", res)
	}
}

// TestLibrarySummary_DecodesTyped proves the one typed read: it decodes
// the JSON200 body instead of relaying raw bytes.
func TestLibrarySummary_DecodesTyped(t *testing.T) {
	var gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/library/summary" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"library":[{"igdb_game_id":1011,"rating":9}]}`))
	})
	lib, err := c.LibrarySummary(context.Background(), "tok-9")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-9" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if len(lib.Library) != 1 || lib.Library[0].IgdbGameId != 1011 || lib.Library[0].Rating == nil || *lib.Library[0].Rating != 9 {
		t.Fatalf("library = %+v", lib.Library)
	}
}

// TestLibrarySummary_UpstreamFailureIsErrUpstream covers the non-200
// branch of the one method that does not go through relay().
func TestLibrarySummary_UpstreamFailureIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := c.LibrarySummary(context.Background(), "tok"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

// TestTransportErrorSurfaces proves every method's own transport-error
// branch: a dead upstream surfaces as a plain wrapped error (never
// ErrUpstream, which is reserved for an upstream that actually
// answered outside the relayed contract).
// TestSharedShelf_DecodesTyped proves the shelf-page composition typed
// read decodes SharedShelf on 200 and forwards the caller's bearer.
func TestSharedShelf_DecodesTyped(t *testing.T) {
	id := uuid.New()
	ownerID := uuid.New()
	var gotPath, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + id.String() + `","name":"Retro","owner_id":"` + ownerID.String() +
			`","params":{},"slug":"retro","visibility":"listed"}`))
	})
	shelf, err := c.SharedShelf(context.Background(), "tok-3", id)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/shelves/"+id.String() {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok-3" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if shelf.Name != "Retro" || shelf.OwnerId != ownerID || string(shelf.Visibility) != "listed" {
		t.Fatalf("shelf = %+v", shelf)
	}
}

// TestSharedShelf_NotFound proves the 404 sentinel: a parsed
// problem+json 404 is ErrShelfNotFound, not a raw status check (unknown
// and private shelves are deliberately indistinguishable).
func TestSharedShelf_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"shelf_not_found"}`))
	})
	if _, err := c.SharedShelf(context.Background(), "tok", uuid.New()); !errors.Is(err, ErrShelfNotFound) {
		t.Fatalf("want ErrShelfNotFound, got %v", err)
	}
}

// TestSharedShelf_NonOKIsErrUpstream covers the fallback branch: a
// status outside 200/404 (both handled explicitly) is ErrUpstream.
func TestSharedShelf_NonOKIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := c.SharedShelf(context.Background(), "tok", uuid.New()); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

// TestSharedShelfBySlug_DecodesTypedAndForwardsParams proves the
// profile-shelf entry point resolves (owner, slug) via query params
// and decodes the same SharedShelf shape.
func TestSharedShelfBySlug_DecodesTypedAndForwardsParams(t *testing.T) {
	id, ownerID := uuid.New(), uuid.New()
	var gotPath, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + id.String() + `","name":"Retro","owner_id":"` + ownerID.String() +
			`","params":{},"slug":"retro","visibility":"listed"}`))
	})
	shelf, err := c.SharedShelfBySlug(context.Background(), "tok", ownerID, "retro")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/shelves/by-slug" {
		t.Fatalf("path = %s", gotPath)
	}
	if !strings.Contains(gotQuery, "owner_id="+ownerID.String()) || !strings.Contains(gotQuery, "slug=retro") {
		t.Fatalf("query = %s", gotQuery)
	}
	if shelf.Id != id || shelf.Slug != "retro" {
		t.Fatalf("shelf = %+v", shelf)
	}
}

// TestSharedShelfBySlug_NotFound proves the same 404 sentinel as
// SharedShelf.
func TestSharedShelfBySlug_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"shelf_not_found"}`))
	})
	if _, err := c.SharedShelfBySlug(context.Background(), "tok", uuid.New(), "missing"); !errors.Is(err, ErrShelfNotFound) {
		t.Fatalf("want ErrShelfNotFound, got %v", err)
	}
}

// TestSharedShelfBySlug_NonOKIsErrUpstream covers the fallback branch.
func TestSharedShelfBySlug_NonOKIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := c.SharedShelfBySlug(context.Background(), "tok", uuid.New(), "x"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

// TestSharedShelfEntries_RelaysStatusesAndForwardsPaging proves the
// entries-tab relay: 200 and 404 both pass through with limit/offset
// forwarded as query params, and an undeclared status is ErrUpstream.
func TestSharedShelfEntries_RelaysStatusesAndForwardsPaging(t *testing.T) {
	id := uuid.New()
	var status int
	var gotPath, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"echo":true}`))
	})
	limit, offset := 10, 20

	status = http.StatusOK
	res, err := c.SharedShelfEntries(context.Background(), "tok", id, &limit, &offset)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/shelves/"+id.String()+"/entries" {
		t.Fatalf("path = %s", gotPath)
	}
	if !strings.Contains(gotQuery, "limit=10") || !strings.Contains(gotQuery, "offset=20") {
		t.Fatalf("query = %s", gotQuery)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d", res.Status)
	}

	status = http.StatusNotFound
	res, err = c.SharedShelfEntries(context.Background(), "tok", id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusNotFound {
		t.Fatalf("status = %d", res.Status)
	}

	status = http.StatusBadRequest
	if _, err := c.SharedShelfEntries(context.Background(), "tok", id, nil, nil); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

// TestListSharedShelves_DecodesTypedAndForwardsParams proves the
// profile-page shelf list forwards owner_ids/limit/offset and decodes
// both the shelves page and the full total_count.
func TestListSharedShelves_DecodesTypedAndForwardsParams(t *testing.T) {
	shelfID, ownerA, ownerB := uuid.New(), uuid.New(), uuid.New()
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/shared/shelves" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"shelves":[{"id":"` + shelfID.String() + `","name":"Retro","owner_id":"` + ownerA.String() +
			`","slug":"retro","visibility":"listed","cover_urls":[],"entry_count":2}],"total_count":7}`))
	})
	shelves, total, err := c.ListSharedShelves(context.Background(), "tok", []uuid.UUID{ownerA, ownerB}, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "limit=5") || !strings.Contains(gotQuery, "offset=10") {
		t.Fatalf("query = %s", gotQuery)
	}
	if len(shelves) != 1 || shelves[0].Id != shelfID || shelves[0].EntryCount != 2 {
		t.Fatalf("shelves = %+v", shelves)
	}
	if total != 7 {
		t.Fatalf("total = %d", total)
	}
}

// TestListSharedShelves_NonOKIsErrUpstream covers the non-200 branch.
func TestListSharedShelves_NonOKIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, _, err := c.ListSharedShelves(context.Background(), "tok", nil, 20, 0); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

// TestSharedShelvesByIDs_DecodesTyped proves the feed/Explore hydration
// batch read decodes the shelves envelope; ids without a resolvable
// shelf are simply absent (no error, no placeholder).
func TestSharedShelvesByIDs_DecodesTyped(t *testing.T) {
	shelfID := uuid.New()
	var gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/shared/shelves/by-ids" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"shelves":[{"id":"` + shelfID.String() + `","name":"Retro","owner_id":"` + uuid.New().String() +
			`","slug":"retro","visibility":"listed","cover_urls":[],"entry_count":0}]}`))
	})
	shelves, err := c.SharedShelvesByIDs(context.Background(), "tok-4", []uuid.UUID{shelfID})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-4" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if len(shelves) != 1 || shelves[0].Id != shelfID {
		t.Fatalf("shelves = %+v", shelves)
	}
}

// TestSharedShelvesByIDs_NonOKIsErrUpstream covers the non-200 branch.
func TestSharedShelvesByIDs_NonOKIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := c.SharedShelvesByIDs(context.Background(), "tok", []uuid.UUID{uuid.New()}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

func TestTransportErrorSurfaces(t *testing.T) {
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	cases := map[string]func() error{
		"ListEntries": func() error {
			_, err := c.ListEntries(context.Background(), "tok", &collectionapi.ListEntriesParams{})
			return err
		},
		"CreateEntry":     func() error { _, err := c.CreateEntry(context.Background(), "tok", nil); return err },
		"GetEntry":        func() error { _, err := c.GetEntry(context.Background(), "tok", id); return err },
		"UpdateEntry":     func() error { _, err := c.UpdateEntry(context.Background(), "tok", id, nil); return err },
		"DeleteEntry":     func() error { _, err := c.DeleteEntry(context.Background(), "tok", id); return err },
		"ReorderEntry":    func() error { _, err := c.ReorderEntry(context.Background(), "tok", id, nil); return err },
		"ListTags":        func() error { _, err := c.ListTags(context.Background(), "tok"); return err },
		"CreateTag":       func() error { _, err := c.CreateTag(context.Background(), "tok", nil); return err },
		"RenameTag":       func() error { _, err := c.RenameTag(context.Background(), "tok", id, nil); return err },
		"DeleteTag":       func() error { _, err := c.DeleteTag(context.Background(), "tok", id); return err },
		"ListViews":       func() error { _, err := c.ListViews(context.Background(), "tok"); return err },
		"CreateView":      func() error { _, err := c.CreateView(context.Background(), "tok", nil); return err },
		"UpdateView":      func() error { _, err := c.UpdateView(context.Background(), "tok", id, nil); return err },
		"DeleteView":      func() error { _, err := c.DeleteView(context.Background(), "tok", id); return err },
		"GetDashboard":    func() error { _, err := c.GetDashboard(context.Background(), "tok", nil); return err },
		"GetValueHistory": func() error { _, err := c.GetValueHistory(context.Background(), "tok"); return err },
		"LibrarySummary":  func() error { _, err := c.LibrarySummary(context.Background(), "tok"); return err },
		"PurgeUserData":   func() error { _, err := c.PurgeUserData(context.Background(), "tok"); return err },
		"CountProductReferences": func() error {
			_, err := c.CountProductReferences(context.Background(), "tok", id)
			return err
		},
		"CreateSubmission": func() error { _, err := c.CreateSubmission(context.Background(), "tok", id); return err },
		"GetSubmission":    func() error { _, err := c.GetSubmission(context.Background(), "tok", id); return err },
		"CancelSubmission": func() error { _, err := c.CancelSubmission(context.Background(), "tok", id); return err },
		"AckSubmission":    func() error { _, err := c.AckSubmission(context.Background(), "tok", id); return err },
		"ListSubmissions": func() error {
			_, err := c.ListSubmissions(context.Background(), "tok", &collectionapi.ListSubmissionsParams{})
			return err
		},
		"SubmitVerdict": func() error { _, err := c.SubmitVerdict(context.Background(), "tok", id, nil); return err },
		"SharedShelf":   func() error { _, err := c.SharedShelf(context.Background(), "tok", id); return err },
		"SharedShelfBySlug": func() error {
			_, err := c.SharedShelfBySlug(context.Background(), "tok", id, "slug")
			return err
		},
		"SharedShelfEntries": func() error {
			_, err := c.SharedShelfEntries(context.Background(), "tok", id, nil, nil)
			return err
		},
		"ListSharedShelves": func() error {
			_, _, err := c.ListSharedShelves(context.Background(), "tok", nil, 20, 0)
			return err
		},
		"SharedShelvesByIDs": func() error {
			_, err := c.SharedShelvesByIDs(context.Background(), "tok", []uuid.UUID{id})
			return err
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("want transport error")
			}
			if errors.Is(err, ErrUpstream) {
				t.Fatal("a transport failure is not ErrUpstream")
			}
		})
	}
}
