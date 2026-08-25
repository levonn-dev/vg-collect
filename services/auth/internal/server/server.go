// Package server maps HTTP (generated ServerInterface) onto the oidc adapters, token
// minter, store, and user-service client, owning login and token lifecycle orchestration.
package server

import (
	"context"
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
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/services/auth/internal/oidc"
	"github.com/levonn-dev/vgkeep/services/auth/internal/store"
	"github.com/levonn-dev/vgkeep/services/auth/internal/token"
	"github.com/levonn-dev/vgkeep/services/auth/internal/userclient"
)

// Store is the persistence surface handlers.go calls. Its sentinel and typed errors
// return as-is: handlers branch on them via errors.Is/As.
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

// Minter is the *token.Minter surface handlers invoke (sign + TTL). PublicKey()/Kid()
// are wired directly off the concrete minter at boot, so they are absent here.
type Minter interface {
	Mint(sub string, roles []string, jti string) (string, error)
	MintService(sub string, ttl time.Duration) (string, error)
	TTL() time.Duration
}

// UserService is the surface login/refresh consume. Get returns userclient.ErrUserNotFound
// when the account is gone; handlers branch on it via errors.Is.
type UserService interface {
	Upsert(ctx context.Context, email, displayName string, avatarURL *string, localeHint string) (userclient.User, error)
	Get(ctx context.Context, id uuid.UUID) (userclient.User, error)
}

// Verifier validates the caller's Bearer token on self-service endpoints (jwtauth.Validator against this service's own JWKS).
type Verifier interface {
	Validate(ctx context.Context, raw string) (jwtauth.Claims, error)
}

// Concrete collaborators satisfy these surfaces; main.go wires the same types, documenting production wiring.
var (
	_ Store       = (*store.Store)(nil)
	_ Minter      = (*token.Minter)(nil)
	_ UserService = (*userclient.Client)(nil)
	_ Verifier    = (*jwtauth.Validator)(nil)
)

// Options carries tunables that vary between environments.
type Options struct {
	DevProviderEnabled     bool
	RefreshTokenTTL        time.Duration
	InternalServiceSecrets []string
	Logger                 *slog.Logger
}

// Handlers owns the backing services and tunable knobs for every HTTP handler in the auth service.
type Handlers struct {
	store      Store
	minter     Minter
	users      UserService
	providers  map[string]oidc.Provider
	verifier   Verifier
	devEnabled bool
	refreshTTL time.Duration

	// internalServiceSecrets is the accepted X-Internal-Token set for POST /internal/service-token (A/B pair during rotation).
	internalServiceSecrets []string

	// logger backs instrument-registration and signing-keys-gauge setup error logs.
	logger *slog.Logger

	// Domain instruments; best-effort: nil on registration failure, record helpers no-op.
	loginOutcomes  metric.Int64Counter
	tokenRefreshes metric.Int64Counter
	signingKeys    metric.Int64ObservableGauge
}

// New builds Handlers wired to the given collaborators. Instrument registration failures
// log but never block startup; opts.Logger defaults to slog.Default() when nil.
func New(st Store, m Minter, users UserService, providers map[string]oidc.Provider,
	verifier Verifier, opts Options) *Handlers {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	h := &Handlers{
		store: st, minter: m, users: users, providers: providers,
		verifier: verifier, devEnabled: opts.DevProviderEnabled, refreshTTL: opts.RefreshTokenTTL,
		internalServiceSecrets: opts.InternalServiceSecrets,
		logger:                 opts.Logger,
	}
	meter := otel.Meter("github.com/levonn-dev/vgkeep/services/auth")
	h.loginOutcomes = vgotel.CounterLogged(meter, h.logger, "vg.auth.login.outcomes",
		"Terminals of provider login and link dances", "{login}")
	h.tokenRefreshes = vgotel.CounterLogged(meter, h.logger, "vg.auth.token.refreshes",
		"Refresh token rotation terminals", "{refresh}")
	var err error
	h.signingKeys, err = meter.Int64ObservableGauge("vg.auth.signing_keys.active",
		metric.WithDescription("Signing keys the JWKS serves right now"),
		metric.WithUnit("{key}"))
	if err != nil {
		h.logger.Error("signing keys gauge unavailable", "err", err)
	} else if _, err := meter.RegisterCallback(h.observeSigningKeys, h.signingKeys); err != nil {
		h.logger.Error("signing keys callback unavailable", "err", err)
	}
	return h
}

// observeSigningKeys reports how many keys the JWKS serves. A store error observes
// nothing (gap, never false zero): zero triggers the platform-wide-401 alert.
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

// Bounded label values; dashboards key on these exact spellings. No provider="unknown":
// unattributable terminals go uncounted and stay visible as 4xx in the RED panels.
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

// recordLogin counts one terminal of a provider dance whose provider is known.
func (h *Handlers) recordLogin(ctx context.Context, provider, flow, outcome string) {
	vgotel.Count(ctx, h.loginOutcomes,
		attribute.String("provider", provider),
		attribute.String("flow", flow),
		attribute.String("outcome", outcome))
}

// recordRefresh counts one RefreshToken terminal.
func (h *Handlers) recordRefresh(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.tokenRefreshes, attribute.String("outcome", outcome))
}

// Problem responses never echo error details to callers; these log server-side (trace ids
// attached). Never log token material: no refresh tokens, hashes, or minted JWTs.

// logProviderError reports an identity-provider hop failing behind a 502 (ProviderError string carries op/status).
func (h *Handlers) logProviderError(ctx context.Context, provider string, err error) {
	h.logger.ErrorContext(ctx, "provider request failed", "provider", provider, "err", err)
}

// logStoreError reports the branch behind a 500; op names the failed operation. Minter
// failures route through here too, so the message stays generic and op carries the subsystem.
func (h *Handlers) logStoreError(ctx context.Context, op string, err error) {
	h.logger.ErrorContext(ctx, "handler error", "op", op, "err", err)
}

// logUserServiceError reports the user service failing behind a login 502 or a refresh 503.
func (h *Handlers) logUserServiceError(ctx context.Context, op string, err error) {
	h.logger.ErrorContext(ctx, "user service unavailable", "op", op, "err", err)
}

func writeJSON(w http.ResponseWriter, status int, v any) { httpkit.WriteJSON(w, status, v) }
func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblemFields(w, r, status, code, detail)
}

// requireUserOrService authenticates a Bearer caller: a user (uuid subject) or a service
// token (uuid.Nil; claims carry the role to authorize on).
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

// requireUser pins a self-service call to a real user subject; service tokens (non-uuid
// sub) are rejected since these endpoints act on "my account".
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

// maxBodyBytes caps request bodies; every auth-service body is a small OAuth/token fragment, far under this.
const maxBodyBytes = 64 << 10
