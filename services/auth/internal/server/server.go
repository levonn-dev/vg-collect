// Package server maps HTTP (generated ServerInterface) onto the oidc
// adapters, token minter, store, and user-service client. It owns the
// login and token lifecycle orchestration.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/auth/internal/oidc"
	"github.com/levonn-dev/vgkeep/services/auth/internal/store"
	"github.com/levonn-dev/vgkeep/services/auth/internal/token"
	"github.com/levonn-dev/vgkeep/services/auth/internal/userclient"
)

// Store is the persistence surface the handlers consume. It is the
// methods of *store.Store that handlers.go actually calls; sentinel and
// typed errors (store.ErrStateNotFound, store.ErrIdentityNotFound,
// store.ErrIdentityTaken, store.ErrRefreshNotFound,
// store.ErrRefreshExpired, store.ErrRefreshRevoked, *store.ReuseError)
// are returned as-is, since handlers branch on them via errors.Is/As.
type Store interface {
	CreateState(ctx context.Context, st store.AuthState) error
	ConsumeState(ctx context.Context, state string) (store.AuthState, error)
	ResolveIdentity(ctx context.Context, provider, subject, email string) (store.Identity, error)
	BindIdentity(ctx context.Context, provider, subject, email string, userID uuid.UUID) error
	RebindIdentity(ctx context.Context, provider, subject, email string, userID uuid.UUID) error
	ListIdentities(ctx context.Context, userID uuid.UUID) ([]store.Identity, error)
	DeleteIdentity(ctx context.Context, userID, identityID uuid.UUID) error
	DeleteUserAuth(ctx context.Context, userID uuid.UUID) error
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
	Upsert(ctx context.Context, email, displayName string, avatarURL *string, localeHint string) (userclient.User, error)
	Get(ctx context.Context, id uuid.UUID) (userclient.User, error)
}

// Verifier validates the caller's Bearer token on self-service
// endpoints (implemented by *jwtauth.Validator against this service's
// own JWKS).
type Verifier interface {
	Validate(ctx context.Context, raw string) (jwtauth.Claims, error)
}

// The concrete collaborators must satisfy the surfaces the server needs.
// main.go passes these same concrete types into New, so these assertions
// also document the production wiring.
var (
	_ Store       = (*store.Store)(nil)
	_ Minter      = (*token.Minter)(nil)
	_ UserService = (*userclient.Client)(nil)
	_ Verifier    = (*jwtauth.Validator)(nil)
)

// Handlers owns the backing services and tunable knobs for every HTTP
// handler in the auth service.
type Handlers struct {
	store      Store
	minter     Minter
	users      UserService
	providers  map[string]oidc.Provider
	verifier   Verifier
	devEnabled bool
	refreshTTL time.Duration

	// Domain instruments (best-effort: nil when registration failed,
	// and the record helpers no-op).
	loginOutcomes  metric.Int64Counter
	tokenRefreshes metric.Int64Counter
	signingKeys    metric.Int64ObservableGauge
}

// New builds a Handlers wired to the given collaborators. Instruments
// are best-effort, like the bff cache counter: a registration failure
// is logged but never prevents startup.
func New(st Store, m Minter, users UserService, providers map[string]oidc.Provider,
	verifier Verifier, devEnabled bool, refreshTTL time.Duration) *Handlers {
	h := &Handlers{
		store: st, minter: m, users: users, providers: providers,
		verifier: verifier, devEnabled: devEnabled, refreshTTL: refreshTTL,
	}
	meter := otel.Meter("github.com/levonn-dev/vgkeep/services/auth")
	var err error
	h.loginOutcomes, err = meter.Int64Counter("vg.auth.login.outcomes",
		metric.WithDescription("Terminals of provider login and link dances"),
		metric.WithUnit("{login}"))
	if err != nil {
		slog.Error("login outcomes counter unavailable", "err", err)
	}
	h.tokenRefreshes, err = meter.Int64Counter("vg.auth.token.refreshes",
		metric.WithDescription("Refresh token rotation terminals"),
		metric.WithUnit("{refresh}"))
	if err != nil {
		slog.Error("token refreshes counter unavailable", "err", err)
	}
	h.signingKeys, err = meter.Int64ObservableGauge("vg.auth.signing_keys.active",
		metric.WithDescription("Signing keys the JWKS serves right now"),
		metric.WithUnit("{key}"))
	if err != nil {
		slog.Error("signing keys gauge unavailable", "err", err)
	} else if _, err := meter.RegisterCallback(h.observeSigningKeys, h.signingKeys); err != nil {
		slog.Error("signing keys callback unavailable", "err", err)
	}
	return h
}

