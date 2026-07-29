// Package server maps HTTP (generated ServerInterface) onto the
// catalog: search, resolve, product reads, batch prices,
// recommendation scoring, and the price-refresh runner shared by the
// admin trigger and the CronJob's internal endpoint.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/cache"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/fx"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// Store is the persistence surface the handlers consume. The sentinel
// store.ErrNotFound is returned as-is; handlers branch via errors.Is.
type Store interface {
	FindProduct(ctx context.Context, key store.ProductKey) (store.Product, error)
	CreateProduct(ctx context.Context, p store.Product) (store.Product, error)
	GetProduct(ctx context.Context, id string) (store.Product, error)
	SetIGDB(ctx context.Context, id string, m store.IGDBMeta) error
	SetPriceCharting(ctx context.Context, id string, m *store.PCMeta) error
	SetPriceChartingIfMissing(ctx context.Context, id string, m *store.PCMeta) (bool, error)
	PromoteProduct(ctx context.Context, id string, igdbMeta *store.IGDBMeta, platform *store.Platform, pc *store.PCMeta) error
	SetCurrentPrices(ctx context.Context, id string, q store.PriceQuote, asOf time.Time) error
	ListPriced(ctx context.Context) ([]store.Product, error)
	ListUnmatchedGames(ctx context.Context, limit int) ([]store.Product, error)
	ListUnmatchedProducts(ctx context.Context, limit, offset int) ([]store.Product, int64, error)
	DeleteUnmatchedProduct(ctx context.Context, id string) (bool, error)
	ListIGDBProducts(ctx context.Context) ([]store.Product, error)
	ProductsByIDs(ctx context.Context, ids []string) ([]store.Product, error)
	SearchByName(ctx context.Context, q string, limit int) ([]store.Product, error)
	SearchCommunityProducts(ctx context.Context, types []string, q string, limit int) ([]store.Product, error)
	ListCommunityProducts(ctx context.Context) ([]store.Product, error)
	ListCommunityProductsPage(ctx context.Context, limit, offset int) ([]store.Product, int64, error)
	ReplacePromoteCandidates(ctx context.Context, id string, cands []store.PromoteCandidate) error
	ListPromoteCandidateProducts(ctx context.Context, limit, offset int, productID string) ([]store.Product, int64, error)
	DismissPromoteCandidate(ctx context.Context, id, provider string, providerID int64) error
	UpsertRaw(ctx context.Context, games []igdb.Game, fetchedAt time.Time) error
	RawByIDs(ctx context.Context, ids []int64) ([]store.RawGame, error)
	UpsertPlatforms(ctx context.Context, ps []igdb.Platform, fetchedAt time.Time) error
	ListPlatforms(ctx context.Context) ([]store.CatalogPlatform, error)
	PlatformsFetchedAt(ctx context.Context) (time.Time, error)
	AppendSnapshot(ctx context.Context, s store.Snapshot) error
	SnapshotsSince(ctx context.Context, ids []string, since time.Time) (map[string][]store.Snapshot, error)
}

// GameProvider is the IGDB surface (real or stub, selected by
// IGDB_MODE).
type GameProvider interface {
	SearchGames(ctx context.Context, q string, limit int) ([]igdb.Game, error)
	GamesByIDs(ctx context.Context, ids []int64) ([]igdb.Game, error)
	PopularGames(ctx context.Context, genreIDs []int64, excludeIDs []int64, limit int) ([]igdb.Game, error)
	Platforms(ctx context.Context) ([]igdb.Platform, error)
}

// PriceProvider is the PriceCharting surface (real or stub, selected
// by PRICECHARTING_MODE).
type PriceProvider interface {
	Search(ctx context.Context, q string) ([]pricecharting.Product, error)
	Product(ctx context.Context, id int64) (pricecharting.Product, error)
}

// FXProvider is the exchange-rate surface (real or stub, selected by
// FX_MODE).
type FXProvider interface {
	Latest(ctx context.Context) (fx.Rates, error)
}

// Cache is the Valkey surface. Errors mean "Valkey is having a
// moment"; every call site fails open (miss + log).
type Cache interface {
	GetSearch(ctx context.Context, kind, q string) ([]byte, error)
	PutSearch(ctx context.Context, kind, q string, body []byte, ttl time.Duration) error
	GetProduct(ctx context.Context, id string) ([]byte, error)
	PutProduct(ctx context.Context, id string, body []byte, ttl time.Duration) error
	InvalidateProduct(ctx context.Context, id string) error
	GetPlatforms(ctx context.Context) ([]byte, error)
	PutPlatforms(ctx context.Context, body []byte, ttl time.Duration) error
}

// The concrete types must satisfy the interfaces above; main.go wires
// these same types, so the assertions document production wiring.
var (
	_ Store         = (*store.Store)(nil)
	_ GameProvider  = (*igdb.Client)(nil)
	_ GameProvider  = (*igdb.Stub)(nil)
	_ PriceProvider = (*pricecharting.Client)(nil)
	_ PriceProvider = (*pricecharting.Stub)(nil)
	_ FXProvider    = (*fx.Client)(nil)
	_ FXProvider    = (*fx.Stub)(nil)
	_ Cache         = (*cache.Cache)(nil)
)

// matchThreshold is re-exported for log/messages symmetry; scoring
// itself lives in internal/match.
const matchThreshold = match.Threshold

// Options carries tunables that vary between environments.
type Options struct {
	SearchCacheTTL   time.Duration
	ProductCacheTTL  time.Duration
	IGDBRefreshAfter time.Duration
	// InternalRefreshSecrets is the accepted X-Internal-Token set for
	// POST /internal/refresh (an A/B pair during rotation).
	InternalRefreshSecrets []string
	Logger                 *slog.Logger
}

