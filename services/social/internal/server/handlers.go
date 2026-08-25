package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/social/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/social/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/social/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

// GetCommentsByIds and GetShelvesSocialSummary cap ids at maxItems:
// 100 (api/social.yaml), enforced by specval before these handlers run.

func (h *Handlers) Follow(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	me, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	if userId == me {
		problem(w, r, http.StatusBadRequest, "self_follow", "you cannot follow yourself")
		return
	}
	cards, err := h.users.CardsByIDs(r.Context(), bearer, []uuid.UUID{userId})
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	if len(cards) == 0 || cards[0].Visibility == "private" {
		problem(w, r, http.StatusNotFound, "profile_not_found", "no such profile")
		return
	}
	inserted, err := h.store.Follow(r.Context(), me, userId, h.opts.CapFollows)
	if errors.Is(err, store.ErrCapExceeded) {
		h.capExceeded(w, r, "follows")
		return
	}
	if err != nil {
		h.internalError(w, r, "follow", "follow failed", err)
		return
	}
	if inserted {
		h.count(r.Context(), h.follows, "op", "create")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) Unfollow(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	me, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if err := h.store.Unfollow(r.Context(), me, userId); err != nil {
		h.internalError(w, r, "unfollow", "unfollow failed", err)
		return
	}
	h.count(r.Context(), h.follows, "op", "delete")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) GetProfileSocialSummary(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	me, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	followers, following, viewerFollows, err := h.store.ProfileSummary(r.Context(), userId, me)
	if err != nil {
		h.internalError(w, r, "profile_summary", "summary failed", err)
		return
	}
	writeJSON(w, http.StatusOK, api.ProfileSocialSummary{
		FollowerCount: followers, FollowingCount: following, ViewerFollows: viewerFollows,
	})
}

// LikeShelf 404s on a private owner too, matching Follow: the bff relays
// this mutation without re-running its own visibility gate.
func (h *Handlers) LikeShelf(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	me, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	shelf, err := h.col.SharedShelf(r.Context(), bearer, shelfId)
	if errors.Is(err, collectionclient.ErrShelfNotFound) {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	cards, err := h.users.CardsByIDs(r.Context(), bearer, []uuid.UUID{shelf.OwnerID})
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	if len(cards) == 0 || cards[0].Visibility == "private" {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	inserted, err := h.store.Like(r.Context(), me, shelf.ID, shelf.OwnerID, h.opts.CapLikes)
	if errors.Is(err, store.ErrCapExceeded) {
		h.capExceeded(w, r, "likes")
		return
	}
	if err != nil {
		h.internalError(w, r, "like", "like failed", err)
		return
	}
	if inserted {
		h.count(r.Context(), h.likes, "op", "create")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) UnlikeShelf(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	me, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if err := h.store.Unlike(r.Context(), me, shelfId); err != nil {
		h.internalError(w, r, "unlike", "unlike failed", err)
		return
	}
	h.count(r.Context(), h.likes, "op", "delete")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) GetShelvesSocialSummary(w http.ResponseWriter, r *http.Request, params api.GetShelvesSocialSummaryParams) {
	me, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	ids := make([]uuid.UUID, len(params.Ids))
	copy(ids, params.Ids)
	sums, err := h.store.ShelfSummaries(r.Context(), ids, me)
	if err != nil {
		h.internalError(w, r, "shelf_summaries", "summaries failed", err)
		return
	}
	out := make([]api.ShelfSocialSummary, len(sums))
	for i, x := range sums {
		out[i] = api.ShelfSocialSummary{
			ShelfId: x.ShelfID, LikeCount: x.LikeCount,
			CommentCount: x.CommentCount, ViewerLikes: x.ViewerLikes,
		}
	}
	writeJSON(w, http.StatusOK, map[string][]api.ShelfSocialSummary{"summaries": out})
}

func toAPIComment(c store.Comment) api.Comment {
	return api.Comment{
		Id: c.ID, ShelfId: c.ShelfID, AuthorId: *c.AuthorID,
		Body: *c.Body, CreatedAt: c.CreatedAt,
	}
}

func parseCursorParam(w http.ResponseWriter, r *http.Request, raw *string) (*store.Cursor, bool) {
	if raw == nil || *raw == "" {
		return nil, true
	}
	cur, err := store.ParseCursor(*raw)
	if err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_param", "malformed cursor")
		return nil, false
	}
	return cur, true
}

func (h *Handlers) ListShelfComments(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID, params api.ListShelfCommentsParams) {
	_, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	cur, ok := parseCursorParam(w, r, params.Cursor)
	if !ok {
		return
	}
	// specval enforces bounds (1-50); this only fills the default-when-absent
	// gap, since the generated param binder skips schema defaults.
	limit := 20
	if params.Limit != nil {
		limit = *params.Limit
	}
	comments, err := h.store.ListLiveComments(r.Context(), shelfId, cur, limit)
	if err != nil {
		h.internalError(w, r, "list_comments", "list failed", err)
		return
	}
	out := struct {
		Comments   []api.Comment `json:"comments"`
		NextCursor *string       `json:"next_cursor,omitempty"`
	}{Comments: make([]api.Comment, len(comments))}
	for i, c := range comments {
		out.Comments[i] = toAPIComment(c)
	}
	if len(comments) == limit {
		s := (store.Cursor{CreatedAt: comments[len(comments)-1].CreatedAt, ID: comments[len(comments)-1].ID}).String()
		out.NextCursor = &s
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) CreateShelfComment(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	me, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	var req api.CreateShelfCommentJSONRequestBody
	if !httpkit.DecodeBody(w, r, maxBodyBytes, &req) {
		return
	}
	// specval's minLength(1) counts raw chars, so whitespace-only bodies
	// pass; TrimSpace catches those. maxLength(2000) is fully specval's job.
	body := strings.TrimSpace(req.Body)
	if body == "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "body must not be blank")
		return
	}
	shelf, err := h.col.SharedShelf(r.Context(), bearer, shelfId)
	if errors.Is(err, collectionclient.ErrShelfNotFound) {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	c, err := h.store.CreateComment(r.Context(), shelf.ID, shelf.OwnerID, me, body, h.opts.CapComments)
	if errors.Is(err, store.ErrCapExceeded) {
		h.capExceeded(w, r, "comments")
		return
	}
	if err != nil {
		h.internalError(w, r, "create_comment", "comment failed", err)
		return
	}
	h.count(r.Context(), h.comments, "op", "create")
	writeJSON(w, http.StatusCreated, toAPIComment(c))
}

func (h *Handlers) DeleteComment(w http.ResponseWriter, r *http.Request, commentId openapi_types.UUID) {
	me, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	outcome, err := h.store.DeleteComment(r.Context(), commentId, me)
	if errors.Is(err, store.ErrForbidden) {
		problem(w, r, http.StatusForbidden, "forbidden", "only the author or the shelf owner may delete")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "comment_not_found", "no such comment")
		return
	}
	if err != nil {
		h.internalError(w, r, "delete_comment", "delete failed", err)
		return
	}
	h.count(r.Context(), h.comments, "op", outcome)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) GetCommentsByIds(w http.ResponseWriter, r *http.Request, params api.GetCommentsByIdsParams) {
	_, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	ids := make([]uuid.UUID, len(params.Ids))
	copy(ids, params.Ids)
	comments, err := h.store.LiveCommentsByIDs(r.Context(), ids)
	if err != nil {
		h.internalError(w, r, "comments_by_ids", "batch failed", err)
		return
	}
	out := make([]api.Comment, len(comments))
	for i, c := range comments {
		out[i] = toAPIComment(c)
	}
	writeJSON(w, http.StatusOK, map[string][]api.Comment{"comments": out})
}

func (h *Handlers) GetFeed(w http.ResponseWriter, r *http.Request, params api.GetFeedParams) {
	me, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	// tab enum ([following, you], common.yaml) is validated by specval.
	cur, ok := parseCursorParam(w, r, params.Cursor)
	if !ok {
		return
	}
	// Bounds are now specval's job; only the default-when-absent fill
	// stays (see ListShelfComments).
	limit := 20
	if params.Limit != nil {
		limit = *params.Limit
	}
	events, err := h.store.Feed(r.Context(), me, string(params.Tab), cur, limit)
	if err != nil {
		h.internalError(w, r, "feed", "feed failed", err)
		return
	}
	h.count(r.Context(), h.feedReads, "tab", string(params.Tab))
	out := struct {
		Events     []api.ActivityEvent `json:"events"`
		NextCursor *string             `json:"next_cursor,omitempty"`
	}{Events: make([]api.ActivityEvent, len(events))}
	for i, e := range events {
		out.Events[i] = api.ActivityEvent{
			Id: e.ID, ActorId: e.ActorID, Verb: common.ActivityVerb(e.Verb),
			ObjectShelfId: e.ObjectShelfID, ObjectCommentId: e.ObjectCommentID,
			TargetUserId: e.TargetUserID, CreatedAt: e.CreatedAt,
		}
	}
	// next_cursor tracks the raw stream position, not page boundaries: the
	// bff filters by visibility after fetching, so even a short page needs
	// a cursor to resume from; omitted only when the raw stream is exhausted.
	if len(events) > 0 {
		s := (store.Cursor{CreatedAt: events[len(events)-1].CreatedAt, ID: events[len(events)-1].ID}).String()
		out.NextCursor = &s
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) RecordShelfPublished(w http.ResponseWriter, r *http.Request) {
	me, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	var req api.RecordShelfPublishedJSONRequestBody
	if !httpkit.DecodeBody(w, r, maxBodyBytes, &req) {
		return
	}
	if req.ShelfId == uuid.Nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "shelf_id required")
		return
	}
	shelf, err := h.col.SharedShelf(r.Context(), bearer, req.ShelfId)
	if errors.Is(err, collectionclient.ErrShelfNotFound) {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	// Unreachable via the bff (it always sends the owner's own bearer), but
	// a non-owner still gets the same shelf_not_found 404, not a new oracle.
	if me != shelf.OwnerID {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	outcome, err := h.store.RecordPublish(r.Context(), shelf.OwnerID, shelf.ID, publishRefreshThrottle)
	if err != nil {
		h.internalError(w, r, "record_publish", "record failed", err)
		return
	}
	h.count(r.Context(), h.publishEvents, "outcome", outcome)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) GetTopShelves(w http.ResponseWriter, r *http.Request, params api.GetTopShelvesParams) {
	_, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	// Bounds are now specval's job; only the default-when-absent fill
	// stays (see ListShelfComments).
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	ids, err := h.store.TopShelves(r.Context(), limit)
	if err != nil {
		h.internalError(w, r, "top_shelves", "leaderboard failed", err)
		return
	}
	out := make([]openapi_types.UUID, len(ids))
	copy(out, ids)
	writeJSON(w, http.StatusOK, map[string][]openapi_types.UUID{"shelf_ids": out})
}

func (h *Handlers) PurgeUserData(w http.ResponseWriter, r *http.Request) {
	me, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if err := h.store.PurgeUser(r.Context(), me); err != nil {
		h.internalError(w, r, "purge", "purge failed", err)
		return
	}
	h.count(r.Context(), h.purgeRuns, "outcome", "ok")
	w.WriteHeader(http.StatusNoContent)
}
