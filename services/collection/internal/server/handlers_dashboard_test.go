// Tests for dashboard aggregates and value-history composition.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/server"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

func TestUnitDashboard_ComposesAndCaches(t *testing.T) {
	user := uuid.New()
	auto := store.PricingRow{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: new(uuid.New())}
	proxyTarget := uuid.New()
	// A custom entry: no own product, priced through the proxy.
	proxy := store.PricingRow{EntryID: uuid.New(), Packaging: "loose", PricingMode: "proxy", PricingProductID: &proxyTarget}
	disabled := store.PricingRow{EntryID: uuid.New(), Packaging: "cib", PricingMode: "disabled", ProductID: new(uuid.New())}
	unpriced := store.PricingRow{EntryID: uuid.New(), Packaging: "sealed", PricingMode: "auto", ProductID: new(uuid.New())}

	st := dashboardStore(user, []store.PricingRow{auto, proxy, disabled, unpriced})
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		cib, loose := int64(4200), int64(1500)
		return map[string]enrichapi.ProductPrices{
			auto.ProductID.String(): {CibCents: &cib},
			proxyTarget.String():    {LooseCents: &loose},
			// unpriced.ProductID absent: unknown to the catalog map.
		}, nil
	}}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)

	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		TotalEntries int                     `json:"total_entries"`
		ByPlatform   []struct{ Name string } `json:"by_platform"`
		Pricing      struct {
			Available       bool   `json:"available"`
			TotalValueCents *int64 `json:"total_value_cents"`
			PricedEntries   int    `json:"priced_entries"`
			UnpricedEntries int    `json:"unpriced_entries"`
			ExcludedEntries int    `json:"excluded_entries"`
		} `json:"pricing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// auto cib 4200 + proxy loose 1500; the absent product is unpriced;
	// disabled is excluded; "" platform reads Unknown.
	if got.TotalEntries != 4 || !got.Pricing.Available ||
		got.Pricing.TotalValueCents == nil || *got.Pricing.TotalValueCents != 5700 ||
		got.Pricing.PricedEntries != 2 || got.Pricing.UnpricedEntries != 1 || got.Pricing.ExcludedEntries != 1 {
		t.Fatalf("dashboard: %+v", got)
	}
	if got.ByPlatform[1].Name != "Unknown" {
		t.Fatalf("platformless label: %+v", got.ByPlatform)
	}
	// The composed body was cached for this user.
	if c.bodies[user.String()] == nil {
		t.Fatal("a healthy dashboard must be cached")
	}
}

func TestUnitDashboard_CacheHitShortCircuits(t *testing.T) {
	user := uuid.New()
	c := newStubCache()
	c.bodies[user.String()] = []byte(`{"total_entries":42}`)
	// A store or enrichment call would panic (all fields nil): the hit
	// must answer alone.
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, c)
	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"total_entries":42}` {
		t.Fatalf("cache hit: %d %s", resp.StatusCode, body)
	}
}