// Handlers owns the collaborators and knobs for every HTTP handler in
// the enrichment service.
type Handlers struct {
	store   Store
	games   GameProvider
	prices  PriceProvider
	fxRates FXProvider
	cache   Cache
	logger  *slog.Logger

	searchTTL        time.Duration
	productTTL       time.Duration
	igdbRefreshAfter time.Duration
	refreshSecrets   []string

	// Domain instruments, registered best-effort in New; the emission
	// helpers below guard the nils, so a telemetry hiccup never blocks
	// serving (the bff cache counter set the pattern).
	cacheFailOpen  metric.Int64Counter
	searchRequests metric.Int64Counter
	matchOutcomes  metric.Int64Counter
	refreshItems   metric.Int64Counter
	walkDuration   metric.Float64Histogram

	// refreshing guards the walk: one at a time, concurrent triggers
	// answer 409.
	refreshing atomic.Bool

	// now is a test seam (staleness math and snapshot stamps).
	now func() time.Time
}

// New builds a Handlers wired to the given collaborators.
func New(st Store, games GameProvider, prices PriceProvider, fxRates FXProvider, c Cache, opts Options) *Handlers {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	h := &Handlers{
		store:   st,
		games:   games,
		prices:  prices,
		fxRates: fxRates,
		cache:   c,
		logger:  opts.Logger,

		searchTTL:        opts.SearchCacheTTL,
		productTTL:       opts.ProductCacheTTL,
		igdbRefreshAfter: opts.IGDBRefreshAfter,
		refreshSecrets:   opts.InternalRefreshSecrets,

		now: time.Now,
	}
	meter := otel.Meter("github.com/levonn-dev/vgkeep/services/enrichment")
	var err error
	if h.cacheFailOpen, err = meter.Int64Counter("vg.enrichment.cache.fail_open",
		metric.WithDescription("Valkey operations that failed and were failed open"),
		metric.WithUnit("{event}")); err != nil {
		opts.Logger.Error("cache fail-open counter unavailable", "err", err)
	}
	if h.searchRequests, err = meter.Int64Counter("vg.enrichment.search.requests",
		metric.WithDescription("Answered catalog searches by kind and answer source"),
		metric.WithUnit("{request}")); err != nil {
		opts.Logger.Error("search requests counter unavailable", "err", err)
	}
	if h.matchOutcomes, err = meter.Int64Counter("vg.enrichment.match.outcomes",
		metric.WithDescription("Auto-match attempts by calling flow and outcome"),
		metric.WithUnit("{attempt}")); err != nil {
		opts.Logger.Error("match outcomes counter unavailable", "err", err)
	}
	if h.refreshItems, err = meter.Int64Counter("vg.enrichment.refresh.items",
		metric.WithDescription("Nightly walk items by step and outcome"),
		metric.WithUnit("{item}")); err != nil {
		opts.Logger.Error("refresh items counter unavailable", "err", err)
	}
	// Explicit boundaries: the SDK defaults top out at 10s and would
	// flatten every multi-minute walk into the last bucket.
	if h.walkDuration, err = meter.Float64Histogram("vg.enrichment.refresh.walk.duration",
		metric.WithDescription("Elapsed seconds per nightly walk step"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(1, 5, 15, 60, 300, 900, 1800)); err != nil {
		opts.Logger.Error("walk duration histogram unavailable", "err", err)
	}
	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeRawJSON serves cached response bodies without re-encoding.
func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblem(w, r, httpkit.Problem{
		Status: status, Title: http.StatusText(status), Code: code, Detail: detail,
	})
}

// failOpen records a Valkey failure the caller is about to treat as a
// cache miss (log + metric; the dashboard watches the metric by op).
func (h *Handlers) failOpen(ctx context.Context, op string, err error) {
	h.logger.WarnContext(ctx, "valkey unavailable; failing open", "op", op, "err", err)
	if h.cacheFailOpen != nil {
		h.cacheFailOpen.Add(ctx, 1, metric.WithAttributes(attribute.String("op", op)))
	}
}

// countSearch records one answered user-facing search. SearchCatalog
// answers only: the resolve-side cached listing search never counts.
func (h *Handlers) countSearch(ctx context.Context, kind, source string) {
	if h.searchRequests != nil {
		h.searchRequests.Add(ctx, 1, metric.WithAttributes(
			attribute.String("kind", kind), attribute.String("source", source)))
	}
}

// countMatch records one auto-match attempt's outcome; source names
// the calling flow (resolve or rematch).
func (h *Handlers) countMatch(ctx context.Context, source, outcome string) {
	if h.matchOutcomes != nil {
		h.matchOutcomes.Add(ctx, 1, metric.WithAttributes(
			attribute.String("source", source), attribute.String("outcome", outcome)))
	}
}

// countRefreshItem records one walked item's outcome for a nightly
// walk step.
func (h *Handlers) countRefreshItem(ctx context.Context, step, outcome string) {
	if h.refreshItems != nil {
		h.refreshItems.Add(ctx, 1, metric.WithAttributes(
			attribute.String("step", step), attribute.String("outcome", outcome)))
	}
}

// recordWalkDuration records one walk step's elapsed seconds. Every
// step defers it, so an aborted or stopped-early step still reports
// and the count series stays an honest the-step-ran signal.
func (h *Handlers) recordWalkDuration(ctx context.Context, step string, seconds float64) {
	if h.walkDuration != nil {
		h.walkDuration.Record(ctx, seconds, metric.WithAttributes(attribute.String("step", step)))
	}
}
