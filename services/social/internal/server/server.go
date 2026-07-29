// Package server maps HTTP (generated ServerInterface) onto the social
// store, enforcing per-handler authorization from JWT claims. Shared
// vocabulary: a "shelf" is a collection saved view seen through the
// social layer. Writes that reference a shelf validate it through
// collection first (never accept unvalidated writes); follows
// validate the followee through the user service.
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
		ctr, err := m.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
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

func (h *Handlers) count(ctx context.Context, c metric.Int64Counter, key, val string) {
	if c == nil {
		return
	}
	c.Add(ctx, 1, metric.WithAttributes(attribute.String(key, val)))
}

// caller extracts the authenticated subject and raw bearer.
func (h *Handlers) caller(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	claims, ok := jwtauth.FromContext(r.Context())
	if !ok {
		problem(w, r, http.StatusUnauthorized, "missing_token", "no validated token in context")
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "token subject is not a user id", "err", err)
		problem(w, r, http.StatusInternalServerError, "internal", "bad subject")
		return uuid.Nil, "", false
	}
	return id, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), true
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
