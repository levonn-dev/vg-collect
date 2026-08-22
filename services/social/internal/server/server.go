// Package server maps HTTP (generated ServerInterface) onto the social
// store, enforcing per-handler authorization from JWT claims. Shared
// vocabulary: a "shelf" is a collection saved view seen through the
// social layer. Writes that reference a shelf validate it through
// collection first (never accept unvalidated writes); follows
// validate the followee through the user service.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/services/social/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/social/internal/store"
	"github.com/levonn-dev/vgkeep/services/social/internal/userclient"
)

// Store is the persistence surface the handlers consume. Sentinels:
// store.ErrCapExceeded (429), store.ErrForbidden (403),
// store.ErrNotFound (404). Edge writes report whether this call
// inserted so the handlers can label outcomes without re-reading.
type Store interface {
	Follow(ctx context.Context, follower, followee uuid.UUID, cap int) (bool, error)
	Unfollow(ctx context.Context, follower, followee uuid.UUID) error
	ProfileSummary(ctx context.Context, userID, viewer uuid.UUID) (followers, following int, viewerFollows bool, err error)
	FolloweeIDs(ctx context.Context, follower uuid.UUID) ([]uuid.UUID, error)
	Like(ctx context.Context, user, shelf, shelfOwner uuid.UUID, cap int) (bool, error)
	Unlike(ctx context.Context, user, shelf uuid.UUID) error
	ShelfSummaries(ctx context.Context, ids []uuid.UUID, viewer uuid.UUID) ([]store.ShelfSummary, error)
	TopShelves(ctx context.Context, limit int) ([]uuid.UUID, error)
	CreateComment(ctx context.Context, shelf, shelfOwner, author uuid.UUID, body string, cap int) (store.Comment, error)
	ListLiveComments(ctx context.Context, shelf uuid.UUID, cursor *store.Cursor, limit int) ([]store.Comment, error)
	LiveCommentsByIDs(ctx context.Context, ids []uuid.UUID) ([]store.Comment, error)
	DeleteComment(ctx context.Context, id, caller uuid.UUID) (string, error)
	Feed(ctx context.Context, viewer uuid.UUID, tab string, cursor *store.Cursor, limit int) ([]store.Event, error)
	RecordPublish(ctx context.Context, actor, shelf uuid.UUID, throttle time.Duration) (string, error)
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

var _ Store = (*store.Store)(nil)

// Collection resolves shelves (write-time validation).
type Collection interface {
	SharedShelf(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Shelf, error)
}

// Users reads profile cards (followee validation).
type Users interface {
	CardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]userclient.Card, error)
}

// publishRefreshThrottle bounds feed bumps from visibility
// flip-flopping: one refresh per shelf per hour, a mechanism guard
// (deliberately not config).
const publishRefreshThrottle = time.Hour

// Options carries construction-time dependencies and the
// community-size cap dials.
type Options struct {
	Logger      *slog.Logger
	CapComments int
	CapFollows  int
	CapLikes    int
}

// Handlers owns the handler dependencies and domain counters.
type Handlers struct {
	store  Store
	col    Collection
	users  Users
	logger *slog.Logger
	opts   Options

	follows       metric.Int64Counter
	likes         metric.Int64Counter
	comments      metric.Int64Counter
	feedReads     metric.Int64Counter
	capRejections metric.Int64Counter
	publishEvents metric.Int64Counter
	purgeRuns     metric.Int64Counter
}

// New builds a Handlers. Counters are best-effort: registration
// failures log and every emission site nil-guards.
func New(st Store, col Collection, users Users, opts Options) *Handlers {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	m := otel.Meter("github.com/levonn-dev/vgkeep/services/social")
	counter := func(name, desc, unit string) metric.Int64Counter {
		ctr, err := vgotel.Counter(m, name, desc, unit)
		if err != nil {
			opts.Logger.Error("counter unavailable", "name", name, "err", err)
		}
		return ctr
	}
	return &Handlers{
		store: st, col: col, users: users, logger: opts.Logger, opts: opts,
		follows: counter("vg.social.follows",
			"Follow edge writes by op (create or delete)", "{op}"),
		likes: counter("vg.social.likes",
			"Like edge writes by op (create or delete)", "{op}"),
		comments: counter("vg.social.comments",
			"Comment lifecycle ops (create, self_delete, owner_delete)", "{op}"),
		feedReads: counter("vg.social.feed.reads",
			"Feed page reads by tab", "{read}"),
		capRejections: counter("vg.social.caps.rejections",
			"Rate-cap rejections by kind (comments, follows, likes)", "{rejection}"),
		publishEvents: counter("vg.social.publish.events",
			"Shelf publish records by outcome (created, refreshed, throttled)", "{event}"),
		purgeRuns: counter("vg.social.purge.runs",
			"Account-deletion purges", "{run}"),
	}
}

// count is kept as a named method (rather than inlining vgotel.Count
// at each of its call sites in handlers.go) so every social handler
// keeps counting through one instrument-plus-attribute call shape;
// the nil guard and the Add itself now live in vgotel.Count, the
// shared emission helper this method's own shape was lifted into.
func (h *Handlers) count(ctx context.Context, c metric.Int64Counter, key, val string) {
	vgotel.Count(ctx, c, attribute.String(key, val))
}

// caller extracts the authenticated subject and raw bearer.
func (h *Handlers) caller(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	return jwtauth.CallerID(w, r, problemEW)
}

func writeJSON(w http.ResponseWriter, status int, v any) { httpkit.WriteJSON(w, status, v) }
func problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	httpkit.WriteProblemFields(w, r, status, code, detail)
}

// internalError answers a 500 and logs its cause: op is the log's
// stable "op" label, detail the response's human-readable text - the
// two already varied independently across the fourteen inline copies
// this collapses (e.g. op "list_comments" against detail "list
// failed"). Same shape as collection's h.internalError and
// enrichment's twin.
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, op, detail string, err error) {
	h.logger.ErrorContext(r.Context(), "store error", "op", op, "err", err)
	problem(w, r, http.StatusInternalServerError, "internal", detail)
}

// capExceeded counts one rate-cap rejection on capRejections and
// answers 429. Follow, LikeShelf, and CreateShelfComment reach this
// identical branch, differing only in kind (the counter's label) and
// the noun in detail; kept as a literal per-kind switch rather than
// derived from kind's plural spelling, so a future kind cannot go
// stale by silently mismatching its message.
func (h *Handlers) capExceeded(w http.ResponseWriter, r *http.Request, kind string) {
	h.count(r.Context(), h.capRejections, "kind", kind)
	var detail string
	switch kind {
	case "follows":
		detail = "follow limit reached; try again later"
	case "likes":
		detail = "like limit reached; try again later"
	case "comments":
		detail = "comment limit reached; try again later"
	}
	problem(w, r, http.StatusTooManyRequests, "cap_exceeded", detail)
}

// maxBodyBytes caps request bodies; every social-service body is a
// small comment or event fragment, far under this.
const maxBodyBytes = 64 << 10
