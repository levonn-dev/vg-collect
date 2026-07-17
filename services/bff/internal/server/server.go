// Package server owns the bff's HTTP surface: the session middleware
// (allowlist, denylist, transparent refresh), CSRF origin checks,
// security headers, and the browser-facing handlers.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/authapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/enrichapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/session"
	"github.com/levonn-dev/vg-collect/services/bff/internal/userclient"
)

// SessionCache is the Valkey surface the server needs (implemented by
// the cache package, stubs in tests). Errors mean "Valkey is having a
// moment"; each call site decides its own fail-open behavior.
type SessionCache interface {
	DenylistAdd(ctx context.Context, jtis []string, ttl time.Duration) error
	DenylistHas(ctx context.Context, jti string) (bool, error)
	AcquireRefreshLock(ctx context.Context, key, holder string, ttl time.Duration) (bool, error)
	ReleaseRefreshLock(ctx context.Context, key, holder string) error
	PutRefreshResult(ctx context.Context, key, sealed string, ttl time.Duration) error
	GetRefreshResult(ctx context.Context, key string) (string, error)
	GetMe(ctx context.Context, sub string) ([]byte, error)
	PutMe(ctx context.Context, sub string, body []byte, ttl time.Duration) error
	GetRecs(ctx context.Context, sub string) ([]byte, error)
	PutRecs(ctx context.Context, sub string, body []byte, ttl time.Duration) error
	InvalidateRecs(ctx context.Context, sub string) error
	InvalidateMe(ctx context.Context, sub string) error
}

