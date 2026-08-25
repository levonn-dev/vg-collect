package enrichmentclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	return reqtest.NewTestClient(t, h, func(baseURL string) *Client {
		c, err := New(baseURL)
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
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

// TestAdminAndFxRelays_RouteBearerStatusAndBody checks verb, path, bearer,
// and relay fidelity across the fx and admin relay methods.
func TestAdminAndFxRelays_RouteBearerStatusAndBody(t *testing.T) {
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
		{"FX", func() (Result, error) {
			return c.FX(context.Background(), "tok")
		}, "GET", "/fx/latest", http.StatusOK},
		{"ListPlatforms", func() (Result, error) {
			return c.ListPlatforms(context.Background(), "tok")
		}, "GET", "/platforms", http.StatusOK},
		{"UnmatchedProducts", func() (Result, error) {
			return c.UnmatchedProducts(context.Background(), "tok", &enrichapi.ListUnmatchedProductsParams{})
		}, "GET", "/admin/products/unmatched", http.StatusOK},
		{"CommunityProducts", func() (Result, error) {
			return c.CommunityProducts(context.Background(), "tok", &enrichapi.ListCommunityProductsParams{})
		}, "GET", "/admin/products/community", http.StatusOK},
		{"SetProductMapping", func() (Result, error) {
			return c.SetProductMapping(context.Background(), "tok", id, []byte(`{}`))
		}, "PUT", "/admin/products/" + id.String() + "/pricecharting", http.StatusOK},
		{"DeleteProduct", func() (Result, error) {
			return c.DeleteProduct(context.Background(), "tok", id)
		}, "DELETE", "/admin/products/" + id.String(), http.StatusNoContent},
		{"TriggerRefresh", func() (Result, error) {
			return c.TriggerRefresh(context.Background(), "tok")
		}, "POST", "/admin/refresh", http.StatusAccepted},
		{"CreateCommunityProduct", func() (Result, error) {
			return c.CreateCommunityProduct(context.Background(), "tok", []byte(`{}`))
		}, "POST", "/admin/products", http.StatusCreated},
		{"PromoteProduct", func() (Result, error) {
			return c.PromoteProduct(context.Background(), "tok", id, []byte(`{}`))
		}, "POST", "/admin/products/" + id.String() + "/promote", http.StatusOK},
		{"PromoteCandidates", func() (Result, error) {
			return c.PromoteCandidates(context.Background(), "tok", &enrichapi.ListPromoteCandidatesParams{})
		}, "GET", "/admin/products/promote-candidates", http.StatusOK},
		{"DismissPromoteCandidate", func() (Result, error) {
			return c.DismissPromoteCandidate(context.Background(), "tok", id, []byte(`{}`))
		}, "POST", "/admin/products/" + id.String() + "/promote-candidates/dismiss", http.StatusNoContent},
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

// TestAdminRelays_ForbiddenIsRelayed: enrichment enforces the admin role, so 403 relays as a user answer.
func TestAdminRelays_ForbiddenIsRelayed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":403,"code":"forbidden"}`))
	})
	res, err := c.TriggerRefresh(context.Background(), "tok")
	if err != nil || res.Status != http.StatusForbidden {
		t.Fatalf("admin 403 must relay: %+v, %v", res, err)
	}
}

func TestScore_RelaysBodyAndDegradedFlag(t *testing.T) {
	var gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"degraded":true,"recommendations":[]}`))
	})
	body, degraded, err := c.Score(context.Background(), "tok-123", enrichapi.ScoreRequest{})
	if err != nil || !degraded || string(body) != `{"degraded":true,"recommendations":[]}` {
		t.Fatalf("body=%s degraded=%v err=%v", body, degraded, err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("bearer: %q", gotAuth)
	}
}

func TestScore_UndeclaredStatusIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, _, err := c.Score(context.Background(), "tok", enrichapi.ScoreRequest{}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

func TestScore_TransportErrorSurfaces(t *testing.T) {
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Score(context.Background(), "tok", enrichapi.ScoreRequest{}); err == nil {
		t.Fatal("want transport error")
	}
}

func TestTransportErrorSurfaces(t *testing.T) {
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	cases := map[string]func() error{
		"Search": func() error { _, err := c.Search(context.Background(), "tok", "game", "zelda"); return err },
		"FX":     func() error { _, err := c.FX(context.Background(), "tok"); return err },
		"ListPlatforms": func() error {
			_, err := c.ListPlatforms(context.Background(), "tok")
			return err
		},
		"UnmatchedProducts": func() error {
			_, err := c.UnmatchedProducts(context.Background(), "tok", &enrichapi.ListUnmatchedProductsParams{})
			return err
		},
		"CommunityProducts": func() error {
			_, err := c.CommunityProducts(context.Background(), "tok", &enrichapi.ListCommunityProductsParams{})
			return err
		},
		"SetProductMapping": func() error {
			_, err := c.SetProductMapping(context.Background(), "tok", id, nil)
			return err
		},
		"DeleteProduct":  func() error { _, err := c.DeleteProduct(context.Background(), "tok", id); return err },
		"TriggerRefresh": func() error { _, err := c.TriggerRefresh(context.Background(), "tok"); return err },
		"CreateCommunityProduct": func() error {
			_, err := c.CreateCommunityProduct(context.Background(), "tok", nil)
			return err
		},
		"PromoteProduct": func() error { _, err := c.PromoteProduct(context.Background(), "tok", id, nil); return err },
		"PromoteCandidates": func() error {
			_, err := c.PromoteCandidates(context.Background(), "tok", &enrichapi.ListPromoteCandidatesParams{})
			return err
		},
		"DismissPromoteCandidate": func() error {
			_, err := c.DismissPromoteCandidate(context.Background(), "tok", id, nil)
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
