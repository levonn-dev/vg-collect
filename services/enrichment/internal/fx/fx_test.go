package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStubServesFixtureSnapshot(t *testing.T) {
	s, err := NewStub()
	if err != nil {
		t.Fatalf("NewStub: %v", err)
	}
	r, err := s.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if r.Base != "USD" || r.Date == "" {
		t.Fatalf("snapshot base/date: %q %q", r.Base, r.Date)
	}
	if r.Rates["EUR"] != 0.5 || r.Rates["JPY"] != 150 {
		t.Fatalf("fixture rates: EUR=%v JPY=%v", r.Rates["EUR"], r.Rates["JPY"])
	}
	if _, ok := r.Rates["USD"]; ok {
		t.Fatal("USD must not appear in the rates map (it is the base)")
	}
}

// upstream returns a test server mimicking frankfurter.dev and a call
// counter; the extra "amount" field mirrors the real payload and must be ignored.
func upstream(t *testing.T, status *atomic.Int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/latest" || r.URL.Query().Get("base") != "USD" {
			t.Errorf("unexpected upstream request: %s", r.URL)
		}
		if s := status.Load(); s != 200 {
			w.WriteHeader(int(s))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"amount":1,"base":"USD","date":"2026-07-09","rates":{"EUR":0.92,"JPY":144.1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testClient(srv *httptest.Server) *Client {
	c := NewClient()
	c.baseURL = srv.URL
	c.httpc = srv.Client()
	return c
}

func TestClientFetchesAndCachesWithinTTL(t *testing.T) {
	var status atomic.Int32
	status.Store(200)
	srv, calls := upstream(t, &status)
	c := testClient(srv)

	r, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if r.Date != "2026-07-09" || r.Rates["EUR"] != 0.92 {
		t.Fatalf("decoded snapshot: %+v", r)
	}
	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatalf("second Latest: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls within TTL: got %d, want 1", calls.Load())
	}
}

func TestClientRefreshesAfterTTLAndServesStaleOnFailure(t *testing.T) {
	var status atomic.Int32
	status.Store(200)
	srv, calls := upstream(t, &status)
	c := testClient(srv)
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatalf("warm-up: %v", err)
	}

	// Past the TTL with a healthy upstream: refetch.
	now = now.Add(refreshAfter + time.Minute)
	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls after TTL: got %d, want 2", calls.Load())
	}

	// Past the TTL with a failing upstream: the stale snapshot serves.
	status.Store(500)
	now = now.Add(refreshAfter + time.Minute)
	r, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("stale-serve: %v", err)
	}
	if r.Rates["EUR"] != 0.92 {
		t.Fatalf("stale snapshot content: %+v", r)
	}
}

func TestClientColdCacheSurfacesUpstreamError(t *testing.T) {
	var status atomic.Int32
	status.Store(503)
	srv, _ := upstream(t, &status)
	c := testClient(srv)
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("cold cache with failing upstream must error")
	}
}

func TestClientRejectsImplausibleSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"base":"EUR","date":"2026-07-09","rates":{}}`))
	}))
	t.Cleanup(srv.Close)
	c := testClient(srv)
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("non-USD base and empty rates must be rejected")
	}
}
