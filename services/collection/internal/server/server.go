// Package server maps HTTP (generated ServerInterface) onto the
// store, the enrichment client, and the dashboard cache, enforcing
// per-user scoping from JWT claims. Every route is own-scoped by the
// token subject except the one admin read: the product-references
// count backing the catalog's guarded product delete.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/cache"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// Store is the persistence surface the handlers consume. Sentinel
// errors (store.ErrNotFound, store.ErrTagNotFound, store.ErrNameTaken,
// store.ErrNotInBacklog, store.ErrConflictingOrder) are returned as-is;
// handlers branch via errors.Is.
type Store interface {
	CreateEntry(ctx context.Context, e store.Entry, tagIDs []uuid.UUID) (store.Entry, error)
	GetEntry(ctx context.Context, userID, id uuid.UUID) (store.Entry, error)
	UpdateEntry(ctx context.Context, e store.Entry, tagIDs []uuid.UUID) (store.Entry, error)
	DeleteEntry(ctx context.Context, userID, id uuid.UUID) error
	AckRegionMismatch(ctx context.Context, userID, entryID uuid.UUID) error
	BulkUpdateEntries(ctx context.Context, userID uuid.UUID, entryIDs []uuid.UUID, actions store.BulkActions) (int, error)
	Reorder(ctx context.Context, userID, entryID uuid.UUID, afterID, beforeID *uuid.UUID) (store.Entry, error)
	ListEntries(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.Entry, error)
	LibrarySummary(ctx context.Context, userID uuid.UUID) ([]store.LibraryGame, error)
	ListTags(ctx context.Context, userID uuid.UUID) ([]store.Tag, error)
	CreateTag(ctx context.Context, userID uuid.UUID, name string) (store.Tag, error)
	RenameTag(ctx context.Context, userID, id uuid.UUID, name string) (store.Tag, error)
	DeleteTag(ctx context.Context, userID, id uuid.UUID) error
	ListViews(ctx context.Context, userID uuid.UUID) ([]store.View, error)
	CreateView(ctx context.Context, userID uuid.UUID, name string, params []byte, visibility string) (store.View, error)
	UpdateView(ctx context.Context, userID, id uuid.UUID, name string, params []byte, visibility string) (store.View, error)
	DeleteView(ctx context.Context, userID, id uuid.UUID) error
	SeedDefaultViews(ctx context.Context, userID uuid.UUID) error
	GetSharedShelf(ctx context.Context, id uuid.UUID) (store.View, error)
	GetSharedShelfBySlug(ctx context.Context, ownerID uuid.UUID, foldedSlug string) (store.View, error)
	ListListedShelves(ctx context.Context, ownerIDs []uuid.UUID, limit, offset int) ([]store.View, int, error)
	SharedShelvesByIDs(ctx context.Context, ids []uuid.UUID) ([]store.View, error)
	CountEntriesFiltered(ctx context.Context, userID uuid.UUID, f store.Filters) (int, error)
	CoverURLs(ctx context.Context, userID uuid.UUID, f store.Filters, limit int) ([]string, error)
	DashboardCounts(ctx context.Context, userID uuid.UUID, f store.Filters) (store.DashboardCounts, error)
	PricingRows(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.PricingRow, error)
	PurgeUserData(ctx context.Context, userID uuid.UUID) error
	ListGameBackedRefs(ctx context.Context) ([]store.GameEntryRef, error)
	SetSnapshotFields(ctx context.Context, entryID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error
	ListAutoGameRematchRefs(ctx context.Context) ([]store.RematchEntryRef, error)
	RepointEntry(ctx context.Context, entryID, productID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error
	ListNameOnlyPlatformEntries(ctx context.Context) ([]store.PlatformEntryRef, error)
	SetEntryPlatformIdentity(ctx context.Context, entryID uuid.UUID, igdbID int64, name string) error
	ListOpenRegionEntries(ctx context.Context, known []string) ([]store.OpenRegionEntryRef, error)
	PromoteEntryRegion(ctx context.Context, entryID uuid.UUID, region string) error
	PromoteEntryRegionSnapshot(ctx context.Context, entryID uuid.UUID, region string, d *time.Time, name, translit, cover *string) error
	CountEntriesByProduct(ctx context.Context, productID uuid.UUID) (int64, error)
	CreateSubmission(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error)
	LatestSubmissionForEntry(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error)
	LatestApprovedSubmissionForEntry(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error)
	AckSubmissionResolution(ctx context.Context, id uuid.UUID) error
	CancelSubmission(ctx context.Context, userID, entryID uuid.UUID) error
	GetSubmission(ctx context.Context, id uuid.UUID) (store.Submission, error)
	CountPendingSubmissions(ctx context.Context, userID uuid.UUID) (int64, error)
	CountAllPendingSubmissions(ctx context.Context) (int64, error)
	CountSubmissionsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error)
	ListPendingSubmissions(ctx context.Context, limit, offset int) ([]store.SubmissionProposal, int64, error)
	RejectSubmission(ctx context.Context, id uuid.UUID, reason string) (store.Submission, error)
	RecordSubmissionProduct(ctx context.Context, id, productID uuid.UUID) error
	ApproveSubmission(ctx context.Context, id uuid.UUID, snap store.CatalogSnapshot) (store.Submission, error)
}

// Enrichment is the catalog surface (typed reads with the caller's
// own bearer relayed on every hop).
type Enrichment interface {
	GetProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichapi.Product, error)
	Resolve(ctx context.Context, bearer string, req enrichapi.ResolveRequest) (enrichapi.Product, error)
	BatchPrices(ctx context.Context, bearer string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error)
	PriceHistory(ctx context.Context, bearer string, ids []uuid.UUID, days int) (map[string][]enrichapi.PricePoint, error)
	CreateCommunityProduct(ctx context.Context, bearer string, req enrichapi.CreateCommunityProductJSONRequestBody) (enrichapi.Product, error)
	ListPlatforms(ctx context.Context, bearer string) ([]enrichmentclient.Platform, error)
}

