package igdb

import (
	"context"
	"io"
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
		_, _ = w.Write([]byte(`[{"id":1011,"name":"Chrono Trigger","genres":[{"id":12,"name":"Role-playing (RPG)"}],"first_release_date":788918400,"total_rating_count":812}]`))
	})

	got, err := c.SearchGames(context.Background(), `zelda "special"`, 20)
	if err != nil || len(got) != 1 || got[0].ID != 1011 || got[0].TotalRatingCount != 812 {
		t.Fatalf("search: %+v, %v", got, err)
	}
	if _, err := c.GamesByIDs(context.Background(), []int64{1011, 1012}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[0], `search "zelda \"special\"";`) || !strings.Contains(bodies[0], "fields name,cover.image_id") ||
		!strings.Contains(bodies[0], "total_rating_count") {
		t.Fatalf("bad search body: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], "where id = (1011,1012);") {
		t.Fatalf("bad where body: %s", bodies[1])
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token must be fetched once and reused, got %d fetches", tokenCalls.Load())
	}
}

func TestClient_GamesByIDsChunksAt500(t *testing.T) {
	var bodies []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		_, _ = w.Write([]byte(`[{"id":1,"name":"A"}]`))
	})
	ids := make([]int64, 1100)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	got, err := c.GamesByIDs(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want the chunk results concatenated (one per query), got %d", len(got))
	}
	if len(bodies) != 3 {
		t.Fatalf("want 3 queries for 1100 ids, got %d", len(bodies))
	}
	for i, want := range []struct{ first, last, limit string }{
		{"(1,", ",500);", "limit 500;"},
		{"(501,", ",1000);", "limit 500;"},
		{"(1001,", ",1100);", "limit 100;"},
	} {
		if !strings.Contains(bodies[i], "where id = "+want.first) ||
			!strings.Contains(bodies[i], want.last) ||
			!strings.Contains(bodies[i], want.limit) {
			t.Fatalf("chunk %d body wrong: %.80s...%s", i, bodies[i], bodies[i][max(0, len(bodies[i])-40):])
		}
	}
}

func TestClient_GamesByIDsChunkErrorPropagates(t *testing.T) {
	var calls atomic.Int64
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	ids := make([]int64, 501)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if _, err := c.GamesByIDs(context.Background(), ids); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("second-chunk failure must surface, got %v", err)
	}
}

func TestClient_PlatformsQueryShape(t *testing.T) {
	var body string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`[{"id":4,"name":"Nintendo 64","abbreviation":"N64","generation":5,"platform_logo":{"image_id":"pl78"}}]`))
	})
	got, err := c.Platforms(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("platforms: %+v, %v", got, err)
	}
	p := got[0]
	if p.ID != 4 || p.Name != "Nintendo 64" || p.Abbreviation != "N64" || p.Generation != 5 ||
		p.PlatformLogo == nil || p.PlatformLogo.ImageID != "pl78" {
		t.Fatalf("projection decode: %+v", p)
	}
	if body != "fields name,abbreviation,generation,platform_logo.image_id; sort id asc; limit 500;" {
		t.Fatalf("bad platforms body: %s", body)
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