// AuthAPI is the auth service surface (implemented by authclient).
type AuthAPI interface {
	Start(ctx context.Context, provider string) (string, error)
	Callback(ctx context.Context, code, state string) (authclient.TokenPair, error)
	DevToken(ctx context.Context, user string) (authclient.TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (authclient.TokenPair, error)
	Revoke(ctx context.Context, refreshToken string) error
	Providers(ctx context.Context) ([]string, error)
	LinkStart(ctx context.Context, provider, bearer string) (string, error)
	DevLink(ctx context.Context, user, bearer string) (authclient.TokenPair, error)
	ListIdentities(ctx context.Context, userID, bearer string) ([]authapi.Identity, error)
	DeleteIdentity(ctx context.Context, identityID uuid.UUID, bearer string) error
	DeleteUserAuth(ctx context.Context, userID, bearer string) error
}

// UserAPI is the user service surface (implemented by userclient).
type UserAPI interface {
	Get(ctx context.Context, id, bearer string) (userapi.User, error)
	Update(ctx context.Context, id, bearer string, body []byte) (userclient.Result, error)
	Delete(ctx context.Context, id, bearer string) error
}

// EnrichmentAPI is the enrichment service surface (implemented by
// enrichmentclient). Answers are verbatim relays: Result carries the
// upstream status, content type, and body for the statuses the bff
// serves as-is.
type EnrichmentAPI interface {
	Search(ctx context.Context, bearer, typ, q string) (enrichmentclient.Result, error)
	Resolve(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error)
	Product(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error)
	Score(ctx context.Context, bearer string, req enrichapi.ScoreRequest) ([]byte, bool, error)
	FX(ctx context.Context, bearer string) (enrichmentclient.Result, error)
	UnmatchedProducts(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error)
	SetProductMapping(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	DeleteProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error)
	TriggerRefresh(ctx context.Context, bearer string) (enrichmentclient.Result, error)
}

// CollectionAPI is the collection service surface (implemented by
// collectionclient). Answers are verbatim relays except
// LibrarySummary, which the bff consumes itself.
type CollectionAPI interface {
	ListEntries(ctx context.Context, bearer string, params *collectionapi.ListEntriesParams) (collectionclient.Result, error)
	CreateEntry(ctx context.Context, bearer string, body []byte) (collectionclient.Result, error)
	GetEntry(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	UpdateEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)
	DeleteEntry(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	ReorderEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)
	ListTags(ctx context.Context, bearer string) (collectionclient.Result, error)
	CreateTag(ctx context.Context, bearer string, body []byte) (collectionclient.Result, error)
	RenameTag(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)
	DeleteTag(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	ListViews(ctx context.Context, bearer string) (collectionclient.Result, error)
	CreateView(ctx context.Context, bearer string, body []byte) (collectionclient.Result, error)
	UpdateView(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)
	DeleteView(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	GetDashboard(ctx context.Context, bearer string, params *collectionapi.GetDashboardParams) (collectionclient.Result, error)
	GetValueHistory(ctx context.Context, bearer string) (collectionclient.Result, error)
	LibrarySummary(ctx context.Context, bearer string) (collectionapi.LibrarySummary, error)
	PurgeUserData(ctx context.Context, bearer string) (collectionclient.Result, error)
	CountProductReferences(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
}

const (
	// lockTTL caps how long a crashed rotation can block others.
	lockTTL = 10 * time.Second
	// resultTTL is how long a published rotation result stays adoptable
	// by a concurrent or slightly-late request still bearing the
	// pre-rotation token. It must exceed the in-flight lifetime of such a
	// request (bounded by client and proxy timeouts) so a late arrival
	// adopts the successor instead of re-refreshing the consumed token;
	// keep it above the gateway's maximum request timeout.
	resultTTL = 60 * time.Second
)

// Options carries tunables that vary between environments.
type Options struct {
	// AccessTokenTTL must match the auth service's access-token TTL;
	// bounds denylist entry lifetimes.
	AccessTokenTTL time.Duration
	// RefreshWindow: refresh starts when less than this remains on the
	// access token.
	RefreshWindow time.Duration
	MeCacheTTL    time.Duration
	// RecsCacheTTL bounds how long a composed /api/recommendations
	// answer stays valid before the next request recomposes it (the
	// caller's own entry mutations invalidate it sooner).
	RecsCacheTTL time.Duration
	// PublicOrigins are the origins allowed to send mutating requests.
	PublicOrigins []string
	// OTLPProxyURL is the collector agent's OTLP/HTTP base URL for the
	// browser telemetry relay. Empty disables the relay (payloads are
	// accepted and dropped).
	OTLPProxyURL string
	Logger       *slog.Logger
}

// Handlers owns the codec, backing services, and tunable knobs for
// every HTTP handler in the bff.
type Handlers struct {
	codec         *session.Codec
	cache         SessionCache
	auth          AuthAPI
	users         UserAPI
	enrichment    EnrichmentAPI
	collection    CollectionAPI
	logger        *slog.Logger
	accessTTL     time.Duration
	refreshWindow time.Duration
	meTTL         time.Duration
	recsTTL       time.Duration
	publicOrigins []string
	otlpProxyURL  string
	otlpHTTP      *http.Client
	failOpen      metric.Int64Counter

	// Test seams: clock and result-adoption pacing.
	now          func() time.Time
	pollInterval time.Duration
	pollBudget   time.Duration
}

// New builds a Handlers. The OTel meter is best-effort: a counter
// registration failure is logged but does not prevent startup.
func New(codec *session.Codec, cache SessionCache, auth AuthAPI, users UserAPI, enrichment EnrichmentAPI, collection CollectionAPI, opts Options) *Handlers {
	failOpen, err := otel.Meter("github.com/levonn-dev/vg-collect/services/bff").
		Int64Counter("vg.bff.cache.fail_open",
			metric.WithDescription("Valkey operations that failed and were failed open"))
	if err != nil {
		// A telemetry hiccup must not stop logins; failOpenEvent
		// guards the nil.
		opts.Logger.Error("fail-open counter unavailable", "err", err)
	}
	return &Handlers{
		codec: codec, cache: cache, auth: auth, users: users, enrichment: enrichment, collection: collection,
		logger:        opts.Logger,
		accessTTL:     opts.AccessTokenTTL,
		refreshWindow: opts.RefreshWindow,
		meTTL:         opts.MeCacheTTL,
		recsTTL:       opts.RecsCacheTTL,
		publicOrigins: opts.PublicOrigins,
		otlpProxyURL:  opts.OTLPProxyURL,
		otlpHTTP: &http.Client{
			Timeout:   10 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		failOpen:     failOpen,
		now:          time.Now,
		pollInterval: 100 * time.Millisecond,
		// pollBudget is deliberately shorter than the auth refresh timeout:
		// a waiter returns 401 promptly and the browser's retry adopts the late result.
		pollBudget: 3 * time.Second,
	}
}

// failOpenEvent records a Valkey failure that the caller is about to
// fail open on (log + metric; alerting watches the metric).
func (h *Handlers) failOpenEvent(ctx context.Context, op string, err error) {
	h.logger.ErrorContext(ctx, "valkey unavailable; failing open", "op", op, "err", err)
	if h.failOpen != nil {
		h.failOpen.Add(ctx, 1, metric.WithAttributes(attribute.String("op", op)))
	}
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblem(w, r, httpkit.Problem{
		Status: status, Title: http.StatusText(status), Code: code, Detail: detail,
	})
}

func (h *Handlers) unauthorized(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r, http.StatusUnauthorized, "unauthenticated", "no valid session")
}

func (h *Handlers) clearAndUnauthorized(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, h.codec.ClearCookie())
	h.unauthorized(w, r)
}