// Cache is the Valkey surface. Errors mean "Valkey is having a
// moment"; every call site fails open (miss + log).
type Cache interface {
	GetDashboard(ctx context.Context, sub string) ([]byte, error)
	PutDashboard(ctx context.Context, sub string, body []byte, ttl time.Duration) error
	InvalidateDashboard(ctx context.Context, sub string) error
	GetValueHistory(ctx context.Context, sub string) ([]byte, error)
	PutValueHistory(ctx context.Context, sub string, body []byte, ttl time.Duration) error
}

// The concrete types must satisfy the interfaces above; main.go wires
// these same types, so the assertions document production wiring.
var (
	_ Store      = (*store.Store)(nil)
	_ Enrichment = (*enrichmentclient.Client)(nil)
	_ Cache      = (*cache.Cache)(nil)
)

// Options carries tunables that vary between environments.
type Options struct {
	DashboardCacheTTL time.Duration
	Logger            *slog.Logger
}

// Handlers owns the collaborators and knobs for every HTTP handler in
// the collection service.
type Handlers struct {
	store        Store
	enrichment   Enrichment
	cache        Cache
	logger       *slog.Logger
	dashboardTTL time.Duration

	// Domain instruments; nil when registration failed (each emission
	// site guards the nil).
	pricingCompose   metric.Int64Counter
	cacheLookups     metric.Int64Counter
	cacheFailOpen    metric.Int64Counter
	submissionEvents metric.Int64Counter
	rematchDuration  metric.Float64Histogram
	rematchTriples   metric.Int64Counter
	rematchRepoints  metric.Int64Counter

	normalizePlatformsRuns metric.Int64Counter
	normalizeRegionsRuns   metric.Int64Counter

	// rematching guards the entry rematch: one at a time, concurrent
	// triggers answer 409 (mirrors enrichment's catalog-refresh guard).
	rematching atomic.Bool
}

