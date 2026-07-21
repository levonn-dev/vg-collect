// Package server maps HTTP (generated ServerInterface) onto the store,
// enforcing per-route authorization from JWT claims.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/services/user/internal/store"
)

// Store is the persistence surface the handlers consume. The sentinel error
// store.ErrNotFound is returned as-is; handlers branch on it via errors.Is.
// Upsert reports whether this call created the account; Delete reports
// whether a row was removed (false: already gone). Both feed the outcome
// labels on the domain counters.
type Store interface {
	Upsert(ctx context.Context, email, displayName string, avatarURL *string, preferredCurrency string) (u store.User, created bool, err error)
	Get(ctx context.Context, id uuid.UUID) (store.User, error)
	Update(ctx context.Context, id uuid.UUID, displayName, avatarURL, preferredCurrency *string) (store.User, error)
	Delete(ctx context.Context, id uuid.UUID) (deleted bool, err error)
}

// The concrete *store.Store must satisfy the Store interface above. main.go
// passes the same concrete type into New, so this assertion also documents
// the production wiring.
var _ Store = (*store.Store)(nil)

// Handlers owns the backing store and the domain counters for every HTTP
// handler in the user service.
type Handlers struct {
	store          Store
	accountUpserts metric.Int64Counter
	currencySeeds  metric.Int64Counter
	accountDeletes metric.Int64Counter
}

// New builds a Handlers wired to the given store. The OTel counters are
// best-effort: a registration failure is logged but does not prevent
// startup (every increment site guards the nil).
func New(st Store) *Handlers {
	h := &Handlers{store: st}
	m := otel.Meter("github.com/levonn-dev/vg-collect/services/user")
	var err error
	if h.accountUpserts, err = m.Int64Counter("vg.user.account.upserts",
		metric.WithDescription("Login-path profile upserts by outcome (created or existing)"),
		metric.WithUnit("{upsert}")); err != nil {
		slog.Error("account upserts counter unavailable", "err", err)
	}
	if h.currencySeeds, err = m.Int64Counter("vg.user.currency.seeds",
		metric.WithDescription("preferred_currency seeds for new accounts by source (locale hint or fallback)"),
		metric.WithUnit("{seed}")); err != nil {
		slog.Error("currency seeds counter unavailable", "err", err)
	}
	if h.accountDeletes, err = m.Int64Counter("vg.user.account.deletes",
		metric.WithDescription("Account deletions by outcome (deleted or noop)"),
		metric.WithUnit("{delete}")); err != nil {
		slog.Error("account deletes counter unavailable", "err", err)
	}
	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblem(w, r, httpkit.Problem{
		Status: status, Title: http.StatusText(status), Code: code, Detail: detail,
	})
}
