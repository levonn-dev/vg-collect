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

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/cache"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/match"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/store"
)

// Store is the persistence surface the handlers consume. The sentinel
// store.ErrNotFound is returned as-is; handlers branch via errors.Is.
type Store interface {
	FindProduct(ctx context.Context, key store.ProductKey) (store.Product, error)
	CreateProduct(ctx context.Context, p store.Product) (store.Product, error)
	GetProduct(ctx context.Context, id string) (store.Product, error)
	SetIGDB(ctx context.Context, id string, m store.IGDBMeta) error
	SetPriceCharting(ctx context.Context, id string, m *store.PCMeta) error
	SetCurrentPrices(ctx context.Context, id string, q store.PriceQuote, asOf time.Time) error
	ListPriced(ctx context.Context) ([]store.Product, error)
	ProductsByIDs(ctx context.Context, ids []string) ([]store.Product, error)
	SearchByName(ctx context.Context, q string, limit int) ([]store.Product, error)
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

// Cache is the Valkey surface. Errors mean "Valkey is having a
// moment"; every call site fails open (miss + log).
type Cache interface {
	GetSearch(ctx context.Context, kind, q string) ([]byte, error)
	PutSearch(ctx context.Context, kind, q string, body []byte, ttl time.Duration) error
	GetProduct(ctx context.Context, id string) ([]byte, error)
	PutProduct(ctx context.Context, id string, body []byte, ttl time.Duration) error
	InvalidateProduct(ctx context.Context, id string) error
}

// The concrete types must satisfy the interfaces above; main.go wires
// these same types, so the assertions document production wiring.
var (
	_ Store         = (*store.Store)(nil)
	_ GameProvider  = (*igdb.Client)(nil)
	_ GameProvider  = (*igdb.Stub)(nil)
	_ PriceProvider = (*pricecharting.Client)(nil)
	_ PriceProvider = (*pricecharting.Stub)(nil)
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
	store  Store
	games  GameProvider
	prices PriceProvider
	cache  Cache
	logger *slog.Logger

	searchTTL        time.Duration
	productTTL       time.Duration
	igdbRefreshAfter time.Duration
	refreshSecrets   []string

	// refreshing guards the walk: one at a time, concurrent triggers
	// answer 409.
	refreshing atomic.Bool

	// now is a test seam (staleness math and snapshot stamps).
	now func() time.Time
}

// New builds a Handlers wired to the given collaborators.
func New(st Store, games GameProvider, prices PriceProvider, c Cache, opts Options) *Handlers {
	return &Handlers{
		store:  st,
		games:  games,
		prices: prices,
		cache:  c,
		logger: opts.Logger,

		searchTTL:        opts.SearchCacheTTL,
		productTTL:       opts.ProductCacheTTL,
		igdbRefreshAfter: opts.IGDBRefreshAfter,
		refreshSecrets:   opts.InternalRefreshSecrets,

		now: time.Now,
	}
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
// cache miss.
func (h *Handlers) failOpen(ctx context.Context, op string, err error) {
	h.logger.WarnContext(ctx, "valkey unavailable; failing open", "op", op, "err", err)
}
