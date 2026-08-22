// Package server maps HTTP (generated ServerInterface) onto the
// catalog: search, resolve, product reads, batch prices,
// recommendation scoring, and the price-refresh runner shared by the
// admin trigger and the CronJob's internal endpoint.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/cache"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/fx"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
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
	PromoteProduct(ctx context.Context, id string, igdbMeta *store.IGDBMeta, platform *store.Platform, pc *store.PCMeta) error
	SetCurrentPrices(ctx context.Context, id string, q store.PriceQuote, asOf time.Time) error
	ListPriced(ctx context.Context) ([]store.Product, error)
	ListUnmatchedProducts(ctx context.Context, limit, offset int) ([]store.Product, int64, error)
	DeleteUnmatchedProduct(ctx context.Context, id string) (bool, error)
	ListIGDBProducts(ctx context.Context) ([]store.Product, error)
	ProductsByIDs(ctx context.Context, ids []string) ([]store.Product, error)
	SearchByName(ctx context.Context, q string, limit int) ([]store.Product, error)
	SearchCommunityProducts(ctx context.Context, types []string, q string, limit int) ([]store.Product, error)
	ListCommunityRegionDocs(ctx context.Context) ([]store.CommunityRegionRef, error)
	SetCommunityRegion(ctx context.Context, id, region string) error
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
	SearchLocalizations(ctx context.Context, q string, limit int) ([]int64, error)
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
	Logger           *slog.Logger
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

	// Domain instruments, registered best-effort in New; the emission
	// helpers below guard the nils, so a telemetry hiccup never blocks
	// serving (the bff cache counter set the pattern).
	cacheFailOpen             metric.Int64Counter
	searchRequests            metric.Int64Counter
	localizationLeg           metric.Int64Counter
	matchOutcomes             metric.Int64Counter
	fallbackSearch            metric.Int64Counter
	refreshItems              metric.Int64Counter
	refreshStepDuration       metric.Float64Histogram
	refreshLastCompleted      metric.Float64ObservableGauge
	normalizeCommunityRegions metric.Int64Counter

	// refreshing guards the catalog refresh: one at a time, concurrent
	// triggers answer 409.
	refreshing atomic.Bool

	// refreshStepStamps holds each step's last-completion unix time in
	// seconds, keyed by step name: recordRefreshStepDuration stamps it,
	// the refreshLastCompleted gauge callback reads it
	// (observeRefreshLastCompleted). A sync.Map, not a fixed-size
	// struct, so a step this process has never completed is simply
	// absent from it - the callback then observes nothing for that
	// step, never a false zero, which is what makes the gauge survive a
	// pod replacement: Prometheus keeps the last real sample a prior
	// process already pushed.
	refreshStepStamps sync.Map

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

		now: time.Now,
	}
	meter := otel.Meter("github.com/levonn-dev/vgkeep/services/enrichment")
	var err error
	if h.cacheFailOpen, err = vgotel.Counter(meter, "vg.enrichment.cache.fail_open",
		"Valkey operations that failed and were failed open", "{event}"); err != nil {
		opts.Logger.Error("cache fail-open counter unavailable", "err", err)
	}
	if h.searchRequests, err = vgotel.Counter(meter, "vg.enrichment.search.requests",
		"Answered catalog searches by kind and answer source", "{request}"); err != nil {
		opts.Logger.Error("search requests counter unavailable", "err", err)
	}
	if h.localizationLeg, err = vgotel.Counter(meter, "vg.enrichment.search.localization_leg",
		"Supplementary localization-title search legs by outcome", "{leg}"); err != nil {
		opts.Logger.Error("localization leg counter unavailable", "err", err)
	}
	if h.matchOutcomes, err = vgotel.Counter(meter, "vg.enrichment.match.outcomes",
		"Auto-match attempts by calling flow and outcome", "{attempt}"); err != nil {
		opts.Logger.Error("match outcomes counter unavailable", "err", err)
	}
	if h.fallbackSearch, err = vgotel.Counter(meter, "vg.enrichment.match.fallback_search",
		"Auto-match fallback name-form searches by outcome", "{search}"); err != nil {
		opts.Logger.Error("fallback search counter unavailable", "err", err)
	}
	if h.refreshItems, err = vgotel.Counter(meter, "vg.enrichment.refresh.items",
		"Nightly refresh items by step and outcome", "{item}"); err != nil {
		opts.Logger.Error("refresh items counter unavailable", "err", err)
	}
	// Explicit boundaries: the SDK defaults top out at 10s and would
	// flatten every multi-minute refresh step into the last bucket
	// (the same shared DurationBuckets tuple collection's
	// rematch.duration histogram uses).
	if h.refreshStepDuration, err = vgotel.Histogram(meter, "vg.enrichment.refresh.step_duration",
		"Elapsed seconds per catalog refresh step", "s", vgotel.DurationBuckets...); err != nil {
		opts.Logger.Error("refresh step duration histogram unavailable", "err", err)
	}
	// Direct SDK registration (not vgotel.Histogram/Counter): an
	// Observable gauge needs RegisterCallback, which those wrappers do
	// not expose. Mirrors libs/go/pgkit's pool gauges and this same
	// service's auth/collection siblings (signing-keys, pending-
	// submissions).
	if h.refreshLastCompleted, err = meter.Float64ObservableGauge("vg.enrichment.refresh.last_completed",
		metric.WithDescription("Unix time a catalog refresh step last completed, labeled by step"),
		metric.WithUnit("s")); err != nil {
		opts.Logger.Error("refresh last completed gauge unavailable", "err", err)
	} else if _, err := meter.RegisterCallback(h.observeRefreshLastCompleted, h.refreshLastCompleted); err != nil {
		opts.Logger.Error("refresh last completed callback unavailable", "err", err)
	}
	if h.normalizeCommunityRegions, err = vgotel.Counter(meter, "vg.enrichment.normalize.regions",
		"Normalize-community-regions sweep rows by outcome", "{row}"); err != nil {
		opts.Logger.Error("normalize community regions counter unavailable", "err", err)
	}
	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) { httpkit.WriteJSON(w, status, v) }

