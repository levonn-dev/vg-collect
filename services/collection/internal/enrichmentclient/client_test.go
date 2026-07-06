package enrichmentclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// stubEnrichment answers the two consumed endpoints with contract
// shapes, recording auth headers and per-call batch sizes.
type stubEnrichment struct {
	srv *httptest.Server

	mu         sync.Mutex
	gotAuth    []string
	batchSizes []int

	knownProduct uuid.UUID
	failWith     int // when nonzero, every response is this status
}

func newStubEnrichment(t *testing.T) *stubEnrichment {
	t.Helper()
	f := &stubEnrichment{knownProduct: uuid.New()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products/{id}", f.product)
	mux.HandleFunc("POST /products/prices:batch", f.batch)
	mux.HandleFunc("POST /products/price-history:batch", f.history)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubEnrichment) record(r *http.Request) {
	f.mu.Lock()
	f.gotAuth = append(f.gotAuth, r.Header.Get("Authorization"))
	f.mu.Unlock()
}

func (f *stubEnrichment) product(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	if f.failWith != 0 {
		w.WriteHeader(f.failWith)
		return
	}
	if r.PathValue("id") != f.knownProduct.String() {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"product_not_found"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{
		"id": %q, "type": "game", "name": "Chrono Trigger",
		"platform": {"igdb_platform_id": 6, "name": "SNES"},
		"igdb": {"game_id": 1000, "name": "Chrono Trigger", "genres": ["RPG"],
		         "themes": [], "franchises": [], "similar_games": [], "companies": [],
		         "first_release_date": "1995-03-11", "fetched_at": "2026-07-01T00:00:00Z"},
		"created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"
	}`, f.knownProduct)
}

func (f *stubEnrichment) batch(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	if f.failWith != 0 {
		w.WriteHeader(f.failWith)
		return
	}
	var req struct {
		ProductIDs []uuid.UUID `json:"product_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.batchSizes = append(f.batchSizes, len(req.ProductIDs))
	f.mu.Unlock()
	prices := map[string]any{}
	for _, id := range req.ProductIDs {
		prices[id.String()] = map[string]any{"unmatched": false, "loose_cents": 1500, "cib_cents": 4200, "new_cents": 9900}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"prices": prices})
}

func (f *stubEnrichment) history(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	if f.failWith != 0 {
		w.WriteHeader(f.failWith)
		return
	}
	var req struct {
		ProductIDs []uuid.UUID `json:"product_ids"`
		Days       *int        `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.batchSizes = append(f.batchSizes, len(req.ProductIDs))
	f.mu.Unlock()
	series := map[string]any{}
	for _, id := range req.ProductIDs {
		series[id.String()] = []map[string]any{
			{"captured_at": "2026-07-01T06:00:00Z", "loose_cents": 1500},
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"series": series})
}

func newClient(t *testing.T, f *stubEnrichment) *Client {
	t.Helper()
	c, err := New(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGetProductParsesAndForwardsBearer(t *testing.T) {
	f := newStubEnrichment(t)
	c := newClient(t, f)
	p, err := c.GetProduct(t.Context(), "tok-abc", f.knownProduct)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Chrono Trigger" || p.Platform == nil || p.Platform.Name != "SNES" ||
		p.Igdb == nil || p.Igdb.GameId != 1000 || p.Igdb.FirstReleaseDate == nil ||
		p.Igdb.FirstReleaseDate.Format("2006-01-02") != "1995-03-11" {
		t.Fatalf("product: %+v", p)
	}
	if f.gotAuth[0] != "Bearer tok-abc" {
		t.Fatalf("the caller's token must ride the hop, got %q", f.gotAuth[0])
	}
}

func TestGetProductMapsErrors(t *testing.T) {
	f := newStubEnrichment(t)
	c := newClient(t, f)
	if _, err := c.GetProduct(t.Context(), "tok", uuid.New()); !errors.Is(err, ErrUnknownProduct) {
		t.Fatalf("404 must be ErrUnknownProduct, got %v", err)
	}
	if f.gotAuth[0] != "Bearer tok" {
		t.Fatalf("auth on 404: got %q", f.gotAuth[0])
	}
	f.failWith = http.StatusInternalServerError
	if _, err := c.GetProduct(t.Context(), "tok", f.knownProduct); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("500 must be ErrUnavailable, got %v", err)
	}
	if f.gotAuth[1] != "Bearer tok" {
		t.Fatalf("auth on 500: got %q", f.gotAuth[1])
	}
	f.srv.Close()
	f.failWith = 0
	if _, err := c.GetProduct(t.Context(), "tok", f.knownProduct); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("transport failure must be ErrUnavailable, got %v", err)
	}
}

func TestBatchPricesChunksAndMerges(t *testing.T) {
	f := newStubEnrichment(t)
	c := newClient(t, f)
	ids := make([]uuid.UUID, 0, 750)
	for i := 0; i < 750; i++ {
		ids = append(ids, uuid.New())
	}
	// Duplicates collapse before chunking.
	ids = append(ids, ids[0], ids[1])
	prices, err := c.BatchPrices(t.Context(), "tok", ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 750 {
		t.Fatalf("merged map: %d", len(prices))
	}
	if len(f.batchSizes) != 2 || f.batchSizes[0] != 500 || f.batchSizes[1] != 250 {
		t.Fatalf("chunking: %v (contract maxItems is 500)", f.batchSizes)
	}
	if p, ok := prices[ids[0].String()]; !ok || p.CibCents == nil || *p.CibCents != 4200 {
		t.Fatalf("price row: %+v", p)
	}
	for i, auth := range f.gotAuth {
		if auth != "Bearer tok" {
			t.Fatalf("bearer relay on call %d: got %q", i, auth)
		}
	}
	if p, ok := prices[ids[500].String()]; !ok || p.CibCents == nil || *p.CibCents != 4200 {
		t.Fatalf("second-chunk price row: %+v", p)
	}
}

func TestBatchPricesEmptyMakesNoCall(t *testing.T) {
	f := newStubEnrichment(t)
	c := newClient(t, f)
	prices, err := c.BatchPrices(t.Context(), "tok", nil)
	if err != nil || len(prices) != 0 {
		t.Fatalf("empty: %v %v", prices, err)
	}
	if len(f.gotAuth) != 0 {
		t.Fatal("no ids must mean no HTTP call")
	}
}

func TestBatchPricesFailureIsUnavailable(t *testing.T) {
	f := newStubEnrichment(t)
	f.failWith = http.StatusBadGateway
	c := newClient(t, f)
	if _, err := c.BatchPrices(t.Context(), "tok", []uuid.UUID{uuid.New()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("upstream failure must be ErrUnavailable, got %v", err)
	}
	if f.gotAuth[0] != "Bearer tok" {
		t.Fatalf("auth on batch failure: got %q", f.gotAuth[0])
	}
}

func TestPriceHistoryChunksMergesAndForwardsBearer(t *testing.T) {
	f := newStubEnrichment(t)
	c := newClient(t, f)

	// 501 unique ids + one duplicate: dedup to 501, chunked 500 + 1.
	ids := make([]uuid.UUID, 0, 502)
	for range 501 {
		ids = append(ids, uuid.New())
	}
	ids = append(ids, ids[0])

	series, err := c.PriceHistory(context.Background(), "tok-history", ids, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 501 {
		t.Fatalf("merged series size %d, want 501", len(series))
	}
	if pts := series[ids[0].String()]; len(pts) != 1 || *pts[0].LooseCents != 1500 {
		t.Fatalf("points not parsed: %+v", pts)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.batchSizes) != 2 || f.batchSizes[0] != 500 || f.batchSizes[1] != 1 {
		t.Fatalf("chunking wrong: %v", f.batchSizes)
	}
	for _, a := range f.gotAuth {
		if a != "Bearer tok-history" {
			t.Fatalf("bearer not relayed: %q", a)
		}
	}
}

func TestPriceHistoryEmptyMakesNoCall(t *testing.T) {
	f := newStubEnrichment(t)
	c := newClient(t, f)
	series, err := c.PriceHistory(context.Background(), "tok", nil, 90)
	if err != nil || len(series) != 0 {
		t.Fatalf("want empty no-call success, got %v %v", series, err)
	}
	if len(f.gotAuth) != 0 {
		t.Fatal("no request may leave the client for an empty id set")
	}
}

func TestPriceHistoryFailureIsUnavailable(t *testing.T) {
	f := newStubEnrichment(t)
	f.failWith = http.StatusInternalServerError
	c := newClient(t, f)
	_, err := c.PriceHistory(context.Background(), "tok", []uuid.UUID{uuid.New()}, 90)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}
