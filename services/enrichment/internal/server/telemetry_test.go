// Telemetry emission tests: the domain instruments registered in New
// (attribute derivation and outcome classification at each emission
// site) and the refresh walk's started line. Metrics are read back
// through an SDK manual reader swapped in as the global provider; the
// package's tests never run parallel, so the swap cannot bleed across
// tests.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/store"
)

// enrichmentMeter is the meter instruments register under (the module
// path, per convention).
const enrichmentMeter = "github.com/levonn-dev/vg-collect/services/enrichment"

// newTestMeter swaps the global meter provider for one draining into
// a manual reader, restored on cleanup. Handlers built after this
// call register real, readable instruments.
func newTestMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetMeterProvider(prev)
		_ = mp.Shutdown(context.Background())
	})
	return reader
}

// metricByName collects and returns the named instrument's data from
// the service meter scope (zero value when nothing was recorded).
func metricByName(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != enrichmentMeter {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	return metricdata.Metrics{}
}

func hasAttrs(set attribute.Set, want []attribute.KeyValue) bool {
	for _, kv := range want {
		v, ok := set.Value(kv.Key)
		if !ok || v.String() != kv.Value.String() {
			return false
		}
	}
	return true
}

// counterSum sums the named counter's points carrying all of want
// (every point when want is empty).
func counterSum(t *testing.T, reader *sdkmetric.ManualReader, name string, want ...attribute.KeyValue) int64 {
	t.Helper()
	m := metricByName(t, reader, name)
	if m.Data == nil {
		return 0
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s: not an int64 sum: %T", name, m.Data)
	}
	var total int64
	for _, dp := range sum.DataPoints {
		if hasAttrs(dp.Attributes, want) {
			total += dp.Value
		}
	}
	return total
}

// histogramPoint returns the named histogram's point carrying all of
// want (zero value, count 0, when absent).
func histogramPoint(t *testing.T, reader *sdkmetric.ManualReader, name string, want ...attribute.KeyValue) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	m := metricByName(t, reader, name)
	if m.Data == nil {
		return metricdata.HistogramDataPoint[float64]{}
	}
	hist, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("%s: not a float64 histogram: %T", name, m.Data)
	}
	for _, dp := range hist.DataPoints {
		if hasAttrs(dp.Attributes, want) {
			return dp
		}
	}
	return metricdata.HistogramDataPoint[float64]{}
}

// Every fail-open routes through the one helper, which stamps the
// call site's op on the counter next to the existing warn.
func TestUnitTelemetry_CacheFailOpenCountsPerOp(t *testing.T) {
	reader := newTestMeter(t)
	env := newAuthEnv(t)
	c := newStubCache()
	c.err = errors.New("valkey down")
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger"}}, nil
	}}
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, c)

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("fail-open search must still answer: %d", rec.Code)
	}

	const name = "vg.enrichment.cache.fail_open"
	for _, op := range []string{"search_get", "search_put"} {
		if got := counterSum(t, reader, name, attribute.String("op", op)); got != 1 {
			t.Fatalf("cache.fail_open{op=%s} = %d, want 1", op, got)
		}
	}
	if total := counterSum(t, reader, name); total != 2 {
		t.Fatalf("cache.fail_open total = %d, want 2", total)
	}
}

// One count per answered SearchCatalog request, labeled by kind and
// by which source produced the answer.
func TestUnitTelemetry_SearchAnswersByKindAndSource(t *testing.T) {
	reader := newTestMeter(t)
	env := newAuthEnv(t)
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger"}}, nil
	}}
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())
	tok := env.token(t, "u1", []string{"user"})

	// Cold cache: the provider answers.
	if rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("provider search: %d", rec.Code)
	}
	// Same query again: the cache answers.
	if rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("cached search: %d", rec.Code)
	}
	// Provider down on a cold key: the degraded local match answers.
	games.searchGames = func(context.Context, string, int) ([]igdb.Game, error) { return nil, errors.New("igdb down") }
	st.searchByName = func(context.Context, string, int) ([]store.Product, error) { return nil, nil }
	if rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=other", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("degraded search: %d", rec.Code)
	}

	const name = "vg.enrichment.search.requests"
	for _, source := range []string{"provider", "cache", "degraded"} {
		got := counterSum(t, reader, name, attribute.String("kind", "game"), attribute.String("source", source))
		if got != 1 {
			t.Fatalf("search.requests{kind=game,source=%s} = %d, want 1", source, got)
		}
	}
	if total := counterSum(t, reader, name); total != 3 {
		t.Fatalf("one count per answer: total = %d, want 3", total)
	}
}

