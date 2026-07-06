package collectionclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
			return c.GetDashboard(context.Background(), "tok")
		}, "GET", "/dashboard", http.StatusOK},
		{"GetValueHistory", func() (Result, error) {
			return c.GetValueHistory(context.Background(), "tok")
		}, "GET", "/dashboard/value-history", http.StatusOK},
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
		"GetDashboard":    func() error { _, err := c.GetDashboard(context.Background(), "tok"); return err },
		"GetValueHistory": func() error { _, err := c.GetValueHistory(context.Background(), "tok"); return err },
		"LibrarySummary":  func() error { _, err := c.LibrarySummary(context.Background(), "tok"); return err },
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
