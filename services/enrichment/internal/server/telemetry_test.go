// Telemetry emission tests: the domain instruments registered in New
// and the catalog refresh's started line. Metrics read back through
// an SDK manual reader swapped in globally; tests never run parallel.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/levonn-dev/vgkeep/libs/go/metrictest"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// Every fail-open routes through the one helper, which stamps the
// call site's op on the counter next to the existing warn.
func TestUnitTelemetry_CacheFailOpenCountsPerOp(t *testing.T) {
	reader := metrictest.Install(t)
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
		if got := metrictest.Int64Sum(t, reader, name, attribute.String("op", op)); got != 1 {
			t.Fatalf("cache.fail_open{op=%s} = %d, want 1", op, got)
		}
	}
	if total := metrictest.Int64Sum(t, reader, name); total != 2 {
		t.Fatalf("cache.fail_open total = %d, want 2", total)
	}
}

// One count per answered SearchCatalog request, labeled by kind and
// by which source produced the answer.
func TestUnitTelemetry_SearchAnswersByKindAndSource(t *testing.T) {
	reader := metrictest.Install(t)
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
	st.searchByName = func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }
	if rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=other", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("degraded search: %d", rec.Code)
	}

	const name = "vg.enrichment.search.requests"
	for _, source := range []string{"provider", "cache", "degraded"} {
		got := metrictest.Int64Sum(t, reader, name, attribute.String("kind", "game"), attribute.String("source", source))
		if got != 1 {
			t.Fatalf("search.requests{kind=game,source=%s} = %d, want 1", source, got)
		}
	}
	if total := metrictest.Int64Sum(t, reader, name); total != 3 {
		t.Fatalf("one count per answer: total = %d, want 3", total)
	}
}

// The localization leg counts exactly one outcome per non-latin
// query: merged, empty, or error. Each scenario uses a distinct query
// string so the search cache can't turn a later call into a no-op hit.
func TestUnitTelemetry_LocalizationLegOutcomes(t *testing.T) {
	reader := metrictest.Install(t)
	env := newAuthEnv(t)
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	games := &stubGames{
		searchGames: func(context.Context, string, int) ([]igdb.Game, error) { return nil, nil },
		gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
			return []igdb.Game{{ID: 1001, Name: "The Legend of Zelda: Ocarina of Time"}}, nil
		},
	}
	h := newUnitHandlers(st, games, nil, newStubCache())
	tok := env.token(t, "u1", []string{"user"})
	get := func(q string) *httptest.ResponseRecorder {
		return serveUnit(t, h, env, http.MethodGet, "/search?type=game&q="+url.QueryEscape(q), tok, nil)
	}

	// merged: the leg finds an id absent from the (empty) primary results.
	games.searchLocalizations = func(context.Context, string, int) ([]int64, error) { return []int64{1001}, nil }
	if rec := get("ゼルダの伝説1"); rec.Code != http.StatusOK {
		t.Fatalf("merged: %d", rec.Code)
	}

	// empty: the leg finds nothing.
	games.searchLocalizations = func(context.Context, string, int) ([]int64, error) { return nil, nil }
	if rec := get("ゼルダの伝説2"); rec.Code != http.StatusOK {
		t.Fatalf("empty: %d", rec.Code)
	}

	// error: the leg itself fails.
	games.searchLocalizations = func(context.Context, string, int) ([]int64, error) { return nil, errors.New("igdb down") }
	if rec := get("ゼルダの伝説3"); rec.Code != http.StatusOK {
		t.Fatalf("error: %d", rec.Code)
	}

	const name = "vg.enrichment.search.localization_leg"
	for _, outcome := range []string{"merged", "empty", "error"} {
		if got := metrictest.Int64Sum(t, reader, name, attribute.String("outcome", outcome)); got != 1 {
			t.Fatalf("localization_leg{outcome=%s} = %d, want 1", outcome, got)
		}
	}
}

// Pins the merged outcome's other branch: every leg id is already
// present in the primary results, so no GamesByIDs fetch is needed
// (the nil gamesByIDs field would panic if the code fetched anyway).
func TestUnitTelemetry_LocalizationLegMergedSkipsFetchWhenNothingMissing(t *testing.T) {
	reader := metrictest.Install(t)
	env := newAuthEnv(t)
	games := &stubGames{
		searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
			return []igdb.Game{{ID: 1001, Name: "The Legend of Zelda: Ocarina of Time"}}, nil
		},
		searchLocalizations: func(context.Context, string, int) ([]int64, error) { return []int64{1001}, nil },
	}
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q="+url.QueryEscape("ゼルダの伝説"), env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	if got := metrictest.Int64Sum(t, reader, "vg.enrichment.search.localization_leg", attribute.String("outcome", "merged")); got != 1 {
		t.Fatalf("localization_leg{outcome=merged} = %d, want 1", got)
	}
}