// The resolve-side cached listing search feeds the auto-matcher, not
// a user: it must never count as a search answer.
func TestUnitTelemetry_ResolveSideListingSearchDoesNotCount(t *testing.T) {
	reader := newTestMeter(t)
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return []pricecharting.Product{{ID: 5005, Name: "Super Mario 64", ConsoleName: "Nintendo 64"}}, nil
	}}
	h := newUnitHandlers(&stubStore{}, nil, prices, newStubCache())

	if meta := h.autoMatchGame(context.Background(), "resolve", "Super Mario 64", "", "Nintendo 64"); meta == nil {
		t.Fatal("the auto-match must land, proving the listing search ran")
	}

	if got := counterSum(t, reader, "vg.enrichment.search.requests"); got != 0 {
		t.Fatalf("resolve-side listing search counted %d search answers, want 0", got)
	}
}

// autoMatchGame classifies every attempt exactly once - matched,
// below_threshold or provider_down - with the caller's flow as source.
func TestUnitTelemetry_MatchOutcomesBySourceAndOutcome(t *testing.T) {
	reader := newTestMeter(t)
	loose := int64(3500)
	prices := &stubPrices{search: func(_ context.Context, q string) ([]pricecharting.Product, error) {
		switch q {
		case "Super Mario 64":
			return []pricecharting.Product{{ID: 5005, Name: "Super Mario 64", ConsoleName: "Nintendo 64", LoosePriceCents: &loose}}, nil
		case "Totally Unknown Homebrew":
			return nil, nil
		default:
			return nil, errors.New("pricecharting down")
		}
	}}
	h := newUnitHandlers(&stubStore{}, nil, prices, newStubCache())
	ctx := context.Background()

	if meta := h.autoMatchGame(ctx, "resolve", "Super Mario 64", "", "Nintendo 64"); meta == nil {
		t.Fatal("want a landed match")
	}
	if meta := h.autoMatchGame(ctx, "rematch", "Totally Unknown Homebrew", "", "Super Nintendo"); meta != nil {
		t.Fatalf("want an auto-miss, got %+v", meta)
	}
	if meta := h.autoMatchGame(ctx, "rematch", "Provider Down Game", "", "Nintendo 64"); meta != nil {
		t.Fatalf("want nil on a dead provider, got %+v", meta)
	}

	const name = "vg.enrichment.match.outcomes"
	for _, want := range []struct{ source, outcome string }{
		{"resolve", "matched"},
		{"rematch", "below_threshold"},
		{"rematch", "provider_down"},
	} {
		got := counterSum(t, reader, name,
			attribute.String("source", want.source), attribute.String("outcome", want.outcome))
		if got != 1 {
			t.Fatalf("match.outcomes{source=%s,outcome=%s} = %d, want 1", want.source, want.outcome, got)
		}
	}
	if total := counterSum(t, reader, name); total != 3 {
		t.Fatalf("one outcome per attempt: total = %d, want 3", total)
	}
}

// Price-walk items: ok means price written and snapshot appended; a
// fetch, write or snapshot failure counts failed - exactly one
// outcome per walked product.
func TestUnitTelemetry_RefreshItemsPrices(t *testing.T) {
	reader := newTestMeter(t)
	prods := []store.Product{
		{ID: "p-ok", PriceCharting: &store.PCMeta{PCProductID: 1}},
		{ID: "p-fetch-fail", PriceCharting: &store.PCMeta{PCProductID: 2}},
		{ID: "p-snap-fail", PriceCharting: &store.PCMeta{PCProductID: 3}},
		{ID: "p-write-fail", PriceCharting: &store.PCMeta{PCProductID: 4}},
	}
	st := &stubStore{
		listPriced: func(context.Context) ([]store.Product, error) { return prods, nil },
		setCurrentPrices: func(_ context.Context, id string, _ store.PriceQuote, _ time.Time) error {
			if id == "p-write-fail" {
				return errors.New("mongo down")
			}
			return nil
		},
		appendSnapshot: func(_ context.Context, s store.Snapshot) error {
			if s.ProductID == "p-snap-fail" {
				return errors.New("mongo down")
			}
			return nil
		},
	}
	prices := &stubPrices{product: func(_ context.Context, id int64) (pricecharting.Product, error) {
		if id == 2 {
			return pricecharting.Product{}, errors.New("pricecharting down")
		}
		return pricecharting.Product{ID: id, Name: "P", ConsoleName: "C"}, nil
	}}
	h := newUnitHandlers(st, nil, prices, newStubCache())

	h.runRefresh(context.Background())

	const name = "vg.enrichment.refresh.items"
	if got := counterSum(t, reader, name, attribute.String("step", "prices"), attribute.String("outcome", "ok")); got != 1 {
		t.Fatalf("prices ok = %d, want 1", got)
	}
	if got := counterSum(t, reader, name, attribute.String("step", "prices"), attribute.String("outcome", "failed")); got != 3 {
		t.Fatalf("prices failed = %d, want 3", got)
	}
}

