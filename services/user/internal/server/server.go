// Package server maps HTTP (generated ServerInterface) onto the store,
// enforcing per-route authorization from JWT claims.
package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/services/user/internal/store"
)

// Store is the persistence surface the handlers consume. The sentinel error
// store.ErrNotFound is returned as-is; handlers branch on it via errors.Is.
type Store interface {
	Upsert(ctx context.Context, email, displayName string, avatarURL *string) (store.User, error)
	Get(ctx context.Context, id uuid.UUID) (store.User, error)
	Update(ctx context.Context, id uuid.UUID, displayName, avatarURL *string) (store.User, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// The concrete *store.Store must satisfy the Store interface above. main.go
// passes the same concrete type into New, so this assertion also documents
// the production wiring.
var _ Store = (*store.Store)(nil)

// Handlers owns the backing store for every HTTP handler in the user service.
type Handlers struct{ store Store }

// New builds a Handlers wired to the given store.
func New(st Store) *Handlers { return &Handlers{store: st} }

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