// The resolve-side cached listing search feeds the auto-matcher, not
// a user: it must never count as a search answer.
func TestUnitTelemetry_ResolveSideListingSearchDoesNotCount(t *testing.T) {
	reader := metrictest.Install(t)
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return []pricecharting.Product{{ID: 5005, Name: "Super Mario 64", ConsoleName: "Nintendo 64"}}, nil
	}}
	h := newUnitHandlers(&stubStore{}, nil, prices, newStubCache())

	if meta := h.autoMatchGame(context.Background(), "resolve", []string{"Super Mario 64"}, "", "Nintendo 64", ""); meta == nil {
		t.Fatal("the auto-match must land, proving the listing search ran")
	}

	if got := metrictest.Int64Sum(t, reader, "vg.enrichment.search.requests"); got != 0 {
		t.Fatalf("resolve-side listing search counted %d search answers, want 0", got)
	}
}

// autoMatchGame classifies every attempt exactly once - matched,
// below_threshold or provider_down - with the caller's flow as source.
func TestUnitTelemetry_MatchOutcomesBySourceAndOutcome(t *testing.T) {
	reader := metrictest.Install(t)
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

	if meta := h.autoMatchGame(ctx, "resolve", []string{"Super Mario 64"}, "", "Nintendo 64", ""); meta == nil {
		t.Fatal("want a landed match")
	}
	if meta := h.autoMatchGame(ctx, "rematch", []string{"Totally Unknown Homebrew"}, "", "Super Nintendo", ""); meta != nil {
		t.Fatalf("want an auto-miss, got %+v", meta)
	}
	if meta := h.autoMatchGame(ctx, "rematch", []string{"Provider Down Game"}, "", "Nintendo 64", ""); meta != nil {
		t.Fatalf("want nil on a dead provider, got %+v", meta)
	}

	const name = "vg.enrichment.match.outcomes"
	for _, want := range []struct{ source, outcome string }{
		{"resolve", "matched"},
		{"rematch", "below_threshold"},
		{"rematch", "provider_down"},
	} {
		got := metrictest.Int64Sum(t, reader, name,
			attribute.String("source", want.source), attribute.String("outcome", want.outcome))
		if got != 1 {
			t.Fatalf("match.outcomes{source=%s,outcome=%s} = %d, want 1", want.source, want.outcome, got)
		}
	}
	if total := metrictest.Int64Sum(t, reader, name); total != 3 {
		t.Fatalf("one outcome per attempt: total = %d, want 3", total)
	}
}