// Re-match items: ok only when the mapping lands; platform-less,
// auto-miss and identity-taken products are skipped; a store fault is
// failed.
func TestUnitTelemetry_RefreshItemsRematch(t *testing.T) {
	reader := newTestMeter(t)
	loose := int64(3500)
	prices := &stubPrices{search: func(_ context.Context, q string) ([]pricecharting.Product, error) {
		switch q {
		case "Super Mario 64":
			return []pricecharting.Product{{ID: 5005, Name: "Super Mario 64", ConsoleName: "Nintendo 64", LoosePriceCents: &loose}}, nil
		case "Chrono Trigger":
			return []pricecharting.Product{{ID: 5011, Name: "Chrono Trigger", ConsoleName: "Super Nintendo", LoosePriceCents: &loose}}, nil
		default:
			return nil, nil
		}
	}}
	n64 := &store.Platform{IGDBID: 4, Name: "Nintendo 64"}
	snes := &store.Platform{IGDBID: 19, Name: "Super Nintendo Entertainment System"}
	work := []store.Product{
		{ID: "p-landed", Type: "game", Name: "Super Mario 64", Platform: n64},
		{ID: "p-store-fail", Type: "game", Name: "Super Mario 64", Platform: n64},
		{ID: "p-raced", Type: "game", Name: "Super Mario 64", Platform: n64},
		{ID: "p-taken", Type: "game", Name: "Chrono Trigger", Platform: snes},
		{ID: "p-miss", Type: "game", Name: "Totally Unknown Homebrew", Platform: snes},
		{ID: "p-no-platform", Type: "game", Name: "Platformless"},
	}
	st := &stubStore{
		listUnmatchedGames: func(context.Context, int) ([]store.Product, error) { return work, nil },
		setPriceChartingIfMissing: func(_ context.Context, id string, _ *store.PCMeta) (bool, error) {
			switch id {
			case "p-taken":
				return false, store.ErrIdentityTaken
			case "p-store-fail":
				return false, errors.New("mongo down")
			case "p-raced":
				return false, nil // matched by something else since the list read
			}
			return true, nil
		},
		appendSnapshot: func(context.Context, store.Snapshot) error { return nil },
	}
	h := newUnitHandlers(st, nil, prices, newStubCache())

	h.runRematch(context.Background())

	const name = "vg.enrichment.refresh.items"
	for _, want := range []struct {
		outcome string
		n       int64
	}{{"ok", 1}, {"failed", 1}, {"skipped", 4}} {
		got := counterSum(t, reader, name, attribute.String("step", "rematch"), attribute.String("outcome", want.outcome))
		if got != want.n {
			t.Fatalf("rematch %s = %d, want %d", want.outcome, got, want.n)
		}
	}
}