func TestUnitDashboard_DegradedIsNotCached(t *testing.T) {
	user := uuid.New()
	rows := []store.PricingRow{{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: new(uuid.New())}}
	st := dashboardStore(user, rows)
	enrich := &stubEnrichment{batchPrices: func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		return nil, enrichmentclient.ErrUnavailable
	}}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)
	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	var got struct {
		Pricing struct {
			Available bool `json:"available"`
		} `json:"pricing"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if resp.StatusCode != http.StatusOK || got.Pricing.Available {
		t.Fatalf("degraded dashboard: %d %+v", resp.StatusCode, got)
	}
	if c.bodies[user.String()] != nil {
		t.Fatal("a degraded dashboard must NOT be cached (it would hide recovery)")
	}
}

func TestUnitDashboard_CacheErrorsFailOpen(t *testing.T) {
	user := uuid.New()
	st := dashboardStore(user, nil)
	c := newStubCache()
	c.err = errors.New("valkey is having a moment")
	srv, a := newUnitServer(t, st, &stubEnrichment{}, c)
	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cache failure must not fail the dashboard: %d", resp.StatusCode)
	}
}

func TestUnitDashboard_FilteredComputesLiveAndSkipsCache(t *testing.T) {
	user := uuid.New()
	var gotCounts, gotRows *store.Filters
	st := &stubStore{
		dashboardCounts: func(_ context.Context, _ uuid.UUID, f store.Filters) (store.DashboardCounts, error) {
			gotCounts = &f
			return store.DashboardCounts{
				Total:      2,
				ByStatus:   map[string]int{"backlog": 2},
				ByItemType: map[string]int{"game": 2},
				ByPlatform: []store.PlatformCount{{Name: "SNES", Count: 2}},
				Spend:      []store.CurrencySpend{},
			}, nil
		},
		pricingRows: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.PricingRow, error) {
			gotRows = &f
			return []store.PricingRow{}, nil
		},
	}
	c := newStubCache()
	// A cached unfiltered dashboard must never answer a filtered
	// request (and the filtered result must not replace it).
	sentinel := []byte(`{"total_entries":999}`)
	c.bodies[user.String()] = sentinel
	srv, a := newUnitServer(t, st, &stubEnrichment{}, c)

	resp := do(t, http.MethodGet, srv.URL+"/dashboard?status=backlog&platform_id=19", a.token(t, user.String()), nil)
	var got struct {
		TotalEntries int `json:"total_entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || got.TotalEntries != 2 {
		t.Fatalf("filtered dashboard: %d %+v", resp.StatusCode, got)
	}
	if gotCounts == nil || gotRows == nil {
		t.Fatal("both aggregates must run for a filtered request")
	}
	for _, f := range []*store.Filters{gotCounts, gotRows} {
		if len(f.Statuses) != 1 || f.Statuses[0] != "backlog" ||
			len(f.PlatformIDs) != 1 || f.PlatformIDs[0] != 19 {
			t.Fatalf("filters did not reach the store: %+v", f)
		}
	}
	if string(c.bodies[user.String()]) != string(sentinel) {
		t.Fatal("a filtered result must not overwrite the unfiltered cache")
	}
}

// TestUnitDashboard_DeveloperFilterComputesLiveAndSkipsCache mirrors
// TestUnitDashboard_FilteredComputesLiveAndSkipsCache for the credit
// filters: a developer-only request must reach the store filter and
// must compute live rather than answer from the unfiltered cache,
// which only holds if Filters.Filtered() counts Developers too.
func TestUnitDashboard_DeveloperFilterComputesLiveAndSkipsCache(t *testing.T) {
	user := uuid.New()
	var gotCounts, gotRows *store.Filters
	st := &stubStore{
		dashboardCounts: func(_ context.Context, _ uuid.UUID, f store.Filters) (store.DashboardCounts, error) {
			gotCounts = &f
			return store.DashboardCounts{
				Total:      1,
				ByStatus:   map[string]int{"backlog": 1},
				ByItemType: map[string]int{"game": 1},
				ByPlatform: []store.PlatformCount{{Name: "SNES", Count: 1}},
				Spend:      []store.CurrencySpend{},
			}, nil
		},
		pricingRows: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.PricingRow, error) {
			gotRows = &f
			return []store.PricingRow{}, nil
		},
	}
	c := newStubCache()
	// A cached unfiltered dashboard must never answer a developer-only
	// request (and the live result must not replace it).
	sentinel := []byte(`{"total_entries":999}`)
	c.bodies[user.String()] = sentinel
	srv, a := newUnitServer(t, st, &stubEnrichment{}, c)

	resp := do(t, http.MethodGet, srv.URL+"/dashboard?developer=Nintendo", a.token(t, user.String()), nil)
	var got struct {
		TotalEntries int `json:"total_entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || got.TotalEntries != 1 {
		t.Fatalf("developer-filtered dashboard: %d %+v", resp.StatusCode, got)
	}
	if gotCounts == nil || gotRows == nil {
		t.Fatal("both aggregates must run live for a developer-filtered request")
	}
	for _, f := range []*store.Filters{gotCounts, gotRows} {
		if len(f.Developers) != 1 || f.Developers[0] != "Nintendo" {
			t.Fatalf("developer filter did not reach the store: %+v", f)
		}
	}
	if string(c.bodies[user.String()]) != string(sentinel) {
		t.Fatal("a developer-filtered result must not overwrite the unfiltered cache")
	}
}

// region is deliberately absent from this list: it is open-world on
// this param now, so no string value is a bad enum for it.
func TestUnitDashboard_BadFilterRejected(t *testing.T) {
	// Zero-field stubs prove the 400 answers before any store, cache,
	// or enrichment work.
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, &stubCache{})
	for _, q := range []string{"status=queued", "item_type=chair"} {
		resp := do(t, http.MethodGet, srv.URL+"/dashboard?"+q, a.token(t, uuid.NewString()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	}
}

func TestUnitLibrarySummary(t *testing.T) {
	user := uuid.New()
	rating := 8
	st := &stubStore{librarySummary: func(context.Context, uuid.UUID) ([]store.LibraryGame, error) {
		return []store.LibraryGame{
			{IGDBGameID: 2000, Rating: &rating, AllDropped: false},
			{IGDBGameID: 2001, AllDropped: true},
		}, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/library/summary", a.token(t, user.String()), nil)
	var got struct {
		Library []struct {
			IgdbGameID int64   `json:"igdb_game_id"`
			Rating     *int    `json:"rating"`
			Status     *string `json:"status"`
		} `json:"library"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Library) != 2 ||
		got.Library[0].IgdbGameID != 2000 || *got.Library[0].Rating != 8 || got.Library[0].Status != nil ||
		got.Library[1].Status == nil || *got.Library[1].Status != "dropped" {
		t.Fatalf("library: %+v", got.Library)
	}
}