// countMatch's region label mirrors the entry region: the same Super
// Famicom candidate matches under ntsc_j but clears no gate under
// base region (below_threshold, label "none"). An unrecognized
// free-text region also clamps to "none" rather than minting its own
// series, or an authenticated user could mint unbounded label cardinality.
func TestUnitTelemetry_MatchOutcomesCarriesRegionLabel(t *testing.T) {
	reader := metrictest.Install(t)
	loose := int64(3500)
	prices := &stubPrices{search: func(_ context.Context, _ string) ([]pricecharting.Product, error) {
		return []pricecharting.Product{{ID: 9101, Name: "Regional Game", ConsoleName: "Super Famicom", LoosePriceCents: &loose}}, nil
	}}
	h := newUnitHandlers(&stubStore{}, nil, prices, newStubCache())
	ctx := context.Background()

	if meta := h.autoMatchGame(ctx, "resolve", []string{"Regional Game"}, "", "Super Nintendo Entertainment System", "ntsc_j"); meta == nil {
		t.Fatal("want a landed ntsc_j match (the JP-admitted console)")
	}
	if meta := h.autoMatchGame(ctx, "resolve", []string{"Regional Game"}, "", "Super Nintendo Entertainment System", ""); meta != nil {
		t.Fatalf("want an auto-miss under base region (the console is JP-only), got %+v", meta)
	}
	if meta := h.autoMatchGame(ctx, "resolve", []string{"Regional Game"}, "", "Super Nintendo Entertainment System", "moon_base_region"); meta != nil {
		t.Fatalf("want an auto-miss under an unrecognized region (treated as base for matching), got %+v", meta)
	}
	if meta := h.autoMatchGame(ctx, "resolve", []string{"Regional Game"}, "", "Super Nintendo Entertainment System", "korea"); meta != nil {
		t.Fatalf("want an auto-miss under korea (base matching; the console is JP-only), got %+v", meta)
	}

	const name = "vg.enrichment.match.outcomes"
	if got := metrictest.Int64Sum(t, reader, name, attribute.String("outcome", "matched"), attribute.String("region", "ntsc_j")); got != 1 {
		t.Fatalf("matched{region=ntsc_j} = %d, want 1", got)
	}
	if got := metrictest.Int64Sum(t, reader, name, attribute.String("outcome", "below_threshold"), attribute.String("region", "none")); got != 2 {
		t.Fatalf("below_threshold{region=none} = %d, want 2 (empty region and an unrecognized one both clamp to none)", got)
	}
	if got := metrictest.Int64Sum(t, reader, name, attribute.String("outcome", "below_threshold"), attribute.String("region", "korea")); got != 1 {
		t.Fatalf("below_threshold{region=korea} = %d, want 1 (a graduated region is a known label, not clamped to none)", got)
	}
	if got := metrictest.Int64Sum(t, reader, name, attribute.String("outcome", "below_threshold"), attribute.String("region", "moon_base_region")); got != 0 {
		t.Fatalf("an unrecognized region must never mint its own label series, got %d", got)
	}
}

// The fallback leg's outcome counter: matched, still_empty (both
// searches empty), or error (fallback search fails). Each case's
// primary query never answers, so the fallback always fires. A fourth
// call with identical translit/canonical forms proves the no-fire
// guard: the fallback sits out, so the total stays exactly 3.
func TestUnitTelemetry_FallbackSearchOutcomes(t *testing.T) {
	reader := metrictest.Install(t)
	loose := int64(3500)
	const platform = "Nintendo Entertainment System"
	prices := &stubPrices{search: func(_ context.Context, q string) ([]pricecharting.Product, error) {
		switch q {
		case "canonical hit":
			return []pricecharting.Product{{ID: 9201, Name: "canonical hit", ConsoleName: "Famicom", LoosePriceCents: &loose}}, nil
		case "canonical error":
			return nil, errors.New("pricecharting down")
		default:
			// Every translit/primary query, and the still-empty case's
			// canonical query, come back with nothing.
			return nil, nil
		}
	}}
	h := newUnitHandlers(&stubStore{}, nil, prices, newStubCache())
	ctx := context.Background()

	if meta := h.autoMatchGame(ctx, "resolve", []string{"translit miss", "canonical hit"}, "", platform, "ntsc_j"); meta == nil {
		t.Fatal("want the fallback leg to land the canonical hit")
	}
	if meta := h.autoMatchGame(ctx, "resolve", []string{"translit miss 2", "canonical still empty"}, "", platform, "ntsc_j"); meta != nil {
		t.Fatalf("want an auto-miss when both legs come back empty, got %+v", meta)
	}
	if meta := h.autoMatchGame(ctx, "resolve", []string{"translit miss 3", "canonical error"}, "", platform, "ntsc_j"); meta != nil {
		t.Fatalf("want nil when the fallback search itself fails, got %+v", meta)
	}
	if meta := h.autoMatchGame(ctx, "resolve", []string{"same form", "same form"}, "", platform, "ntsc_j"); meta != nil {
		t.Fatalf("want an auto-miss (identical forms clear no gate), got %+v", meta)
	}

	const name = "vg.enrichment.match.fallback_search"
	for _, want := range []struct{ outcome string }{{"matched"}, {"still_empty"}, {"error"}} {
		got := metrictest.Int64Sum(t, reader, name, attribute.String("outcome", want.outcome))
		if got != 1 {
			t.Fatalf("fallback_search{outcome=%s} = %d, want 1", want.outcome, got)
		}
	}
	if total := metrictest.Int64Sum(t, reader, name); total != 3 {
		t.Fatalf("identical name forms must never fire the fallback: fallback_search total = %d, want 3 (unchanged)", total)
	}
}

