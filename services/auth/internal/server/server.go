// Package server maps HTTP (generated ServerInterface) onto the oidc
// adapters, token minter, store, and user-service client. It owns the
// login and token lifecycle orchestration.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/services/auth/internal/oidc"
	"github.com/levonn-dev/vg-collect/services/auth/internal/store"
	"github.com/levonn-dev/vg-collect/services/auth/internal/token"
	"github.com/levonn-dev/vg-collect/services/auth/internal/userclient"
)

// Store is the persistence surface the handlers consume. It is the
// methods of *store.Store that handlers.go actually calls; sentinel and
// typed errors (store.ErrStateNotFound, store.ErrRefreshNotFound,
// store.ErrRefreshExpired, store.ErrRefreshRevoked, *store.ReuseError)
// are returned as-is, since handlers branch on them via errors.Is/As.
type Store interface {
	CreateState(ctx context.Context, st store.AuthState) error
	ConsumeState(ctx context.Context, state string) (store.AuthState, error)
	BindIdentity(ctx context.Context, provider, subject string, userID uuid.UUID) error
	CreateSession(ctx context.Context, tokenHash string, userID uuid.UUID, accessJTI string, expiresAt time.Time) error
	PeekSession(ctx context.Context, tokenHash string) (store.Session, error)
	Rotate(ctx context.Context, presentedHash, newHash, newAccessJTI string, jtiWindow time.Duration) (store.RotateResult, error)
	RevokeFamilyByToken(ctx context.Context, tokenHash string) error
	ActiveSigningKeys(ctx context.Context) ([]store.SigningKey, error)
}

// Minter is the slice of *token.Minter the handlers invoke: signing an
// access token and reporting its TTL. The router wires PublicKey()/Kid()
// off the concrete minter at boot, so they are deliberately absent here;
// this interface backs only the Handlers minter field.
type Minter interface {
	Mint(sub string, roles []string, jti string) (string, error)
	TTL() time.Duration
}

// UserService is the user-service surface the login and refresh paths
// consume (implemented by *userclient.Client). Get returns
// userclient.ErrUserNotFound when the account is gone; handlers branch on
// it via errors.Is.
type UserService interface {
	Upsert(ctx context.Context, email, displayName string, avatarURL *string) (userclient.User, error)
	Get(ctx context.Context, id uuid.UUID) (userclient.User, error)
}

// The concrete collaborators must satisfy the surfaces the server needs.
// main.go passes these same concrete types into New, so these assertions
// also document the production wiring.
var (
	_ Store       = (*store.Store)(nil)
	_ Minter      = (*token.Minter)(nil)
	_ UserService = (*userclient.Client)(nil)
)

// Handlers owns the backing services and tunable knobs for every HTTP
// handler in the auth service.
type Handlers struct {
	store      Store
	minter     Minter
	users      UserService
	providers  map[string]oidc.Provider
	devEnabled bool
	refreshTTL time.Duration
}

// New builds a Handlers wired to the given collaborators.
func New(st Store, m Minter, users UserService,
	providers map[string]oidc.Provider, devEnabled bool, refreshTTL time.Duration) *Handlers {
	return &Handlers{
		store: st, minter: m, users: users,
		providers: providers, devEnabled: devEnabled, refreshTTL: refreshTTL,
	}
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