func TestDashboardInvalidationThroughTheStack(t *testing.T) {
	s := newStack(t)
	productID := s.enrich.addGame("Chrono Trigger", 1500, 4200, 9900)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	dashboard := func() (int, bool) {
		t.Helper()
		resp := do(t, http.MethodGet, s.baseURL+"/dashboard", tok, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("dashboard: %d", resp.StatusCode)
		}
		var got struct {
			TotalEntries int `json:"total_entries"`
			Pricing      struct {
				Available bool `json:"available"`
			} `json:"pricing"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		return got.TotalEntries, got.Pricing.Available
	}

	if n, _ := dashboard(); n != 0 {
		t.Fatalf("empty collection: %d", n)
	}
	// A second read is served from Valkey: the enrichment fake sees no
	// second batch call.
	before := s.enrich.batchHits
	if n, _ := dashboard(); n != 0 {
		t.Fatalf("cached read: %d", n)
	}
	if s.enrich.batchHits != before {
		t.Fatalf("second read must come from the cache (batch hits %d -> %d)", before, s.enrich.batchHits)
	}

	// A mutation invalidates: the next read recomposes and sees the row.
	resp := do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if n, _ := dashboard(); n != 1 {
		t.Fatalf("post-mutation dashboard must recompose, got %d entries", n)
	}

	// Degraded pricing is never cached: recovery is visible on the very
	// next read.
	s.enrich.mu.Lock()
	s.enrich.down = true
	s.enrich.mu.Unlock()
	// Invalidate via another mutation so the next read recomposes. A
	// product-backed create cannot be used here: it fetches the product
	// from enrichment (a hard dependency), which is down too. A custom
	// (non-proxied) create has no enrichment dependency and still
	// invalidates the dashboard cache like any other mutation.
	resp = do(t, http.MethodPost, s.baseURL+"/entries", tok, jsonBody(map[string]any{
		"display_name": "Offline pickup", "item_type": "game",
		"region": "ntsc_u", "packaging": "loose",
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if _, available := dashboard(); available {
		t.Fatal("pricing must be degraded")
	}
	s.enrich.mu.Lock()
	s.enrich.down = false
	s.enrich.mu.Unlock()
	if _, available := dashboard(); !available {
		t.Fatal("recovery must be immediate (degraded results are not cached)")
	}
}

func TestComposeValueSeries_CustomStepsInAtSetAt(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	v := int64(5000)
	setAt := day("2026-06-10T06:00:00Z") // mid-day: truncates to 06-10
	customRow := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "custom", CustomValueCents: &v, CustomValueSetAt: &setAt}

	pid := uuid.New()
	loose1, loose2 := int64(1000), int64(1200)
	proxyRow := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "proxy", PricingProductID: &pid}
	series := map[string][]enrichapi.PricePoint{pid.String(): {
		{CapturedAt: day("2026-06-05T06:00:00Z"), LooseCents: &loose1},
		{CapturedAt: day("2026-06-15T06:00:00Z"), LooseCents: &loose2},
	}}

	points := server.ComposeValueSeries([]store.PricingRow{customRow, proxyRow}, series, windowStart)
	// Days: 06-05 (snapshot), 06-10 (set-at injected), 06-15 (snapshot).
	if len(points) != 3 {
		t.Fatalf("want 3 points, got %d: %+v", len(points), points)
	}
	wants := []int64{1000, 6000, 6200} // custom joins on 06-10, carry-forward everywhere
	for i, w := range wants {
		if points[i].ValueCents != w {
			t.Fatalf("point %d = %d, want %d", i, points[i].ValueCents, w)
		}
	}
}

func TestComposeValueSeries_CustomOnlyAndPreWindowClamp(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	v := int64(7700)
	old := day("2026-01-15T12:00:00Z") // long before the window
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "cib",
		PricingMode: "custom", CustomValueCents: &v, CustomValueSetAt: &old}

	points := server.ComposeValueSeries([]store.PricingRow{row}, nil, windowStart)
	if len(points) != 1 {
		t.Fatalf("want the clamped set-at day only, got %d points", len(points))
	}
	if !points[0].Date.Equal(windowStart) || points[0].ValueCents != 7700 {
		t.Fatalf("want 7700 at %v, got %+v", windowStart, points[0])
	}
}

func TestComposeValueSeries_SetAtAtMidnightBoundary(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	v := int64(100)
	exact := day("2026-06-10T00:00:00Z") // exactly midnight: contributes ON its day
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "custom", CustomValueCents: &v, CustomValueSetAt: &exact}
	points := server.ComposeValueSeries([]store.PricingRow{row}, nil, windowStart)
	if len(points) != 1 || points[0].ValueCents != 100 || !points[0].Date.Equal(exact) {
		t.Fatalf("midnight set-at must contribute on its own day, got %+v", points)
	}
}

// TestComposeValueSeries_SnapshotCapturedAtMidnightBoundary covers the
// other captured-at input (a product snapshot, not a custom set-at):
// existing snapshot fixtures all use 06:00Z, so pin the exact-midnight
// case too.
func TestComposeValueSeries_SnapshotCapturedAtMidnightBoundary(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	pid := uuid.New()
	loose := int64(1000)
	exact := day("2026-06-10T00:00:00Z") // exactly midnight, not the fixtures' usual 06:00Z
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "proxy", PricingProductID: &pid}
	series := map[string][]enrichapi.PricePoint{pid.String(): {{CapturedAt: exact, LooseCents: &loose}}}

	points := server.ComposeValueSeries([]store.PricingRow{row}, series, windowStart)
	if len(points) != 1 || !points[0].Date.Equal(exact) || points[0].ValueCents != 1000 {
		t.Fatalf("midnight snapshot must bucket into and price its own day, got %+v", points)
	}
}

// TestComposeValueSeries_SortsShuffledSeriesDefensively pins that
// composition does not depend on the caller delivering each product's
// snapshots oldest-first: a shuffled series composes identically to
// the same points pre-sorted.
func TestComposeValueSeries_SortsShuffledSeriesDefensively(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	pid := uuid.New()
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "proxy", PricingProductID: &pid}

	l1, l2, l3 := int64(1000), int64(1200), int64(1500)
	oldest := enrichapi.PricePoint{CapturedAt: day("2026-06-05T06:00:00Z"), LooseCents: &l1}
	middle := enrichapi.PricePoint{CapturedAt: day("2026-06-10T06:00:00Z"), LooseCents: &l2}
	newest := enrichapi.PricePoint{CapturedAt: day("2026-06-20T06:00:00Z"), LooseCents: &l3}

	want := server.ComposeValueSeries([]store.PricingRow{row},
		map[string][]enrichapi.PricePoint{pid.String(): {oldest, middle, newest}}, windowStart)
	got := server.ComposeValueSeries([]store.PricingRow{row},
		map[string][]enrichapi.PricePoint{pid.String(): {newest, oldest, middle}}, windowStart)

	if len(got) != len(want) {
		t.Fatalf("shuffled input produced %d points, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Date.Equal(want[i].Date.Time) || got[i].ValueCents != want[i].ValueCents {
			t.Fatalf("point %d: shuffled gave %+v, sorted-input gave %+v", i, got[i], want[i])
		}
	}
}

// The pricing rows seed entries the same way the dashboard unit tests
// do; the enrichment stub answers per-product snapshot series.
func TestUnitValueHistoryComposesCarriesForwardAndCaches(t *testing.T) {
	userID := uuid.New()
	own := uuid.New()
	proxyTarget := uuid.New()
	day := func(d int) time.Time { return time.Date(2026, time.July, d, 6, 0, 0, 0, time.UTC) }
	cents := func(v int64) *int64 { return &v }

	pricingCalls := 0
	st := &stubStore{
		pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
			pricingCalls++
			return []store.PricingRow{
				// auto + cib: follows cib_cents
				{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: &own},
				// custom + proxy + loose: follows the target's loose_cents
				{EntryID: uuid.New(), Packaging: "loose", PricingMode: "proxy", PricingProductID: &proxyTarget},
				// disabled: excluded entirely
				{EntryID: uuid.New(), Packaging: "sealed", PricingMode: "disabled", ProductID: &own},
			}, nil
		},
	}
	var gotDays, gotIDs int
	enrich := &stubEnrichment{
		priceHistory: func(_ context.Context, _ string, ids []uuid.UUID, days int) (map[string][]enrichapi.PricePoint, error) {
			gotDays, gotIDs = days, len(ids)
			return map[string][]enrichapi.PricePoint{
				// own: points on day 1 and day 3
				own.String(): {
					{CapturedAt: day(1), CibCents: cents(4200)},
					{CapturedAt: day(3), CibCents: cents(4400)},
				},
				// proxy target: first point on day 2 (contributes 0 on day 1)
				proxyTarget.String(): {
					{CapturedAt: day(2), LooseCents: cents(1500)},
				},
			}, nil
		},
	}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)
	tok := a.token(t, userID.String())

	resp := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", tok, nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if gotDays != 90 {
		t.Fatalf("window: got %d days, want 90", gotDays)
	}
	if gotIDs != 2 {
		t.Fatalf("effective ids: got %d, want 2 (disabled row excluded)", gotIDs)
	}
	var got struct {
		Available bool `json:"available"`
		Points    []struct {
			Date       string `json:"date"`
			ValueCents int64  `json:"value_cents"`
		} `json:"points"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Points) != 3 {
		t.Fatalf("want available with 3 daily points, got %s", body)
	}
	// day1: own cib 4200 (proxy has no point yet) = 4200
	// day2: own carries 4200 forward + proxy loose 1500 = 5700
	// day3: own 4400 + proxy carried 1500 = 5900
	wants := []struct {
		date  string
		cents int64
	}{{"2026-07-01", 4200}, {"2026-07-02", 5700}, {"2026-07-03", 5900}}
	for i, w := range wants {
		if got.Points[i].Date != w.date || got.Points[i].ValueCents != w.cents {
			t.Fatalf("point %d: got %s=%d, want %s=%d",
				i, got.Points[i].Date, got.Points[i].ValueCents, w.date, w.cents)
		}
	}
	// The composed body was cached under the value-history key...
	if c.vhBodies[userID.String()] == nil {
		t.Fatal("available result must be cached")
	}
	// ...and a second read serves the cache without recomposing.
	pricingCalls = 0
	resp2 := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", tok, nil)
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK || string(body2) != string(body) {
		t.Fatalf("cache hit must serve the identical body")
	}
	if pricingCalls != 0 {
		t.Fatal("cache hit must not recompose")
	}
}

func TestUnitValueHistoryDegradedIsServedNotCached(t *testing.T) {
	userID := uuid.New()
	own := uuid.New()
	st := &stubStore{
		pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
			return []store.PricingRow{{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: &own}}, nil
		},
	}
	enrich := &stubEnrichment{
		priceHistory: func(context.Context, string, []uuid.UUID, int) (map[string][]enrichapi.PricePoint, error) {
			return nil, errors.New("enrichment down")
		},
	}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)

	resp := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", a.token(t, userID.String()), nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"available":false`) {
		t.Fatalf("degraded answer must say available=false: %s", body)
	}
	if len(c.vhBodies) != 0 {
		t.Fatal("degraded answers are never cached")
	}
}