// Price-refresh items: ok means price written and snapshot appended; a
// fetch, write or snapshot failure counts failed - exactly one
// outcome per processed product.
func TestUnitTelemetry_RefreshItemsPrices(t *testing.T) {
	reader := metrictest.Install(t)
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
	if got := metrictest.Int64Sum(t, reader, name, attribute.String("step", "prices"), attribute.String("outcome", "ok")); got != 1 {
		t.Fatalf("prices ok = %d, want 1", got)
	}
	if got := metrictest.Int64Sum(t, reader, name, attribute.String("step", "prices"), attribute.String("outcome", "failed")); got != 3 {
		t.Fatalf("prices failed = %d, want 3", got)
	}
}

// Reprojection items: a rebuilt projection is ok, the diff gate and
// an unusable raw are skipped, a write fault is failed.
func TestUnitTelemetry_RefreshItemsReprojection(t *testing.T) {
	reader := metrictest.Install(t)
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
		got := metrictest.Int64Sum(t, reader, name, attribute.String("step", "reprojection"), attribute.String("outcome", want.outcome))
		if got != want.n {
			t.Fatalf("reprojection %s = %d, want %d", want.outcome, got, want.n)
		}
	}
}

// Sweep items: candidates stashed is flagged, swept clean is ok, and
// a provider fault (either provider) or a store fault is failed.
func TestUnitTelemetry_RefreshItemsSweep(t *testing.T) {
	reader := metrictest.Install(t)
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
		got := metrictest.Int64Sum(t, reader, name, attribute.String("step", "sweep"), attribute.String("outcome", want.outcome))
		if got != want.n {
			t.Fatalf("sweep %s = %d, want %d", want.outcome, got, want.n)
		}
	}
}