// observeSigningKeys reports how many keys the JWKS serves. On a store
// error it observes nothing: a gap in the series, never a false zero
// (zero is the platform-wide-401 alert condition).
func (h *Handlers) observeSigningKeys(ctx context.Context, o metric.Observer) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	keys, err := h.store.ActiveSigningKeys(ctx)
	if err != nil {
		return err
	}
	o.ObserveInt64(h.signingKeys, int64(len(keys)))
	return nil
}

// Label values for the outcome counters; all bounded sets, and the
// dashboards key on these exact spellings. There is deliberately no
// provider="unknown": terminals without an attributable provider
// (malformed body, unknown_provider, invalid_state) are not counted
// and stay visible as 4xx in the RED panels.
const (
	flowLogin = "login"
	flowLink  = "link"

	outcomeSuccess       = "success"
	outcomeRejected      = "rejected"
	outcomeProviderError = "provider_error"
	outcomeUpstreamError = "upstream_error"
	outcomeInternalError = "internal_error"
	outcomeReuseDetected = "reuse_detected"
)

// recordLogin counts one terminal of a provider dance whose provider
// is known.
func (h *Handlers) recordLogin(ctx context.Context, provider, flow, outcome string) {
	if h.loginOutcomes == nil {
		return
	}
	h.loginOutcomes.Add(ctx, 1, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("flow", flow),
		attribute.String("outcome", outcome)))
}

// recordRefresh counts one RefreshToken terminal.
func (h *Handlers) recordRefresh(ctx context.Context, outcome string) {
	if h.tokenRefreshes == nil {
		return
	}
	h.tokenRefreshes.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// Problem responses never echo internal error details to callers, so
// these events carry them server-side (the shared slog handler
// attaches trace ids). Never log token material: no refresh tokens,
// no hashes, no minted JWTs.

// logProviderError reports an identity-provider hop failing behind a
// 502 (the ProviderError string carries op and status).
func logProviderError(ctx context.Context, provider string, err error) {
	slog.ErrorContext(ctx, "provider request failed", "provider", provider, "err", err)
}

// logStoreError reports the branch detail behind a 500 internal
// answer; op names the operation that failed.
func logStoreError(ctx context.Context, op string, err error) {
	slog.ErrorContext(ctx, "auth store error", "op", op, "err", err)
}

// logUserServiceError reports the user service failing behind a login
// 502 or a refresh 503.
func logUserServiceError(ctx context.Context, op string, err error) {
	slog.ErrorContext(ctx, "user service unavailable", "op", op, "err", err)
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

// requireUserOrService authenticates a Bearer caller that may be
// either a user (uuid subject) or a service token (uuid.Nil returned;
// the claims carry the role for the caller to authorize on).
func (h *Handlers) requireUserOrService(w http.ResponseWriter, r *http.Request) (uuid.UUID, jwtauth.Claims, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		problem(w, r, http.StatusUnauthorized, "missing_token", "Authorization: Bearer token required")
		return uuid.Nil, jwtauth.Claims{}, false
	}
	claims, err := h.verifier.Validate(r.Context(), raw)
	if err != nil {
		problem(w, r, http.StatusUnauthorized, "invalid_token", "token validation failed")
		return uuid.Nil, jwtauth.Claims{}, false
	}
	userID, _ := uuid.Parse(claims.Subject)
	return userID, claims, true
}

// requireUser authenticates a self-service call and pins it to a real
// user subject (service tokens carry a non-uuid sub and are rejected:
// these endpoints act on "my account", which a service is not).
func (h *Handlers) requireUser(w http.ResponseWriter, r *http.Request) (uuid.UUID, jwtauth.Claims, bool) {
	userID, claims, ok := h.requireUserOrService(w, r)
	if !ok {
		return uuid.Nil, jwtauth.Claims{}, false
	}
	if userID == uuid.Nil {
		problem(w, r, http.StatusForbidden, "forbidden", "a user token is required")
		return uuid.Nil, jwtauth.Claims{}, false
	}
	return userID, claims, true
}
