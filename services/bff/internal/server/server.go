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

	"github.com/levonn-dev/vgkeep/libs/go/contract/collectionapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/socialapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
	"github.com/levonn-dev/vgkeep/services/bff/internal/socialclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
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
	ListIdentities(ctx context.Context, userID, bearer string) ([]common.Identity, error)
	DeleteIdentity(ctx context.Context, identityID uuid.UUID, bearer string) error
	DeleteUserAuth(ctx context.Context, userID, bearer string) error
}

// UserAPI is the user service surface (implemented by userclient).
type UserAPI interface {
	Get(ctx context.Context, id, bearer string) (userapi.User, error)
	Update(ctx context.Context, id, bearer string, body []byte) (userclient.Result, error)
	Delete(ctx context.Context, id, bearer string) error
	SharedProfile(ctx context.Context, bearer, handle string) (userapi.ProfileCard, error)
	SharedCardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]userapi.ProfileCard, error)
	SearchProfiles(ctx context.Context, bearer, q string) (userclient.Result, error)
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
	ListPlatforms(ctx context.Context, bearer string) (enrichmentclient.Result, error)
	UnmatchedProducts(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error)
	CommunityProducts(ctx context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error)
	SetProductMapping(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	DeleteProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error)
	TriggerRefresh(ctx context.Context, bearer string) (enrichmentclient.Result, error)
	CreateCommunityProduct(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error)
	PromoteProduct(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	PromoteCandidates(ctx context.Context, bearer string, params *enrichapi.ListPromoteCandidatesParams) (enrichmentclient.Result, error)
	DismissPromoteCandidate(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	NormalizeCommunityRegions(ctx context.Context, bearer string) (enrichmentclient.Result, error)
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
	AckRegionMismatch(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	ReorderEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)
	BulkUpdateEntries(ctx context.Context, bearer string, body []byte) (collectionclient.Result, error)
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
	CreateSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	GetSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	CancelSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	AckSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	ListSubmissions(ctx context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (collectionclient.Result, error)
	SubmitVerdict(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)
	TriggerRematch(ctx context.Context, bearer string) (collectionclient.Result, error)
	Resnapshot(ctx context.Context, bearer string) (collectionclient.Result, error)
	NormalizePlatforms(ctx context.Context, bearer string) (collectionclient.Result, error)
	NormalizeRegions(ctx context.Context, bearer string) (collectionclient.Result, error)
	SharedShelf(ctx context.Context, bearer string, id uuid.UUID) (collectionapi.SharedShelf, error)
	SharedShelfBySlug(ctx context.Context, bearer string, ownerID uuid.UUID, slug string) (collectionapi.SharedShelf, error)
	SharedShelfEntries(ctx context.Context, bearer string, id uuid.UUID, limit, offset *int) (collectionclient.Result, error)
	ListSharedShelves(ctx context.Context, bearer string, ownerIDs []uuid.UUID, limit, offset int) ([]collectionapi.SharedShelfSummary, int, error)
	SharedShelvesByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]collectionapi.SharedShelfSummary, error)
}

// SocialAPI is the social service surface (implemented by
// socialclient). Follow/Unfollow/Like/Unlike/ListComments/
// CreateComment/DeleteComment/PurgeUserData are verbatim relays; the
// rest are typed reads the bff consumes itself to compose the shared
// pages, the activity feed and Explore browsing, and the publish
// orchestration leg.
type SocialAPI interface {
	Follow(ctx context.Context, bearer string, userID uuid.UUID) (socialclient.Result, error)
	Unfollow(ctx context.Context, bearer string, userID uuid.UUID) (socialclient.Result, error)
	Like(ctx context.Context, bearer string, shelfID uuid.UUID) (socialclient.Result, error)
	Unlike(ctx context.Context, bearer string, shelfID uuid.UUID) (socialclient.Result, error)
	ListComments(ctx context.Context, bearer string, shelfID uuid.UUID, cursor *string, limit *int) (socialclient.Result, error)
	CreateComment(ctx context.Context, bearer string, shelfID uuid.UUID, body []byte) (socialclient.Result, error)
	DeleteComment(ctx context.Context, bearer string, commentID uuid.UUID) (socialclient.Result, error)
	ProfileSummary(ctx context.Context, bearer string, userID uuid.UUID) (socialapi.ProfileSocialSummary, error)
	ShelvesSummary(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.ShelfSocialSummary, error)
	CommentsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.Comment, error)
	Feed(ctx context.Context, bearer, tab string, cursor *string, limit int) (events []socialapi.ActivityEvent, nextCursor *string, err error)
	TopShelves(ctx context.Context, bearer string, limit int) ([]uuid.UUID, error)
	RecordPublish(ctx context.Context, bearer string, shelfID uuid.UUID) error
	PurgeUserData(ctx context.Context, bearer string) (socialclient.Result, error)
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
	social        SocialAPI
	logger        *slog.Logger
	accessTTL     time.Duration
	refreshWindow time.Duration
	meTTL         time.Duration
	recsTTL       time.Duration
	publicOrigins []string
	otlpProxyURL  string
	otlpHTTP      *http.Client
	failOpen      metric.Int64Counter
	logins        metric.Int64Counter
	refreshes     metric.Int64Counter
	cacheLookups  metric.Int64Counter

	// Test seams: clock and result-adoption pacing.
	now          func() time.Time
	pollInterval time.Duration
	pollBudget   time.Duration
}