// writeRawJSON serves cached response bodies without re-encoding.
func writeRawJSON(w http.ResponseWriter, body []byte) { httpkit.WriteRawJSON(w, http.StatusOK, body) }

func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblemFields(w, r, status, code, detail)
}

// internalError answers a 500 and logs its cause: op is a stable,
// grep-able label for the failing operation (the log's "op" key),
// detail is the response's human-readable text - the two vary
// independently, same as they did inline. Before this helper, every
// one of the 29 sites it collapses answered 500 with no log line at
// all: the cause existed nowhere. collection's h.internalError proved
// the log-then-respond shape; social and user already logged this
// way, so op/err are the same keys here.
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, op, detail string, err error) {
	h.logger.ErrorContext(r.Context(), "store error", "op", op, "err", err)
	problem(w, r, http.StatusInternalServerError, "internal", detail)
}

// requireService answers false (and writes the 403 problem) unless
// the verified claims are a service token (token_use=service): the
// guard on POST /internal/refresh, the catalog-refresh CronJob's
// machine-only trigger. Same claims-access path and problem shape as
// the admin-role gates elsewhere in this file (jwtauth already ran
// ahead of every handler, so a missing claims value here would be a
// minting bug, not a caller error).
func (h *Handlers) requireService(w http.ResponseWriter, r *http.Request) bool {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.IsService() {
		problem(w, r, http.StatusForbidden, "forbidden", "a service token is required")
		return false
	}
	return true
}

// requireAdminOrService admits an admin user or a service token - the
// guard on POST /internal/normalize-community-regions, this service's
// twin of collection's admin-or-service levers (normalize-platforms,
// normalize-regions): the nightly job runs it as a service token
// alongside those, and an operator can also trigger it with the admin
// role. Same claims-access path and 403 problem shape as
// requireService above.
func (h *Handlers) requireAdminOrService(w http.ResponseWriter, r *http.Request) bool {
	return jwtauth.RequireAdminOrService(w, r, problemEW)
}

// requireAdmin answers false (and writes the 403 problem) unless the
// verified claims carry the admin role: the guard on every admin-only
// product and community-catalog lever (CreateCommunityProduct, the
// unmatched/community/promote-candidate worklists, promote, dismiss,
// the mapping fix, delete, and the immediate refresh trigger). Same
// claims-access path and problem shape as requireService and
// requireAdminOrService above; nine identical inline copies collapse
// into this one.
func (h *Handlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return false
	}
	return true
}