// Reprojection items: a rebuilt projection is ok, the diff gate and
// an unusable raw are skipped, a write fault is failed.
func TestUnitTelemetry_RefreshItemsReprojection(t *testing.T) {
	reader := newTestMeter(t)
	fetchedAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	gameOne := igdb.Game{ID: 1, Name: "Game One", ReleaseDates: []igdb.ReleaseDate{}}
	gameTwo := igdb.Game{ID: 2, Name: "Game Two", ReleaseDates: []igdb.ReleaseDate{}}
	gameFour := igdb.Game{ID: 4, Name: "Game Four", ReleaseDates: []igdb.ReleaseDate{}}
	sameMeta := store.NewIGDBMeta(gameTwo, 0, fetchedAt)
	prods := []store.Product{
		{ID: "p-rebuilt", Type: "game", IGDB: &store.IGDBMeta{GameID: 1}},
		{ID: "p-unchanged", Type: "game", IGDB: &sameMeta},
		{ID: "p-no-raw", Type: "game", IGDB: &store.IGDBMeta{GameID: 3}},
		{ID: "p-write-fail", Type: "game", IGDB: &store.IGDBMeta{GameID: 4}},
		{ID: "p-nil-projection", Type: "game"}, // defensive branch: filter matched, projection gone
	}
	st := &stubStore{
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return prods, nil },
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) {
			return []store.RawGame{
				{GameID: 1, Game: gameOne, FetchedAt: fetchedAt},
				{GameID: 2, Game: gameTwo, FetchedAt: fetchedAt},
				{GameID: 4, Game: gameFour, FetchedAt: fetchedAt},
			}, nil
		},
		upsertRaw: func(context.Context, []igdb.Game, time.Time) error { return nil },
		setIGDB: func(_ context.Context, id string, _ store.IGDBMeta) error {
			if id == "p-write-fail" {
				return errors.New("mongo down")
			}
			return nil
		},
	}
	// Game 3 has no raw; the batched refetch comes back empty (the
	// provider no longer knows it), leaving that product unusable.
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())

	h.runReprojection(context.Background())

	const name = "vg.enrichment.refresh.items"
	for _, want := range []struct {
		outcome string
		n       int64
	}{{"ok", 1}, {"failed", 1}, {"skipped", 3}} {
		got := counterSum(t, reader, name, attribute.String("step", "reprojection"), attribute.String("outcome", want.outcome))
		if got != want.n {
			t.Fatalf("reprojection %s = %d, want %d", want.outcome, got, want.n)
		}
	}
}

// Sweep items: candidates stashed is flagged, swept clean is ok, and
// a provider fault (either provider) or a store fault is failed.
func TestUnitTelemetry_RefreshItemsSweep(t *testing.T) {
	reader := newTestMeter(t)
	comm := []store.Product{
		{ID: "c-flagged", Type: "game", Name: "Chrono Trigger", Origin: "community"},
		{ID: "c-clean", Type: "game", Name: "Totally Unknown Homebrew", Origin: "community"},
		{ID: "c-igdb-fail", Type: "game", Name: "IGDB Down Game", Origin: "community"},
		{ID: "c-store-fail", Type: "game", Name: "Another Unknown Homebrew", Origin: "community"},
		{ID: "c-pc-fail", Type: "console", Name: "Nintendo 64 Console", Origin: "community"},
	}
	st := &stubStore{
		listCommunityProducts: func(context.Context) ([]store.Product, error) { return comm, nil },
		replacePromoteCandidates: func(_ context.Context, id string, _ []store.PromoteCandidate) error {
			if id == "c-store-fail" {
				return errors.New("mongo down")
			}
			return nil
		},
	}
	games := &stubGames{searchGames: func(_ context.Context, q string, _ int) ([]igdb.Game, error) {
		switch q {
		case "Chrono Trigger":
			return []igdb.Game{{ID: 1011, Name: "Chrono Trigger"}}, nil
		case "IGDB Down Game":
			return nil, errors.New("igdb down")
		default:
			return nil, nil
		}
	}}
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return nil, errors.New("pricecharting down")
	}}
	h := newUnitHandlers(st, games, prices, newStubCache())

	h.runCandidateSweep(context.Background())

	const name = "vg.enrichment.refresh.items"
	for _, want := range []struct {
		outcome string
		n       int64
	}{{"flagged", 1}, {"ok", 1}, {"failed", 3}} {
		got := counterSum(t, reader, name, attribute.String("step", "sweep"), attribute.String("outcome", want.outcome))
		if got != want.n {
			t.Fatalf("sweep %s = %d, want %d", want.outcome, got, want.n)
		}
	}
}