// New builds a Handlers. The OTel meter is best-effort: a counter
// registration failure is logged but does not prevent startup.
func New(codec *session.Codec, cache SessionCache, auth AuthAPI, users UserAPI, enrichment EnrichmentAPI, collection CollectionAPI, social SocialAPI, opts Options) *Handlers {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	meter := otel.Meter("github.com/levonn-dev/vgkeep/services/bff")
	failOpen := vgotel.CounterLogged(meter, opts.Logger, "vg.bff.cache.fail_open",
		"Valkey operations that failed and were failed open", "{event}")
	logins := vgotel.CounterLogged(meter, opts.Logger, "vg.bff.auth.logins",
		"Completed login and account-link attempts by flow and outcome", "{login}")
	refreshes := vgotel.CounterLogged(meter, opts.Logger, "vg.bff.session.refreshes",
		"Session refresh attempts by terminal outcome", "{refresh}")
	cacheLookups := vgotel.CounterLogged(meter, opts.Logger, "vg.bff.cache.lookups",
		"Composition cache lookups (me, recs) by hit or miss", "{lookup}")
	return &Handlers{
		codec: codec, cache: cache, auth: auth, users: users, enrichment: enrichment, collection: collection, social: social,
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
		logins:       logins,
		refreshes:    refreshes,
		cacheLookups: cacheLookups,
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
	h.logger.ErrorContext(ctx, "dependency unavailable; failing open", "op", op, "err", err)
	vgotel.Count(ctx, h.failOpen, attribute.String("op", op))
}

// loginEvent counts one completed login or account-link attempt
// (flow: login|link; outcome vocabulary in the bff runbook). Redirects
// to an identity provider are not counted: that attempt completes at
// the callback.
func (h *Handlers) loginEvent(ctx context.Context, flow, outcome string) {
	vgotel.Count(ctx, h.logins, attribute.String("flow", flow), attribute.String("outcome", outcome))
}

// refreshEvent counts one session refresh attempt reaching a terminal
// outcome (vocabulary in the bff runbook).
func (h *Handlers) refreshEvent(ctx context.Context, outcome string) {
	vgotel.Count(ctx, h.refreshes, attribute.String("outcome", outcome))
}

// cacheLookupEvent counts a composition-cache lookup (cache: me|recs).
// A Valkey read error counts as miss (the composition runs); the
// caller fires failOpenEvent for the error itself.
func (h *Handlers) cacheLookupEvent(ctx context.Context, cache, outcome string) {
	vgotel.Count(ctx, h.cacheLookups, attribute.String("cache", cache), attribute.String("outcome", outcome))
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblemFields(w, r, status, code, detail)
}

func (h *Handlers) unauthorized(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r, http.StatusUnauthorized, "unauthenticated", "no valid session")
}

func (h *Handlers) clearAndUnauthorized(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, h.codec.ClearCookie())
	h.unauthorized(w, r)
}

// requireSession is the entry guard every session-gated handler runs
// first: it wraps session.FromContext and, on a miss, writes the 401
// itself so every call site collapses to a single ok check instead of
// repeating the unauthorized call. Mid-handler re-checks (an account
// that vanished after the prologue already passed, a cookie that needs
// clearing) call h.unauthorized or h.clearAndUnauthorized directly -
// requireSession is only for the handler's own opening guard.
func (h *Handlers) requireSession(w http.ResponseWriter, r *http.Request) (session.Session, session.Claims, bool) {
	sess, claims, ok := session.FromContext(r.Context())
	if !ok {
		h.unauthorized(w, r)
		return session.Session{}, session.Claims{}, false
	}
	return sess, claims, true
}

// writeRelay serves an upstream answer verbatim (pass-throughs are
// never cached at the bff: one staleness authority per data type).
func writeRelay(w http.ResponseWriter, status int, contentType string, body []byte) {
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// readCapped reads a pass-through body under the standard cap; a
// false return means the 400 was already written.
func readCapped(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return httpkit.ReadCapped(w, r, 64*1024)
}

// relayCollection funnels every collection pass-through: session
// check happened at the caller; any client error is an infrastructure
// fault answered 502.
func (h *Handlers) relayCollection(w http.ResponseWriter, r *http.Request, res collectionclient.Result, err error) {
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// relayEnrichment funnels every enrichment pass-through: session check
// happened at the caller; any client error is an infrastructure fault
// answered 502 (relayCollection's twin for the enrichment service).
func (h *Handlers) relayEnrichment(w http.ResponseWriter, r *http.Request, res enrichmentclient.Result, err error) {
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "enrichment service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// relayUser funnels every user-service pass-through: session check
// happened at the caller; any client error is an infrastructure fault
// answered 502 (relayCollection's twin for the user service).
func (h *Handlers) relayUser(w http.ResponseWriter, r *http.Request, res userclient.Result, err error) {
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

func writeJSON(w http.ResponseWriter, status int, v any) { httpkit.WriteJSON(w, status, v) }

func writeRawJSON(w http.ResponseWriter, body []byte) { httpkit.WriteRawJSON(w, http.StatusOK, body) }

var _ api.ServerInterface = (*Handlers)(nil)