// failOpen records a Valkey failure the caller is about to treat as a
// cache miss (log + metric; the dashboard watches the metric by op).
func (h *Handlers) failOpen(ctx context.Context, op string, err error) {
	valkeykit.FailOpen(ctx, h.logger, h.cacheFailOpen, op, err)
}

// countSearch records one answered user-facing search. SearchCatalog
// answers only: the resolve-side cached listing search never counts.
func (h *Handlers) countSearch(ctx context.Context, kind, source string) {
	vgotel.Count(ctx, h.searchRequests, attribute.String("kind", kind), attribute.String("source", source))
}

// countLocalizationLeg records one supplementary localization-title
// search leg's outcome (merged, empty or error).
func (h *Handlers) countLocalizationLeg(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.localizationLeg, attribute.String("outcome", outcome))
}

// countMatch records one auto-match attempt's outcome; source names
// the calling flow (resolve today; the label stays for future
// flows), region the clamped entry region that steered acceptance
// ("none" when the resolve carried no region). The resolve request's
// region is free text (maxLength 32, no enum), so anything outside
// regionkit.KnownRegions clamps to "none" too - matching already
// treats an unrecognized region as base region (see match.go's
// acceptedConsoles), so "none" stays honest, and an authenticated
// user cannot mint an unbounded label series by varying the string.
func (h *Handlers) countMatch(ctx context.Context, source, outcome, region string) {
	if !regionkit.KnownRegions[region] {
		region = "none"
	}
	vgotel.Count(ctx, h.matchOutcomes,
		attribute.String("source", source), attribute.String("outcome", outcome), attribute.String("region", region))
}

// countFallbackSearch records one fired fallback name-form search leg
// (matched, still_empty, or error).
func (h *Handlers) countFallbackSearch(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.fallbackSearch, attribute.String("outcome", outcome))
}

// countRefreshItem records one visited item's outcome for a nightly
// refresh step.
func (h *Handlers) countRefreshItem(ctx context.Context, step, outcome string) {
	vgotel.Count(ctx, h.refreshItems, attribute.String("step", step), attribute.String("outcome", outcome))
}

// recordRefreshStepDuration records one catalog refresh step's
// elapsed seconds and stamps its completion time for the
// refreshLastCompleted gauge (observeRefreshLastCompleted). Every step
// defers it, so an aborted or stopped-early step still reports and
// both series stay an honest the-step-ran signal.
func (h *Handlers) recordRefreshStepDuration(ctx context.Context, step string, seconds float64) {
	vgotel.Record(ctx, h.refreshStepDuration, seconds, attribute.String("step", step))
	h.refreshStepStamps.Store(step, h.now().Unix())
}

// observeRefreshLastCompleted reports each catalog refresh step's
// last-completion unix time, stamped by recordRefreshStepDuration. A
// step refreshStepStamps has never stamped (never completed in this
// process) is skipped entirely rather than reported as zero: the
// stalled-refresh alert's last_over_time query already treats an
// absent series as "never happened", which is the truth for a step
// this process has not run yet - and, across a pod replacement, the
// prior process's own last real sample is still what Prometheus falls
// back on, since a gauge (unlike a counter's increase()) re-reports
// its last-known value on every export interval.
func (h *Handlers) observeRefreshLastCompleted(_ context.Context, o metric.Observer) error {
	h.refreshStepStamps.Range(func(key, value any) bool {
		o.ObserveFloat64(h.refreshLastCompleted, float64(value.(int64)), metric.WithAttributes(attribute.String("step", key.(string))))
		return true
	})
	return nil
}

// countNormalizeCommunityRegions records one normalize-community-
// regions sweep row's outcome: normalized (promoted), skipped (no
// fold/synonym match), or failed (the store write errored) - the
// same three-way split collection's normalize-regions counter uses.
func (h *Handlers) countNormalizeCommunityRegions(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.normalizeCommunityRegions, attribute.String("outcome", outcome))
}

var _ api.ServerInterface = (*Handlers)(nil)

// maxBodyBytes caps request bodies at the router's specval layer; the
// largest legitimate body is a recommendations-scoring request
// carrying the caller's full owned library (handlers_recommendations.go's
// own DecodeBody call uses the same 256KiB cap directly).
const maxBodyBytes = 256 << 10
