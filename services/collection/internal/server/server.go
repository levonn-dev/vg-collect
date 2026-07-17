// Package server maps HTTP (generated ServerInterface) onto the
// store, the enrichment client, and the dashboard cache, enforcing
// per-user scoping from JWT claims. Every route is own-scoped by the
// token subject except the one admin read: the product-references
// count backing the catalog's guarded product delete.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/services/collection/internal/cache"
	"github.com/levonn-dev/vg-collect/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vg-collect/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vg-collect/services/collection/internal/store"
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
	Reorder(ctx context.Context, userID, entryID uuid.UUID, afterID, beforeID *uuid.UUID) (store.Entry, error)
	ListEntries(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.Entry, error)
	LibrarySummary(ctx context.Context, userID uuid.UUID) ([]store.LibraryGame, error)
	ListTags(ctx context.Context, userID uuid.UUID) ([]store.Tag, error)
	CreateTag(ctx context.Context, userID uuid.UUID, name string) (store.Tag, error)
	RenameTag(ctx context.Context, userID, id uuid.UUID, name string) (store.Tag, error)
	DeleteTag(ctx context.Context, userID, id uuid.UUID) error
	ListViews(ctx context.Context, userID uuid.UUID) ([]store.View, error)
	CreateView(ctx context.Context, userID uuid.UUID, name string, params []byte) (store.View, error)
	UpdateView(ctx context.Context, userID, id uuid.UUID, name string, params []byte) (store.View, error)
	DeleteView(ctx context.Context, userID, id uuid.UUID) error
	DashboardCounts(ctx context.Context, userID uuid.UUID, f store.Filters) (store.DashboardCounts, error)
	PricingRows(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.PricingRow, error)
	PurgeUserData(ctx context.Context, userID uuid.UUID) error
	ListGameBackedRefs(ctx context.Context) ([]store.GameEntryRef, error)
	SetFirstReleaseDate(ctx context.Context, entryID uuid.UUID, d *time.Time) error
	CountEntriesByProduct(ctx context.Context, productID uuid.UUID) (int64, error)
}

// Enrichment is the catalog surface (typed reads with the caller's
// own bearer relayed on every hop).
type Enrichment interface {
	GetProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichapi.Product, error)
	BatchPrices(ctx context.Context, bearer string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error)
	PriceHistory(ctx context.Context, bearer string, ids []uuid.UUID, days int) (map[string][]enrichapi.PricePoint, error)
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
}

// New builds a Handlers wired to the given collaborators.
func New(st Store, enrich Enrichment, c Cache, opts Options) *Handlers {
	return &Handlers{
		store:        st,
		enrichment:   enrich,
		cache:        c,
		logger:       opts.Logger,
		dashboardTTL: opts.DashboardCacheTTL,
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
	_, _ = w.Write(body) //nolint:gosec // G705: body is our own json.Marshal output (already HTML-escaped there), never raw user input; served as application/json, never rendered as HTML
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

// caller resolves the authenticated caller: the JWT subject as the
// owning user id, plus the raw bearer for enrichment hops. jwtauth has
// already validated the token, so a non-uuid subject is a minting bug
// on our side, answered as an internal error (false = answered).
func (h *Handlers) caller(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	claims, ok := jwtauth.FromContext(r.Context())
	if !ok {
		problem(w, r, http.StatusUnauthorized, "missing_token", "no validated token in context")
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "token subject is not a user id")
		return uuid.Nil, "", false
	}
	return id, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), true
}
