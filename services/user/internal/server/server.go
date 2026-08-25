// Package server maps HTTP (generated ServerInterface) onto the store,
// enforcing per-route authorization from JWT claims.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
)

// Store is the persistence surface the handlers consume. ErrNotFound is
// returned as-is (handlers branch via errors.Is). Upsert/Delete report
// whether this call created/removed a row, feeding the counter outcome labels.
type Store interface {
	Upsert(ctx context.Context, email, displayNameSeed string, avatarURL *string, preferredCurrency string) (u store.User, created bool, err error)
	Get(ctx context.Context, id uuid.UUID) (store.User, error)
	Update(ctx context.Context, id uuid.UUID, handle, avatarURL, preferredCurrency, profileVisibility, landingPage *string, cooldown time.Duration) (store.User, error)
	Delete(ctx context.Context, id uuid.UUID) (deleted bool, err error)
	GetByHandle(ctx context.Context, foldedHandle string) (store.User, error)
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]store.User, error)
	SearchListed(ctx context.Context, foldedQuery string, limit int) ([]store.User, error)
}

// Documents the production wiring: main.go passes the same concrete
// *store.Store into New.
var _ Store = (*store.Store)(nil)

// Options carries tunables that vary between environments.
type Options struct {
	// HandleChangeCooldown gates how often a handle may change (passed to
	// Store.Update); the Tilt dev stack overrides it to 5s for e2e's 429 test.
	HandleChangeCooldown time.Duration
	Logger               *slog.Logger
}

// Handlers owns the backing store and the domain counters for every HTTP
// handler in the user service.
type Handlers struct {
	store          Store
	logger         *slog.Logger
	handleCooldown time.Duration
	accountUpserts metric.Int64Counter
	currencySeeds  metric.Int64Counter
	accountDeletes metric.Int64Counter
}

// New builds a Handlers wired to the given store. OTel counters are
// best-effort: a registration failure logs but never blocks startup.
func New(st Store, opts Options) *Handlers {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	h := &Handlers{store: st, logger: opts.Logger, handleCooldown: opts.HandleChangeCooldown}
	m := otel.Meter("github.com/levonn-dev/vgkeep/services/user")
	h.accountUpserts = vgotel.CounterLogged(m, opts.Logger, "vg.user.account.upserts",
		"Login-path profile upserts by outcome (created or existing)", "{upsert}")
	h.currencySeeds = vgotel.CounterLogged(m, opts.Logger, "vg.user.currency.seeds",
		"preferred_currency seeds for new accounts by source (locale hint or fallback)", "{seed}")
	h.accountDeletes = vgotel.CounterLogged(m, opts.Logger, "vg.user.account.deletes",
		"Account deletions by outcome (deleted or noop)", "{delete}")
	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) { httpkit.WriteJSON(w, status, v) }
func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblemFields(w, r, status, code, detail)
}

// internalError logs the cause under a stable "op" label and answers a
// 500 with a separate human-readable detail (they vary independently).
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, op, detail string, err error) {
	h.logger.ErrorContext(r.Context(), "handler error", "op", op, "err", err)
	problem(w, r, http.StatusInternalServerError, "internal", detail)
}

// maxBodyBytes caps request bodies; every user-service body is a
// small profile fragment, far under this.
const maxBodyBytes = 64 << 10