// Normalize-community-regions rows: a reviewed synonym promotes, an
// unreviewed string skips, a store-write fault fails (counted separately, so scanned can exceed their sum).
func TestUnitTelemetry_NormalizeCommunityRegions(t *testing.T) {
	reader := metrictest.Install(t)
	st := &stubStore{
		listCommunityRegionDocs: func(context.Context, []string) ([]store.CommunityRegionRef, error) {
			return []store.CommunityRegionRef{
				{ID: "p-ok", Region: "Japan"},
				{ID: "p-unknown", Region: "Atlantis"},
				{ID: "p-write-fail", Region: "japan"},
			}, nil
		},
		setCommunityRegion: func(_ context.Context, id, _ string) error {
			if id == "p-write-fail" {
				return errors.New("mongo down")
			}
			return nil
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	env := newAuthEnv(t)
	admin := env.token(t, "admin1", []string{"admin"})

	rec := serveUnit(t, h, env, http.MethodPost, "/internal/normalize-community-regions", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var counts map[string]int
	if err := json.NewDecoder(rec.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if counts["scanned"] != 3 || counts["normalized"] != 1 || counts["skipped"] != 1 {
		t.Fatalf("counts = %+v, want scanned 3 normalized 1 skipped 1", counts)
	}
	// The write failure lands in neither counter, so scanned outruns
	// their sum: the response-side proof of the divergence the metric below records.
	if counts["scanned"] <= counts["normalized"]+counts["skipped"] {
		t.Fatalf("scanned (%d) must exceed normalized+skipped (%d) when a write fails", counts["scanned"], counts["normalized"]+counts["skipped"])
	}

	const name = "vg.enrichment.normalize.regions"
	for _, want := range []struct {
		outcome string
		n       int64
	}{{"normalized", 1}, {"skipped", 1}, {"failed", 1}} {
		got := metrictest.Int64Sum(t, reader, name, attribute.String("outcome", want.outcome))
		if got != want.n {
			t.Fatalf("%s = %d, want %d", want.outcome, got, want.n)
		}
	}
}

// Each refresh step records its elapsed seconds exactly once per run,
// into the explicit buckets (the SDK defaults top out at 10s).
func TestUnitTelemetry_RefreshStepDurationPerStep(t *testing.T) {
	reader := metrictest.Install(t)
	st := &stubStore{
		listPriced:            func(context.Context) ([]store.Product, error) { return nil, nil },
		listIGDBProducts:      func(context.Context) ([]store.Product, error) { return nil, nil },
		listCommunityProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	ctx := context.Background()

	h.runRefresh(ctx)
	h.runReprojection(ctx)
	h.runCandidateSweep(ctx)

	const name = "vg.enrichment.refresh.step_duration"
	wantBounds := []float64{1, 5, 15, 60, 300, 900, 1800}
	for _, step := range []string{"prices", "reprojection", "sweep"} {
		dp := metrictest.Float64HistogramPoint(t, reader, name, attribute.String("step", step))
		if dp.Count != 1 {
			t.Fatalf("step_duration{step=%s} count = %d, want 1", step, dp.Count)
		}
		if !slices.Equal(dp.Bounds, wantBounds) {
			t.Fatalf("step_duration{step=%s} bounds = %v, want %v", step, dp.Bounds, wantBounds)
		}
	}
}

// A step that aborts on its list read still records: the count
// series is the refresh-happened signal for the stalled-refresh alert.
func TestUnitTelemetry_RefreshStepDurationRecordedOnAbort(t *testing.T) {
	reader := metrictest.Install(t)
	st := &stubStore{listPriced: func(context.Context) ([]store.Product, error) { return nil, errors.New("mongo down") }}
	h := newUnitHandlers(st, nil, nil, newStubCache())

	h.runRefresh(context.Background())

	dp := metrictest.Float64HistogramPoint(t, reader, "vg.enrichment.refresh.step_duration", attribute.String("step", "prices"))
	if dp.Count != 1 {
		t.Fatalf("aborted step must still record its duration: count = %d, want 1", dp.Count)
	}
}

// The refresh-last-completed gauge observes the unix time
// recordRefreshStepDuration stamped. Unlike a counter's increase(), a
// gauge re-reports its last-known value every export interval, so
// Prometheus already has a real sample across a pod replacement.
func TestUnitTelemetry_RefreshLastCompletedObservesStampedStep(t *testing.T) {
	reader := metrictest.Install(t)
	h := newUnitHandlers(&stubStore{}, nil, nil, newStubCache())
	fixed := time.Date(2026, 8, 15, 6, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return fixed }

	h.recordRefreshStepDuration(context.Background(), "prices", 1.5)

	const name = "vg.enrichment.refresh.last_completed"
	dp := metrictest.Float64GaugePoint(t, reader, name, attribute.String("step", "prices"))
	if dp.Value != float64(fixed.Unix()) {
		t.Fatalf("last_completed{step=prices} = %v, want %v", dp.Value, float64(fixed.Unix()))
	}
}

// A never-stamped step yields no observation, never a false zero:
// this keeps a fresh process (post pod-replacement) safe, since every
// step stays absent until its own first completion.
func TestUnitTelemetry_RefreshLastCompletedOmitsNeverStampedStep(t *testing.T) {
	reader := metrictest.Install(t)
	h := newUnitHandlers(&stubStore{}, nil, nil, newStubCache())

	h.recordRefreshStepDuration(context.Background(), "prices", 1.5)

	const name = "vg.enrichment.refresh.last_completed"
	if dp := metrictest.Float64GaugePoint(t, reader, name, attribute.String("step", "reprojection")); dp.Value != 0 {
		t.Fatalf("never-stamped step must yield no observation, got %+v", dp)
	}
}

// lockedBuffer serializes writes: the detached refresh run logs from
// its own goroutine while the test reads the captured output.
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
// hung-refresh triage.
func TestUnitTelemetry_CatalogRefreshStartedLogsTrigger(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{
		listPriced:            func(context.Context) ([]store.Product, error) { return nil, nil },
		listIGDBProducts:      func(context.Context) ([]store.Product, error) { return nil, nil },
		listCommunityProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
	}
	buf := &lockedBuffer{}
	h := New(st, nil, nil, &stubFX{}, newStubCache(), Options{
		SearchCacheTTL:   time.Hour,
		ProductCacheTTL:  time.Minute,
		IGDBRefreshAfter: 720 * time.Hour,
		Logger:           slog.New(slog.NewJSONHandler(buf, nil)),
	})

	rec := serveInternal(t, h, env, env.serviceToken(t, "svc:catalog-refresh"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("internal trigger: %d", rec.Code)
	}
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	logged := buf.String()
	if !strings.Contains(logged, `"msg":"catalog refresh started"`) || !strings.Contains(logged, `"trigger":"internal"`) {
		t.Fatalf("internal started line missing: %s", logged)
	}

	rec = serveUnit(t, h, env, http.MethodPost, "/admin/refresh", env.token(t, "a1", []string{"admin"}), nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admin trigger: %d", rec.Code)
	}
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	if !strings.Contains(buf.String(), `"trigger":"admin"`) {
		t.Fatalf("admin started line missing: %s", buf.String())
	}
}

// Pins the shared 500 helper: the problem body carries only a
// generic detail text, while the log line carries the op and cause
// the client never sees.
func TestUnitInternalErrorLogCarriesCause(t *testing.T) {
	env := newAuthEnv(t)
	boom := errors.New("mongo exploded")
	st := &stubStore{productsByIDs: func(context.Context, []string) ([]store.Product, error) {
		return nil, boom
	}}
	buf := &lockedBuffer{}
	h := New(st, nil, nil, &stubFX{}, newStubCache(), Options{
		SearchCacheTTL: time.Hour, ProductCacheTTL: time.Minute, IGDBRefreshAfter: 720 * time.Hour,
		Logger: slog.New(slog.NewJSONHandler(buf, nil)),
	})

	rec := serveUnit(t, h, env, http.MethodPost, "/products/prices:batch",
		env.token(t, "u1", []string{"user"}), map[string]any{"product_ids": []string{uuid.NewString()}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "internal" || p.Detail != "price lookup failed" {
		t.Fatalf("problem = %+v, want code internal, detail %q", p, "price lookup failed")
	}

	logged := buf.String()
	if !strings.Contains(logged, `"msg":"handler error"`) || !strings.Contains(logged, `"level":"ERROR"`) ||
		!strings.Contains(logged, `"op":"batch_prices"`) || !strings.Contains(logged, "mongo exploded") {
		t.Fatalf("handler error log line missing or wrong shape: %s", logged)
	}
}

// Names requireAdmin itself as the mechanism: a plain user is
// forbidden, an admin passes, using CreateCommunityProduct as the
// representative site (other callers have their own per-handler RBAC tests).
func TestUnitRequireAdmin_Guards(t *testing.T) {
	env := newAuthEnv(t)
	h := newUnitHandlers(&stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		p.ID = uuid.NewString()
		return p, nil
	}}, &stubGames{}, &stubPrices{}, newStubCache())
	body := map[string]any{"type": "game", "name": "Chrono Trigger"}

	user := env.token(t, "u1", []string{"user"})
	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", user, body)
	reqtest.AssertProblemRec(t, rec, http.StatusForbidden, "forbidden")

	admin := env.token(t, "a1", []string{"admin"})
	rec = serveUnit(t, h, env, http.MethodPost, "/admin/products", admin, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body.String())
	}
}

// stubErrMeterProvider hands out a meter that refuses every
// registration; noop embeds satisfy the rest of the interfaces.
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

func (stubErrMeter) Float64ObservableGauge(string, ...metric.Float64ObservableGaugeOption) (metric.Float64ObservableGauge, error) {
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
	h.countLocalizationLeg(ctx, "merged")
	h.countMatch(ctx, "resolve", "matched", "ntsc_u")
	h.countFallbackSearch(ctx, "matched")
	h.countRefreshItem(ctx, "prices", "ok")
	h.recordRefreshStepDuration(ctx, "prices", 1.5)
	h.countNormalizeCommunityRegions(ctx, "normalized")

	logged := buf.String()
	for _, want := range []string{
		`"msg":"counter unavailable","name":"vg.enrichment.cache.fail_open"`,
		`"msg":"counter unavailable","name":"vg.enrichment.search.requests"`,
		`"msg":"counter unavailable","name":"vg.enrichment.search.localization_leg"`,
		`"msg":"counter unavailable","name":"vg.enrichment.match.outcomes"`,
		`"msg":"counter unavailable","name":"vg.enrichment.match.fallback_search"`,
		`"msg":"counter unavailable","name":"vg.enrichment.refresh.items"`,
		`"msg":"histogram unavailable","name":"vg.enrichment.refresh.step_duration"`,
		"refresh last completed gauge unavailable",
		`"msg":"counter unavailable","name":"vg.enrichment.normalize.regions"`,
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("missing registration-failure log %q in: %s", want, logged)
		}
	}
}

// Pins the constructor's tolerate-nil idiom: with every registration
// failing and Options.Logger left nil, New must still complete, not panic.
func TestUnitTelemetry_NilLoggerDoesNotPanic(t *testing.T) {
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(stubErrMeterProvider{})
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	h := New(&stubStore{}, nil, nil, &stubFX{}, newStubCache(), Options{})
	if h == nil {
		t.Fatal("New returned nil")
	}
}
