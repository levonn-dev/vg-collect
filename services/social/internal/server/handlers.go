package server

import (
	"encoding/json"
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

// GetCommentsByIds' and GetShelvesSocialSummary's ids params both
// declare maxItems: 100 in api/social.yaml; specval's
// request-validation middleware enforces that bound ahead of these
// handlers.

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

// LikeShelf also gates on the shelf owner's profile visibility,
// mirroring Follow: a private owner must 404 exactly like a missing
// or shelf-private shelf, since the bff relays this mutation without
// re-running its effectiveShelf gate.
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
	// Bounds (minimum 1, maximum 50) are now specval's job; only the
	// default-when-absent fill (the generated param binder does not
	// apply schema defaults) stays here.
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
	// minLength(1) is specval's job, but it cannot reject a
	// whitespace-only body (minLength counts raw characters);
	// TrimSpace catches what specval cannot. maxLength(2000) is
	// entirely specval's job, so this only fires on the
	// blank-after-trim case.
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
	// tab's enum membership (common.yaml's shared tab parameter:
	// [following, you]) is now specval's job.
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
	// Unlike ListShelfComments' page-boundary cursor, next_cursor here
	// tracks how far the RAW stream was read for the bff's fill loop
	// (it filters by visibility after fetching, so a short raw page
	// still needs a cursor to resume from); only a truly empty page -
	// the raw stream itself exhausted - omits it.
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
	var req struct {
		ShelfID uuid.UUID `json:"shelf_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ShelfID == uuid.Nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "shelf_id required")
		return
	}
	shelf, err := h.col.SharedShelf(r.Context(), bearer, req.ShelfID)
	if errors.Is(err, collectionclient.ErrShelfNotFound) {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	// Defense in depth: the bff only ever calls this endpoint with the
	// shelf owner's own bearer (publishIfListed fires off that
	// caller's own successful view write), so this gate is
	// unreachable through the current call graph - but a caller
	// recording a publish for a shelf they do not own must not be
	// trusted regardless. Same posture as Follow/Like's
	// owner-visibility gate: answer the IDENTICAL shelf_not_found 404
	// the missing-shelf branch above already emits, so a mismatch is
	// never a new oracle for shelf existence.
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