// New builds a Handlers wired to the given collaborators. The OTel
// meter is best-effort: an instrument registration failure is logged
// but never prevents startup.
func New(st Store, enrich Enrichment, c Cache, opts Options) *Handlers {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	m := otel.Meter("github.com/levonn-dev/vgkeep/services/collection")
	counter := func(name, desc, unit string) metric.Int64Counter {
		ctr, err := vgotel.Counter(m, name, desc, unit)
		if err != nil {
			opts.Logger.Error("counter unavailable", "name", name, "err", err)
		}
		return ctr
	}
	histogram := func(name, desc, unit string, buckets ...float64) metric.Float64Histogram {
		hg, err := vgotel.Histogram(m, name, desc, unit, buckets...)
		if err != nil {
			opts.Logger.Error("histogram unavailable", "name", name, "err", err)
		}
		return hg
	}
	h := &Handlers{
		store:        st,
		enrichment:   enrich,
		cache:        c,
		logger:       opts.Logger,
		dashboardTTL: opts.DashboardCacheTTL,
		pricingCompose: counter("vg.collection.pricing.compose",
			"Read-time value compositions that called enrichment for prices, by surface and outcome",
			"{request}"),
		cacheLookups: counter("vg.collection.cache.lookups",
			"Dashboard and value-history cache reads, split hit/miss",
			"{lookup}"),
		cacheFailOpen: counter("vg.collection.cache.fail_open",
			"Valkey operations that failed and were failed open",
			"{event}"),
		submissionEvents: counter("vg.collection.submissions.events",
			"Catalog submission lifecycle transitions",
			"{event}"),
		// Explicit boundaries: the SDK defaults top out at 10s and would
		// flatten a multi-minute entry-rematch run into the last bucket
		// (the same shared DurationBuckets tuple enrichment's
		// refresh.step_duration histogram uses).
		rematchDuration: histogram("vg.collection.rematch.duration",
			"Elapsed seconds per entry-rematch run",
			"s", vgotel.DurationBuckets...),
		rematchTriples: counter("vg.collection.rematch.triples",
			"Entry-rematch (game, platform, region) triples by outcome",
			"{triple}"),
		rematchRepoints: counter("vg.collection.rematch.repoints",
			"Entries repointed onto a region-correct sibling by the entry rematch",
			"{entry}"),
		normalizePlatformsRuns: counter("vg.collection.normalize.platforms",
			"Normalize-platforms sweep rows by outcome",
			"{row}"),
		normalizeRegionsRuns: counter("vg.collection.normalize.regions",
			"Normalize-regions sweep rows by outcome",
			"{row}"),
	}
	registerPendingGauge(m, st, opts.Logger)
	return h
}

// registerPendingGauge reports the all-users pending submission count
// (the admin review backlog) on every collection cycle. A nil store
// (router-only construction) registers nothing.
func registerPendingGauge(m metric.Meter, st Store, logger *slog.Logger) {
	if st == nil {
		return
	}
	pending, err := m.Int64ObservableGauge("vg.collection.submissions.pending",
		metric.WithDescription("Catalog submissions awaiting an admin verdict, across all users"),
		metric.WithUnit("{submission}"))
	if err != nil {
		logger.Error("pending-submissions gauge unavailable", "err", err)
		return
	}
	if _, err := m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		n, err := st.CountAllPendingSubmissions(ctx)
		if err != nil {
			return err
		}
		o.ObserveInt64(pending, n)
		return nil
	}, pending); err != nil {
		logger.Error("pending-submissions gauge unavailable", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) { httpkit.WriteJSON(w, status, v) }

// writeRawJSON serves cached response bodies without re-encoding.
func writeRawJSON(w http.ResponseWriter, body []byte) { httpkit.WriteRawJSON(w, http.StatusOK, body) }

func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblemFields(w, r, status, code, detail)
}

// failOpen records a Valkey failure the caller is about to treat as a
// cache miss (log + metric; the service dashboard watches the metric).
func (h *Handlers) failOpen(ctx context.Context, op string, err error) {
	valkeykit.FailOpen(ctx, h.logger, h.cacheFailOpen, op, err)
}

// composeEvent counts one read-time value composition that reached
// enrichment; err classifies the outcome (ok or degraded). Requests
// that price nothing never get here, keeping degraded/(ok+degraded)
// a clean enrichment-hop failure rate.
func (h *Handlers) composeEvent(ctx context.Context, op string, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "degraded"
	}
	vgotel.Count(ctx, h.pricingCompose, attribute.String("op", op), attribute.String("outcome", outcome))
}