func TestUnitValueHistoryEmptyCollection(t *testing.T) {
	userID := uuid.New()
	st := &stubStore{
		pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
			return []store.PricingRow{}, nil
		},
	}
	// priceHistory deliberately nil: a call would panic the stub.
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", a.token(t, userID.String()), nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"points":[]`) || !strings.Contains(string(body), `"available":true`) {
		t.Fatalf("empty collection: want available with empty points, got %s", body)
	}
}

func TestValueHistoryInvalidationThroughTheStack(t *testing.T) {
	s := newStack(t)
	productID := s.enrich.addGame("Chrono Trigger", 1500, 4200, 9900)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	// Cold read: empty collection composes an available, empty series
	// and caches it in the real Valkey.
	resp := do(t, http.MethodGet, s.baseURL+"/dashboard/value-history", tok, nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"points":[]`) {
		t.Fatalf("cold read: %d %s", resp.StatusCode, body)
	}

	// A create must invalidate it in Valkey, so the next read sees the
	// new entry's point (cib packaging -> 4200).
	resp = do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	resp = do(t, http.MethodGet, s.baseURL+"/dashboard/value-history", tok, nil)
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"value_cents":4200`) {
		t.Fatalf("post-create read must recompose with the new entry: %s", body)
	}
}

func TestUnitDashboard_CustomOnlyNeedsNoEnrichment(t *testing.T) {
	user := uuid.New()
	rows := []store.PricingRow{
		{EntryID: uuid.New(), PricingMode: "custom", CustomValueCents: new(int64(7700)), CustomValueSetAt: new(time.Now())},
	}
	st := dashboardStore(user, rows)
	enrich := &stubEnrichment{batchPrices: func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		t.Fatal("enrichment must not be consulted for custom-only pricing")
		return nil, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		Pricing struct {
			Available       bool   `json:"available"`
			TotalValueCents *int64 `json:"total_value_cents"`
			PricedEntries   int    `json:"priced_entries"`
			UnpricedEntries int    `json:"unpriced_entries"`
			ExcludedEntries int    `json:"excluded_entries"`
		} `json:"pricing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Pricing.Available || got.Pricing.TotalValueCents == nil || *got.Pricing.TotalValueCents != 7700 ||
		got.Pricing.PricedEntries != 1 || got.Pricing.UnpricedEntries != 0 || got.Pricing.ExcludedEntries != 0 {
		t.Fatalf("dashboard: %+v", got.Pricing)
	}
}

func TestUnitDashboard_MixedCustomProxyDisabled(t *testing.T) {
	user := uuid.New()
	proxyTarget := uuid.New()
	custom := store.PricingRow{EntryID: uuid.New(), PricingMode: "custom", CustomValueCents: new(int64(7700)), CustomValueSetAt: new(time.Now())}
	proxy := store.PricingRow{EntryID: uuid.New(), Packaging: "loose", PricingMode: "proxy", PricingProductID: &proxyTarget}
	disabled := store.PricingRow{EntryID: uuid.New(), Packaging: "cib", PricingMode: "disabled", ProductID: new(uuid.New())}
	st := dashboardStore(user, []store.PricingRow{custom, proxy, disabled})
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		if len(ids) != 1 || ids[0] != proxyTarget {
			t.Fatalf("effective ids: %v", ids)
		}
		loose := int64(1000)
		return map[string]enrichapi.ProductPrices{proxyTarget.String(): {LooseCents: &loose}}, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		Pricing struct {
			TotalValueCents *int64 `json:"total_value_cents"`
			PricedEntries   int    `json:"priced_entries"`
			UnpricedEntries int    `json:"unpriced_entries"`
			ExcludedEntries int    `json:"excluded_entries"`
		} `json:"pricing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Pricing.TotalValueCents == nil || *got.Pricing.TotalValueCents != 8700 ||
		got.Pricing.PricedEntries != 2 || got.Pricing.UnpricedEntries != 0 || got.Pricing.ExcludedEntries != 1 {
		t.Fatalf("dashboard: %+v", got.Pricing)
	}
}
