package igdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient wires a Client at httptest servers. The token server
// counts calls; the api handler is per-test.
func newTestClient(t *testing.T, api http.HandlerFunc) (*Client, *atomic.Int64) {
	t.Helper()
	var tokenCalls atomic.Int64
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"app-token-1","expires_in":3600,"token_type":"bearer"}`))
	}))
	t.Cleanup(tokenSrv.Close)
	apiSrv := httptest.NewServer(api)
	t.Cleanup(apiSrv.Close)

	c := NewClient("test-client-id", "test-secret")
	c.apiURL = apiSrv.URL
	c.tokenURL = tokenSrv.URL
	return c, &tokenCalls
}

func TestClient_QueryShapeAndTokenReuse(t *testing.T) {
	var bodies []string
	c, tokenCalls := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		bodies = append(bodies, string(b[:n]))
		if r.Header.Get("Client-ID") != "test-client-id" || r.Header.Get("Authorization") != "Bearer app-token-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[{"id":1011,"name":"Chrono Trigger","genres":[{"id":12,"name":"Role-playing (RPG)"}],"first_release_date":788918400}]`))
	})

	got, err := c.SearchGames(context.Background(), `zelda "special"`, 20)
	if err != nil || len(got) != 1 || got[0].ID != 1011 {
		t.Fatalf("search: %+v, %v", got, err)
	}
	if _, err := c.GamesByIDs(context.Background(), []int64{1011, 1012}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[0], `search "zelda \"special\"";`) || !strings.Contains(bodies[0], "fields name,cover.image_id") {
		t.Fatalf("bad search body: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], "where id = (1011,1012);") {
		t.Fatalf("bad where body: %s", bodies[1])
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token must be fetched once and reused, got %d fetches", tokenCalls.Load())
	}
}

func TestClient_PopularGames_ExcludesClientSide(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"A","total_rating":90},{"id":2,"name":"B","total_rating":85},{"id":3,"name":"C","total_rating":80}]`))
	})
	got, err := c.PopularGames(context.Background(), []int64{12}, []int64{2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("exclusion/limit broken: %+v", got)
	}
}

func TestClient_429SurfacesError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := c.SearchGames(context.Background(), "zelda", 5); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("want 429 error, got %v", err)
	}
}

func TestClient_TimeoutSurfaces(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`[]`))
	})
	c.httpc.Timeout = 30 * time.Millisecond
	if _, err := c.SearchGames(context.Background(), "zelda", 5); err == nil {
		t.Fatal("want timeout error")
	}
}

func TestClient_MalformedJSONSurfaces(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"not":"an array`))
	})
	if _, err := c.SearchGames(context.Background(), "zelda", 5); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestClient_TokenFailureSurfaces(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	// A bare http.HandlerFunc has no router, so appending a bogus path to
	// tokenURL still reaches the same handler and still succeeds -- it
	// cannot induce a failure. Point at a dedicated token fake that always
	// fails instead.
	failingTokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(failingTokenSrv.Close)
	c.tokenURL = failingTokenSrv.URL

	if _, err := c.SearchGames(context.Background(), "zelda", 5); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("want token error, got %v", err)
	}
}