// cacheLookup counts a cache GET as hit or miss. The surface label is
// keyed "cache" (not "op") to match the bff's lookups counter, so one
// key groups cache hit ratios across services; "op" stays the
// operation key on the fail-open counter. An errored GET is a miss
// here and additionally a fail-open event at the failOpen site.
func (h *Handlers) cacheLookup(ctx context.Context, cache string, hit bool) {
	outcome := "miss"
	if hit {
		outcome = "hit"
	}
	vgotel.Count(ctx, h.cacheLookups, attribute.String("cache", cache), attribute.String("outcome", outcome))
}

// submissionEvent counts one submission lifecycle transition
// (created, cancelled, approved, rejected).
func (h *Handlers) submissionEvent(ctx context.Context, event string) {
	vgotel.Count(ctx, h.submissionEvents, attribute.String("event", event))
}

// countRematchTriple records one entry-rematch (game, platform,
// region) triple's outcome: ok when nothing was pending or the
// triple's resolve succeeded - a per-entry RepointEntry failure logs
// and continues rather than flipping this to failed, so the audit log
// (not this counter) is the honest signal for a partial repoint;
// failed when the member fetch or the resolve call itself errored.
func (h *Handlers) countRematchTriple(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.rematchTriples, attribute.String("outcome", outcome))
}

// countRematchRepoint records one entry the entry rematch actually
// repointed onto a region-correct sibling member.
func (h *Handlers) countRematchRepoint(ctx context.Context) {
	vgotel.Count(ctx, h.rematchRepoints)
}

// recordRematchDuration records one entry-rematch run's elapsed
// seconds (the CAS-gated run only; a 409-refused overlap never runs
// and so never records).
func (h *Handlers) recordRematchDuration(ctx context.Context, seconds float64) {
	vgotel.Record(ctx, h.rematchDuration, seconds)
}

// countNormalizePlatformsRow records one normalize-platforms sweep
// row's outcome: normalized (stamped), skipped (no alias match), or
// failed (the store write itself errored) - the same three-way split
// countNormalizeRegionsRow uses for its sibling lever.
func (h *Handlers) countNormalizePlatformsRow(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.normalizePlatformsRuns, attribute.String("outcome", outcome))
}

// countNormalizeRegionsRow records one normalize-regions sweep row's
// outcome: normalized (promoted), skipped (no fold/synonym match), or
// failed (the product fetch or the store write errored). Only the
// fetch-failure branch also folds into the response body's skipped
// count; a store-write failure increments neither normalized nor
// skipped there (so scanned can exceed their sum) and surfaces only
// through the warning log line and this failed outcome - matching the
// platforms lever's response posture.
func (h *Handlers) countNormalizeRegionsRow(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.normalizeRegionsRuns, attribute.String("outcome", outcome))
}

// internalError answers a 500 and logs its cause, which the problem
// body deliberately does not carry; without this line the reason for
// a 500 exists nowhere.
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, detail string, err error) {
	h.logger.ErrorContext(r.Context(), "internal error", "detail", detail, "err", err)
	problem(w, r, http.StatusInternalServerError, "internal", detail)
}

// bearerToken extracts the raw Authorization bearer, unparsed. caller
// uses it for its own return value; the internal levers use it
// directly, since their caller may be a service token whose subject
// is not a user id (see caller's doc comment).
func bearerToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// caller resolves the authenticated caller: the JWT subject as the
// owning user id, plus the raw bearer for enrichment hops. Never call
// this before the caller is known to be a real user: a service
// token's subject (svc:<name>) is not a uuid, and jwtauth.CallerID
// answers that with a 500, not a clean 403. Every ordinary route is
// user-scoped already. Of the internal levers: resnapshot, the entry
// rematch, normalize-platforms, and normalize-regions all admit a
// service token, so they read bearerToken directly instead and never
// call this at all.
func (h *Handlers) caller(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	return jwtauth.CallerID(w, r, problemEW)
}

// requireAdminOrService admits an admin user or a service token - the
// guard on every operator lever a CronJob drives: resnapshot, the
// entry rematch, normalize-platforms, and normalize-regions.
func (h *Handlers) requireAdminOrService(w http.ResponseWriter, r *http.Request) bool {
	return jwtauth.RequireAdminOrService(w, r, problemEW)
}

var _ api.ServerInterface = (*Handlers)(nil)

// maxBodyBytes caps request bodies; the largest legitimate body is an
// entry update with long notes, far under this.
const maxBodyBytes = 64 * 1024

func datesEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