// Each walk step records its elapsed seconds exactly once per run,
// into the explicit walk buckets (the SDK defaults top out at 10s).
func TestUnitTelemetry_WalkDurationPerStep(t *testing.T) {
	reader := newTestMeter(t)
	st := &stubStore{
		listPriced:            func(context.Context) ([]store.Product, error) { return nil, nil },
		listUnmatchedGames:    func(context.Context, int) ([]store.Product, error) { return nil, nil },
		listIGDBProducts:      func(context.Context) ([]store.Product, error) { return nil, nil },
		listCommunityProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	ctx := context.Background()

	h.runRefresh(ctx)
	h.runRematch(ctx)
	h.runReprojection(ctx)
	h.runCandidateSweep(ctx)

	const name = "vg.enrichment.refresh.walk.duration"
	wantBounds := []float64{1, 5, 15, 60, 300, 900, 1800}
	for _, step := range []string{"prices", "rematch", "reprojection", "sweep"} {
		dp := histogramPoint(t, reader, name, attribute.String("step", step))
		if dp.Count != 1 {
			t.Fatalf("walk.duration{step=%s} count = %d, want 1", step, dp.Count)
		}
		if !slices.Equal(dp.Bounds, wantBounds) {
			t.Fatalf("walk.duration{step=%s} bounds = %v, want %v", step, dp.Bounds, wantBounds)
		}
	}
}

// A step that aborts on its list read still records: the count series
// is the walk-happened signal for the stalled-walk alert, so an abort
// must not go silent.
func TestUnitTelemetry_WalkDurationRecordedOnAbort(t *testing.T) {
	reader := newTestMeter(t)
	st := &stubStore{listPriced: func(context.Context) ([]store.Product, error) { return nil, errors.New("mongo down") }}
	h := newUnitHandlers(st, nil, nil, newStubCache())

	h.runRefresh(context.Background())

	dp := histogramPoint(t, reader, "vg.enrichment.refresh.walk.duration", attribute.String("step", "prices"))
	if dp.Count != 1 {
		t.Fatalf("aborted walk step must still record its duration: count = %d, want 1", dp.Count)
	}
}

// lockedBuffer serializes writes: the detached walk logs from its own
// goroutine while the test reads the captured output.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The started line lands right after the guard is won and names which
// trigger fired; it pairs with the per-step finished summaries for
// hung-walk triage.
func TestUnitTelemetry_RefreshWalkStartedLogsTrigger(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{
		listPriced:            func(context.Context) ([]store.Product, error) { return nil, nil },
		listUnmatchedGames:    func(context.Context, int) ([]store.Product, error) { return nil, nil },
		listIGDBProducts:      func(context.Context) ([]store.Product, error) { return nil, nil },
		listCommunityProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
	}
	buf := &lockedBuffer{}
	h := New(st, nil, nil, &stubFX{}, newStubCache(), Options{
		SearchCacheTTL:         time.Hour,
		ProductCacheTTL:        time.Minute,
		IGDBRefreshAfter:       720 * time.Hour,
		InternalRefreshSecrets: []string{testInternalToken},
		Logger:                 slog.New(slog.NewJSONHandler(buf, nil)),
	})

	rec := serveInternal(t, h, env, testInternalToken)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("internal trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	logged := buf.String()
	if !strings.Contains(logged, `"msg":"refresh walk started"`) || !strings.Contains(logged, `"trigger":"internal"`) {
		t.Fatalf("internal started line missing: %s", logged)
	}

	rec = serveUnit(t, h, env, http.MethodPost, "/admin/refresh", env.token(t, "a1", []string{"admin"}), nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admin trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	if !strings.Contains(buf.String(), `"trigger":"admin"`) {
		t.Fatalf("admin started line missing: %s", buf.String())
	}
}

// stubErrMeterProvider hands out a meter that refuses every
// registration this service performs; the noop embeds satisfy the
// rest of the interfaces.
type stubErrMeterProvider struct{ noop.MeterProvider }

func (stubErrMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return stubErrMeter{}
}

type stubErrMeter struct{ noop.Meter }

func (stubErrMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("registration refused")
}

func (stubErrMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, errors.New("registration refused")
}

// Registration is best-effort: a refused instrument is logged once
// and every emission helper tolerates the nil instead of panicking.
func TestUnitTelemetry_RegistrationFailureIsBestEffort(t *testing.T) {
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(stubErrMeterProvider{})
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	buf := &lockedBuffer{}
	h := New(&stubStore{}, nil, nil, &stubFX{}, newStubCache(), Options{
		Logger: slog.New(slog.NewJSONHandler(buf, nil)),
	})

	ctx := context.Background()
	h.failOpen(ctx, "search_get", errors.New("valkey down"))
	h.countSearch(ctx, "game", "provider")
	h.countMatch(ctx, "resolve", "matched")
	h.countRefreshItem(ctx, "prices", "ok")
	h.recordWalkDuration(ctx, "prices", 1.5)

	logged := buf.String()
	for _, want := range []string{
		"cache fail-open counter unavailable",
		"search requests counter unavailable",
		"match outcomes counter unavailable",
		"refresh items counter unavailable",
		"walk duration histogram unavailable",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("missing registration-failure log %q in: %s", want, logged)
		}
	}
}
