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

// Store is the persistence surface the handlers consume. The sentinel error
// store.ErrNotFound is returned as-is; handlers branch on it via errors.Is.
// Upsert reports whether this call created the account; Delete reports
// whether a row was removed (false: already gone). Both feed the outcome
// labels on the domain counters.
type Store interface {
	Upsert(ctx context.Context, email, displayNameSeed string, avatarURL *string, preferredCurrency string) (u store.User, created bool, err error)
	Get(ctx context.Context, id uuid.UUID) (store.User, error)
	Update(ctx context.Context, id uuid.UUID, handle, avatarURL, preferredCurrency, profileVisibility, landingPage *string, cooldown time.Duration) (store.User, error)
	Delete(ctx context.Context, id uuid.UUID) (deleted bool, err error)
	GetByHandle(ctx context.Context, foldedHandle string) (store.User, error)
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]store.User, error)
	SearchListed(ctx context.Context, foldedQuery string, limit int) ([]store.User, error)
}

// The concrete *store.Store must satisfy the Store interface above. main.go
// passes the same concrete type into New, so this assertion also documents
// the production wiring.
var _ Store = (*store.Store)(nil)

// Handlers owns the backing store and the domain counters for every HTTP
// handler in the user service.
type Handlers struct {
	store          Store
	handleCooldown time.Duration
	accountUpserts metric.Int64Counter
	currencySeeds  metric.Int64Counter
	accountDeletes metric.Int64Counter
}

// New builds a Handlers wired to the given store; cooldown gates how often
// a caller may change their handle (passed through to Store.Update). The
// OTel counters are best-effort: a registration failure is logged but does
// not prevent startup (every increment site guards the nil).
func New(st Store, cooldown time.Duration) *Handlers {
	h := &Handlers{store: st, handleCooldown: cooldown}
	m := otel.Meter("github.com/levonn-dev/vgkeep/services/user")
	var err error
	if h.accountUpserts, err = vgotel.Counter(m, "vg.user.account.upserts",
		"Login-path profile upserts by outcome (created or existing)", "{upsert}"); err != nil {
		slog.Error("account upserts counter unavailable", "err", err)
	}
	if h.currencySeeds, err = vgotel.Counter(m, "vg.user.currency.seeds",
		"preferred_currency seeds for new accounts by source (locale hint or fallback)", "{seed}"); err != nil {
		slog.Error("currency seeds counter unavailable", "err", err)
	}
	if h.accountDeletes, err = vgotel.Counter(m, "vg.user.account.deletes",
		"Account deletions by outcome (deleted or noop)", "{delete}"); err != nil {
		slog.Error("account deletes counter unavailable", "err", err)
	}
	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) { httpkit.WriteJSON(w, status, v) }
func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblemFields(w, r, status, code, detail)
}

// internalError answers a 500 and logs its cause via slog's
// package-level funcs, since Handlers holds no *slog.Logger field and
// every other log line here already goes through them. op is the
// log's "op" key, detail the response text - same op/err convention
// and log-then-respond shape as collection, social, and enrichment's
// h.internalError, kept as a method here for that identical
// call-site shape even though it never touches h.
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, op, detail string, err error) {
	slog.ErrorContext(r.Context(), "store error", "op", op, "err", err)
	problem(w, r, http.StatusInternalServerError, "internal", detail)
}

// maxBodyBytes caps request bodies; every user-service body is a
// small profile fragment, far under this.
const maxBodyBytes = 64 << 10
