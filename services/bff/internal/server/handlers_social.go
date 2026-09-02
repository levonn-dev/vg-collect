package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/collectionapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/socialapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/socialclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

// profilePageShelvesLimit caps the shelf list embedded in a profile page;
// dedicated feed/Explore browse routes page further, not bound by it.
const profilePageShelvesLimit = 50

// effectiveShelf resolves a shelf and its owner under the two-sided
// visibility rule; unknown, private-shelf, and private-owner all converge on the same 404.
func (h *Handlers) effectiveShelf(w http.ResponseWriter, r *http.Request, bearer string, shelfID uuid.UUID) (collectionapi.SharedShelf, userapi.ProfileCard, bool) {
	shelf, err := h.collection.SharedShelf(r.Context(), bearer, shelfID)
	if errors.Is(err, collectionclient.ErrShelfNotFound) {
		writeProblem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return collectionapi.SharedShelf{}, userapi.ProfileCard{}, false
	}
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return collectionapi.SharedShelf{}, userapi.ProfileCard{}, false
	}
	cards, err := h.users.SharedCardsByIDs(r.Context(), bearer, []uuid.UUID{shelf.OwnerId})
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return collectionapi.SharedShelf{}, userapi.ProfileCard{}, false
	}
	if len(cards) == 0 || cards[0].ProfileVisibility == "private" {
		writeProblem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return collectionapi.SharedShelf{}, userapi.ProfileCard{}, false
	}
	return shelf, cards[0], true
}

// toProfileCard maps the cross-user projection onto the bff's wire type (same shape, different package).
func toProfileCard(c userapi.ProfileCard) api.ProfileCard {
	return api.ProfileCard{
		UserId: c.UserId, Handle: c.Handle, AvatarUrl: c.AvatarUrl,
		ProfileVisibility: common.Visibility(c.ProfileVisibility),
	}
}

// toShelfMeta maps a resolved shelf's identity and stored view state (owner/social are ShelfPage's sibling fields).
func toShelfMeta(shelf collectionapi.SharedShelf) api.ShelfMeta {
	return api.ShelfMeta{
		Id: shelf.Id, Name: shelf.Name, Slug: shelf.Slug,
		Params: shelf.Params, PublishedAt: shelf.PublishedAt,
	}
}

func toShelfSocialSummary(s socialapi.ShelfSocialSummary) api.ShelfSocialSummary {
	return api.ShelfSocialSummary(s)
}

func toProfileSocialSummary(s socialapi.ProfileSocialSummary) api.ProfileSocialSummary {
	return api.ProfileSocialSummary(s)
}

// toShelfCard maps a shelf summary, owner, and optional social counts onto
// the card type (shared by profile-page/feed/Explore); nil social means all three fields stay absent.
func toShelfCard(summary collectionapi.SharedShelfSummary, owner userapi.ProfileCard, social *socialapi.ShelfSocialSummary) api.ShelfCard {
	card := api.ShelfCard{
		Id: summary.Id, Name: summary.Name, Slug: summary.Slug, Owner: toProfileCard(owner),
		PublishedAt: summary.PublishedAt, EntryCount: summary.EntryCount, CoverUrls: summary.CoverUrls,
	}
	if social != nil {
		card.LikeCount = &social.LikeCount
		card.CommentCount = &social.CommentCount
		card.ViewerLikes = &social.ViewerLikes
	}
	return card
}

// composeShelfPage builds and writes a ShelfPage for an already-resolved,
// already-visibility-checked shelf/owner, shared by GetShelfPage and GetProfileShelfPage.
func (h *Handlers) composeShelfPage(w http.ResponseWriter, r *http.Request, bearer string, shelf collectionapi.SharedShelf, owner userapi.ProfileCard) {
	page := api.ShelfPage{Shelf: toShelfMeta(shelf), Owner: toProfileCard(owner)}
	summaries, err := h.social.ShelvesSummary(r.Context(), bearer, []uuid.UUID{shelf.Id})
	if err != nil {
		h.failOpenEvent(r.Context(), "social_summary", err)
	} else if len(summaries) > 0 {
		page.SocialAvailable = true
		social := toShelfSocialSummary(summaries[0])
		page.Social = &social
	}
	writeJSON(w, http.StatusOK, page)
}

// GetShelfPage composes a shared shelf's page by id.
func (h *Handlers) GetShelfPage(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	shelf, owner, ok := h.effectiveShelf(w, r, sess.AccessToken, shelfId)
	if !ok {
		return
	}
	h.composeShelfPage(w, r, sess.AccessToken, shelf, owner)
}

// GetProfileShelfPage resolves a shelf by (owner handle, slug), composing
// the same page as GetShelfPage. Every miss - unknown handle, private owner,
// unknown slug, private shelf - answers the same 404: a distinct profile_not_found would leak handle existence.
func (h *Handlers) GetProfileShelfPage(w http.ResponseWriter, r *http.Request, handle string, slug string) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	bearer := sess.AccessToken
	owner, err := h.users.SharedProfile(r.Context(), bearer, handle)
	switch {
	case errors.Is(err, userclient.ErrProfileNotFound):
		writeProblem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	case err != nil:
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	if owner.ProfileVisibility == common.Private {
		writeProblem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	shelf, err := h.collection.SharedShelfBySlug(r.Context(), bearer, owner.UserId, slug)
	switch {
	case errors.Is(err, collectionclient.ErrShelfNotFound):
		writeProblem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	case err != nil:
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	h.composeShelfPage(w, r, bearer, shelf, owner)
}

// sharedShelfIDs extracts shelf ids for a social-summary batch read.
func sharedShelfIDs(shelves []collectionapi.SharedShelfSummary) []uuid.UUID {
	ids := make([]uuid.UUID, len(shelves))
	for i, s := range shelves {
		ids[i] = s.Id
	}
	return ids
}

// socialForProfile composes a profile page's social tail: the profile and
// per-shelf summaries succeed or fail together; available false means the caller leaves both unset.
func (h *Handlers) socialForProfile(ctx context.Context, bearer string, ownerID uuid.UUID, shelfIDs []uuid.UUID) (socialapi.ProfileSocialSummary, map[uuid.UUID]*socialapi.ShelfSocialSummary, bool) {
	profileSummary, err := h.social.ProfileSummary(ctx, bearer, ownerID)
	if err != nil {
		h.failOpenEvent(ctx, "social_summary", err)
		return socialapi.ProfileSocialSummary{}, nil, false
	}
	byID := map[uuid.UUID]*socialapi.ShelfSocialSummary{}
	if len(shelfIDs) > 0 {
		summaries, err := h.social.ShelvesSummary(ctx, bearer, shelfIDs)
		if err != nil {
			h.failOpenEvent(ctx, "social_summary", err)
			return socialapi.ProfileSocialSummary{}, nil, false
		}
		for i := range summaries {
			s := summaries[i]
			byID[s.ShelfId] = &s
		}
	}
	return profileSummary, byID, true
}

// GetProfilePage composes a public profile: card, up to profilePageShelvesLimit
// shelves, and social counts; owners see exactly what visitors see.
func (h *Handlers) GetProfilePage(w http.ResponseWriter, r *http.Request, handle string) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	bearer := sess.AccessToken
	owner, err := h.users.SharedProfile(r.Context(), bearer, handle)
	switch {
	case errors.Is(err, userclient.ErrProfileNotFound):
		writeProblem(w, r, http.StatusNotFound, "profile_not_found", "no such profile")
		return
	case err != nil:
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	if owner.ProfileVisibility == common.Private {
		writeProblem(w, r, http.StatusNotFound, "profile_not_found", "no such profile")
		return
	}

	shelves, totalCount, err := h.collection.ListSharedShelves(r.Context(), bearer, []uuid.UUID{owner.UserId}, profilePageShelvesLimit, 0)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}

	page := api.ProfilePage{Profile: toProfileCard(owner), Shelves: make([]api.ShelfCard, len(shelves)), TotalCount: totalCount}
	profileSummary, shelfSummaries, available := h.socialForProfile(r.Context(), bearer, owner.UserId, sharedShelfIDs(shelves))
	page.SocialAvailable = available
	if available {
		social := toProfileSocialSummary(profileSummary)
		page.Social = &social
	}
	for i, s := range shelves {
		page.Shelves[i] = toShelfCard(s, owner, shelfSummaries[s.Id])
	}
	writeJSON(w, http.StatusOK, page)
}

// ListShelfEntries relays a shelf's whitelisted entries after the same effective-visibility check as any shelf sub-route.
func (h *Handlers) ListShelfEntries(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID, params api.ListShelfEntriesParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if _, _, ok := h.effectiveShelf(w, r, sess.AccessToken, shelfId); !ok {
		return
	}
	res, err := h.collection.SharedShelfEntries(r.Context(), sess.AccessToken, shelfId, params.Limit, params.Offset)
	h.relayCollection(w, r, res, err)
}

// ListShelfComments composes live comments after the effective-visibility
// check: author cards hydrate in ONE SharedCardsByIDs call over distinct ids
// (mirrors hydrateFeed). Non-200 from social relays verbatim; cursor/limit pass through.
func (h *Handlers) ListShelfComments(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID, params api.ListShelfCommentsParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if _, _, ok := h.effectiveShelf(w, r, sess.AccessToken, shelfId); !ok {
		return
	}
	res, err := h.social.ListComments(r.Context(), sess.AccessToken, shelfId, params.Cursor, params.Limit)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
		return
	}
	if res.Status != http.StatusOK {
		writeRelay(w, res.Status, res.ContentType, res.Body)
		return
	}
	h.composeCommentsPage(w, r, sess.AccessToken, res.Body)
}

// rawComment is a live comment row exactly as social's page serializes it, decoded locally not via socialapi.
type rawComment struct {
	Id        uuid.UUID `json:"id"`
	ShelfId   uuid.UUID `json:"shelf_id"`
	AuthorId  uuid.UUID `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// rawCommentsPage is social's ListShelfComments 200 body, decoded locally
// so composeCommentsPage can hydrate it before recomposing the bff's CommentList.
type rawCommentsPage struct {
	Comments   []rawComment `json:"comments"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

// dedupedIDs collects each item's id once, first-seen order; a non-nil keep
// skips (not just dedups) ids failing it - used only to drop uuid.Nil (purged
// comment authors) from the batch. Other call sites pass nil keep.
func dedupedIDs[T any](items []T, idOf func(T) uuid.UUID, keep func(uuid.UUID) bool) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(items))
	seen := make(map[uuid.UUID]bool, len(items))
	for _, item := range items {
		id := idOf(item)
		if keep != nil && !keep(id) {
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// indexByID maps each item under idOf's key; a duplicate id is last write wins.
func indexByID[T any](items []T, idOf func(T) uuid.UUID) map[uuid.UUID]T {
	m := make(map[uuid.UUID]T, len(items))
	for _, item := range items {
		m[idOf(item)] = item
	}
	return m
}

// indexByIDPtr is indexByID's pointer variant: a missing key reads back nil,
// not zero-valued T - toShelfCard's absent-vs-zeroed nil check depends on it.
func indexByIDPtr[T any](items []T, idOf func(T) uuid.UUID) map[uuid.UUID]*T {
	m := make(map[uuid.UUID]*T, len(items))
	for i := range items {
		v := items[i]
		m[idOf(v)] = &v
	}
	return m
}

// dedupedCommentAuthorIDs collects each comment's author id once, first-seen
// order - the batch input for SharedCardsByIDs (dedupedOwnerIDs' sibling).
func dedupedCommentAuthorIDs(comments []rawComment) []uuid.UUID {
	return dedupedIDs(comments, func(c rawComment) uuid.UUID { return c.AuthorId }, nil)
}

// composeCommentsPage attaches an author card to every comment; a card-fetch
// failure fails open (identity is enhancement, not access-gated data). author_id
// is always present: live reads filter deleted_at IS NULL, so tombstones never serialize.
func (h *Handlers) composeCommentsPage(w http.ResponseWriter, r *http.Request, bearer string, body []byte) {
	var page rawCommentsPage
	if err := json.Unmarshal(body, &page); err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
		return
	}

	var cardByID map[uuid.UUID]userapi.ProfileCard
	if authorIDs := dedupedCommentAuthorIDs(page.Comments); len(authorIDs) > 0 {
		cards, err := h.users.SharedCardsByIDs(r.Context(), bearer, authorIDs)
		if err != nil {
			h.failOpenEvent(r.Context(), "comment_authors", err)
		} else {
			cardByID = cardsByID(cards)
		}
	}

	out := api.CommentList{Comments: make([]api.Comment, len(page.Comments)), NextCursor: page.NextCursor}
	for i, c := range page.Comments {
		item := api.Comment{Id: c.Id, ShelfId: c.ShelfId, Body: c.Body, CreatedAt: c.CreatedAt, AuthorId: c.AuthorId}
		if card, ok := cardByID[c.AuthorId]; ok {
			profileCard := toProfileCard(card)
			item.Author = &profileCard
		}
		out.Comments[i] = item
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateShelfComment relays a new comment after the effective-visibility
// check; body passes through untouched, social owns validation.
func (h *Handlers) CreateShelfComment(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if _, _, ok := h.effectiveShelf(w, r, sess.AccessToken, shelfId); !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.social.CreateComment(r.Context(), sess.AccessToken, shelfId, body)
	h.relaySocial(w, r, res, err)
}

// DeleteComment relays a tombstone without a shelf resolve: social knows
// the row's shelf/owner and authorizes by row (author or shelf owner).
func (h *Handlers) DeleteComment(w http.ResponseWriter, r *http.Request, commentId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.social.DeleteComment(r.Context(), sess.AccessToken, commentId)
	h.relaySocial(w, r, res, err)
}

// relaySocial funnels every social pass-through (session check
// happened at the caller); any client error answers 502 (relayCollection's twin).
func (h *Handlers) relaySocial(w http.ResponseWriter, r *http.Request, res socialclient.Result, err error) {
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// Follow and its siblings (Unfollow, LikeShelf, UnlikeShelf) are thin relays;
// social validates the target (self-follow, shelf visibility) itself, no effectiveShelf call here.
func (h *Handlers) Follow(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.social.Follow(r.Context(), sess.AccessToken, userId)
	h.relaySocial(w, r, res, err)
}

func (h *Handlers) Unfollow(w http.ResponseWriter, r *http.Request, userId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.social.Unfollow(r.Context(), sess.AccessToken, userId)
	h.relaySocial(w, r, res, err)
}

func (h *Handlers) LikeShelf(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.social.Like(r.Context(), sess.AccessToken, shelfId)
	h.relaySocial(w, r, res, err)
}

func (h *Handlers) UnlikeShelf(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.social.Unlike(r.Context(), sess.AccessToken, shelfId)
	h.relaySocial(w, r, res, err)
}

// SearchUsers relays a listed-handle substring search.
func (h *Handlers) SearchUsers(w http.ResponseWriter, r *http.Request, params api.SearchUsersParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.users.SearchProfiles(r.Context(), sess.AccessToken, params.Q)
	h.relayUser(w, r, res, err)
}

// cardsByID indexes profile cards by user id after one batched SharedCardsByIDs call.
func cardsByID(cards []userapi.ProfileCard) map[uuid.UUID]userapi.ProfileCard {
	return indexByID(cards, func(c userapi.ProfileCard) uuid.UUID { return c.UserId })
}

// shelfSummariesByID indexes shelf summaries by id after one batched SharedShelvesByIDs call.
func shelfSummariesByID(shelves []collectionapi.SharedShelfSummary) map[uuid.UUID]collectionapi.SharedShelfSummary {
	return indexByID(shelves, func(s collectionapi.SharedShelfSummary) uuid.UUID { return s.Id })
}

// shelfSocialByID indexes social summaries by shelf id after one batched
// ShelvesSummary call; a missing key yields nil, which toShelfCard treats as "no counts".
func shelfSocialByID(summaries []socialapi.ShelfSocialSummary) map[uuid.UUID]*socialapi.ShelfSocialSummary {
	return indexByIDPtr(summaries, func(s socialapi.ShelfSocialSummary) uuid.UUID { return s.ShelfId })
}

// dedupedOwnerIDs collects each shelf summary's owner id once, first-seen
// order, for a single SharedCardsByIDs call; feed/Explore repeat owners, the profile page never needs this.
func dedupedOwnerIDs(shelves []collectionapi.SharedShelfSummary) []uuid.UUID {
	return dedupedIDs(shelves, func(s collectionapi.SharedShelfSummary) uuid.UUID { return s.OwnerId }, nil)
}

// feedFillRounds bounds how many raw pages GetFeed fetches trying to fill one response.
const feedFillRounds = 3

// GetFeed fills the response by fetching raw pages, hydrating, and gating
// by tab, up to feedFillRounds rounds. next_cursor is the last INCLUDED
// event's cursor, so a boundary can re-scan a few dropped rows - correct,
// cost bounded by drop rate. tab/limit are specval's job (api/bff.yaml); a
// present cursor still needs ParseCursor here, since socialclient.Feed would
// otherwise turn an unvalidated bad cursor into a misleading 502.
func (h *Handlers) GetFeed(w http.ResponseWriter, r *http.Request, params api.GetFeedParams) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	limit := 20
	if params.Limit != nil {
		limit = *params.Limit
	}
	tab := string(params.Tab)
	var cursor *string
	if params.Cursor != nil && *params.Cursor != "" {
		if _, err := httpkit.ParseCursor(*params.Cursor); err != nil {
			writeProblem(w, r, http.StatusBadRequest, "invalid_param", "cursor must be <unixnano>.<uuid>")
			return
		}
		cursor = params.Cursor
	}
	items := []api.FeedItem{}
	var nextCursor *string
	for round := 0; round < feedFillRounds && len(items) < limit; round++ {
		events, rawNext, err := h.social.Feed(r.Context(), sess.AccessToken, tab, cursor, limit)
		if err != nil {
			writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
			return
		}
		hydrated, detail, err := h.hydrateFeed(r.Context(), sess.AccessToken, claims.Sub, tab, events)
		if err != nil {
			writeProblem(w, r, http.StatusBadGateway, "upstream_error", detail)
			return
		}
		for i, item := range hydrated {
			if item == nil {
				continue // gated out
			}
			items = append(items, *item)
			c := (httpkit.Cursor{CreatedAt: events[i].CreatedAt, ID: events[i].Id}).String()
			nextCursor = &c
			if len(items) == limit {
				break
			}
		}
		if rawNext == nil {
			if len(items) < limit {
				nextCursor = nil // exhausted
			}
			break
		}
		cursor = rawNext
		if len(items) < limit {
			c := *rawNext
			nextCursor = &c
		}
	}
	writeJSON(w, http.StatusOK, api.FeedPage{Items: items, NextCursor: nextCursor})
}

// hydrateFeed batches actor/followee/shelf-owner cards, shelf summaries,
// comment bodies, and social counts for one page, building a *api.FeedItem
// per event (nil where gating drops it) at the same index as events, so the
// caller recovers each survivor's cursor by index; on error the string names
// the failing dependency for the 502 detail. Every lookup here is a hard
// dependency, unlike decorative social counts elsewhere: failing open would risk naming a gated object.
func (h *Handlers) hydrateFeed(ctx context.Context, bearer, _, tab string, events []socialapi.ActivityEvent) ([]*api.FeedItem, string, error) {
	var shelfIDs []uuid.UUID
	seenShelf := map[uuid.UUID]bool{}
	var commentIDs []uuid.UUID
	for _, e := range events {
		if e.ObjectShelfId != nil && !seenShelf[*e.ObjectShelfId] {
			seenShelf[*e.ObjectShelfId] = true
			shelfIDs = append(shelfIDs, *e.ObjectShelfId)
		}
		if e.ObjectCommentId != nil {
			commentIDs = append(commentIDs, *e.ObjectCommentId)
		}
	}

	var shelves []collectionapi.SharedShelfSummary
	if len(shelfIDs) > 0 {
		var err error
		shelves, err = h.collection.SharedShelvesByIDs(ctx, bearer, shelfIDs)
		if err != nil {
			return nil, "collection service unavailable", fmt.Errorf("hydrate feed: shelves: %w", err)
		}
	}
	shelfByID := shelfSummariesByID(shelves)

	var personIDs []uuid.UUID
	seenPerson := map[uuid.UUID]bool{}
	addPerson := func(id uuid.UUID) {
		if !seenPerson[id] {
			seenPerson[id] = true
			personIDs = append(personIDs, id)
		}
	}
	for _, e := range events {
		addPerson(e.ActorId)
		if e.Verb == common.FollowedUser {
			addPerson(e.TargetUserId)
		}
	}
	for _, s := range shelves {
		addPerson(s.OwnerId)
	}
	var cards []userapi.ProfileCard
	if len(personIDs) > 0 {
		var err error
		cards, err = h.users.SharedCardsByIDs(ctx, bearer, personIDs)
		if err != nil {
			return nil, "user service unavailable", fmt.Errorf("hydrate feed: cards: %w", err)
		}
	}
	cardByID := cardsByID(cards)

	var comments []socialapi.Comment
	if len(commentIDs) > 0 {
		var err error
		comments, err = h.social.CommentsByIDs(ctx, bearer, commentIDs)
		if err != nil {
			return nil, "social service unavailable", fmt.Errorf("hydrate feed: comments: %w", err)
		}
	}
	// Same value-map shape as cardsByID/shelfSummariesByID; indexByID fits since
	// commentByID's one reader does an ok-check, so nil-vs-zero-value never mattered.
	commentByID := indexByID(comments, func(c socialapi.Comment) uuid.UUID { return c.Id })

	var summaries []socialapi.ShelfSocialSummary
	if len(shelfIDs) > 0 {
		var err error
		summaries, err = h.social.ShelvesSummary(ctx, bearer, shelfIDs)
		if err != nil {
			return nil, "social service unavailable", fmt.Errorf("hydrate feed: shelves summary: %w", err)
		}
	}
	summaryByID := shelfSocialByID(summaries)

	items := make([]*api.FeedItem, len(events))
	for i, e := range events {
		actor, ok := cardByID[e.ActorId]
		if !ok {
			continue // actor account gone; nothing left to attribute the action to
		}
		item := &api.FeedItem{
			Id: e.Id, Verb: e.Verb, CreatedAt: e.CreatedAt,
			Actor: toProfileCard(actor),
		}

		if e.Verb == common.FollowedUser {
			followee, ok := cardByID[e.TargetUserId]
			if tab == "following" && (!ok || followee.ProfileVisibility != common.Listed) {
				continue // followee not listed; gated out
			}
			if ok {
				card := toProfileCard(followee)
				item.FollowedUser = &card
			}
			items[i] = item
			continue
		}

		if e.ObjectShelfId == nil {
			continue // malformed event; nothing to attach
		}
		summary, ok := shelfByID[*e.ObjectShelfId]
		if !ok {
			continue // shelf gone or private
		}
		owner, ok := cardByID[summary.OwnerId]
		if !ok {
			continue
		}
		if tab == "following" && (summary.Visibility != collectionapi.Listed || owner.ProfileVisibility != common.Listed) {
			continue // shelf or owner not listed; gated out
		}
		card := toShelfCard(summary, owner, summaryByID[summary.Id])
		item.Shelf = &card
		if e.Verb == common.CommentedShelf && e.ObjectCommentId != nil {
			if c, ok := commentByID[*e.ObjectCommentId]; ok {
				item.CommentExcerpt = &c.Body
			}
		}
		items[i] = item
	}
	return items, "", nil
}

// topShelvesLimit is social's fixed leaderboard size (top ignores limit/
// offset); exploreFillRounds bounds exploreRecent's fill attempts, mirroring feedFillRounds.
const (
	topShelvesLimit   = 50
	exploreFillRounds = 3
)

// GetExplore browses shared shelves: recent (newest-published, paged) or
// top (fixed like-count leaderboard), both listed-only by construction. sort/
// limit(1-100)/offset(>=0) are specval's job (api/bff.yaml); only default-when-absent is handled here.
func (h *Handlers) GetExplore(w http.ResponseWriter, r *http.Request, params api.GetExploreParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	bearer := sess.AccessToken
	switch params.Sort {
	case api.Recent:
		limit := 20
		if params.Limit != nil {
			limit = *params.Limit
		}
		offset := 0
		if params.Offset != nil {
			offset = *params.Offset
		}
		h.exploreRecent(w, r, bearer, limit, offset)
	case api.Top:
		h.exploreTop(w, r, bearer)
	}
}

// exploreRecent pages collection's listed shelves directly (no owner_ids
// fan-in, so no ~5000-listed-profile ceiling), gating each page's owner for
// listed-ness at the bff. Short pages refill by re-paging at the advanced
// offset, up to exploreFillRounds rounds; social's like/comment counts fail open.
func (h *Handlers) exploreRecent(w http.ResponseWriter, r *http.Request, bearer string, limit, offset int) {
	cards := make([]api.ShelfCard, 0, limit)
	pos, total := offset, 0
	for round := 0; round < exploreFillRounds && len(cards) < limit; round++ {
		pageStart := pos
		shelves, t, err := h.collection.ListSharedShelves(r.Context(), bearer, nil, limit, pageStart)
		if err != nil {
			writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
			return
		}
		total = t
		consumed := len(shelves)
		pos = pageStart + consumed // whole-page advance; overridden below if the response fills before the page is fully examined

		if consumed > 0 {
			// dedupedOwnerIDs' input is at most limit shelves (capped at 100 by the
			// contract), within the user service's own by-ids batch cap (also 100) - no cross-call batching needed.
			owners, err := h.users.SharedCardsByIDs(r.Context(), bearer, dedupedOwnerIDs(shelves))
			if err != nil {
				writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
				return
			}
			ownerByID := cardsByID(owners)

			var summaryByID map[uuid.UUID]*socialapi.ShelfSocialSummary
			if summaries, err := h.social.ShelvesSummary(r.Context(), bearer, sharedShelfIDs(shelves)); err != nil {
				h.failOpenEvent(r.Context(), "social_summary", err)
			} else {
				summaryByID = shelfSocialByID(summaries)
			}

			for i, s := range shelves {
				owner, ok := ownerByID[s.OwnerId]
				if !ok || owner.ProfileVisibility != common.Listed {
					continue // owner unlisted or vanished between the listing and the card batch
				}
				cards = append(cards, toShelfCard(s, owner, summaryByID[s.Id]))
				if len(cards) == limit {
					// Resume just past the last INCLUDED row, not the whole page: any later
					// row (this page or beyond) is unexamined, and next_offset must not skip it -
					// an unexamined listed shelf would otherwise never be served.
					pos = pageStart + i + 1
					break
				}
			}
		}

		if consumed < limit || pos >= total {
			break // collection's listed-shelf stream is exhausted
		}
	}

	out := api.ExplorePage{Shelves: cards}
	if pos < total {
		next := pos
		out.NextOffset = &next
	}
	writeJSON(w, http.StatusOK, out)
}

// exploreTop is the fixed leaderboard: social orders it, collection supplies
// metadata, and shelf-then-owner listed checks drop delisted items, preserving
// order. Every lookup is a hard dependency: a social outage already 502'd at TopShelves.
func (h *Handlers) exploreTop(w http.ResponseWriter, r *http.Request, bearer string) {
	leaderboard, err := h.social.TopShelves(r.Context(), bearer, topShelvesLimit)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
		return
	}
	if len(leaderboard) == 0 {
		writeJSON(w, http.StatusOK, api.ExplorePage{Shelves: []api.ShelfCard{}})
		return
	}
	shelves, err := h.collection.SharedShelvesByIDs(r.Context(), bearer, leaderboard)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collection service unavailable")
		return
	}
	shelfByID := shelfSummariesByID(shelves)
	var ownerByID map[uuid.UUID]userapi.ProfileCard
	if ownerIDs := dedupedOwnerIDs(shelves); len(ownerIDs) > 0 {
		owners, err := h.users.SharedCardsByIDs(r.Context(), bearer, ownerIDs)
		if err != nil {
			writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
			return
		}
		ownerByID = cardsByID(owners)
	}

	type survivor struct {
		summary collectionapi.SharedShelfSummary
		owner   userapi.ProfileCard
	}
	survivors := make([]survivor, 0, len(leaderboard))
	survivorIDs := make([]uuid.UUID, 0, len(leaderboard))
	for _, id := range leaderboard {
		summary, ok := shelfByID[id]
		if !ok || summary.Visibility != collectionapi.Listed {
			continue
		}
		owner, ok := ownerByID[summary.OwnerId]
		if !ok || owner.ProfileVisibility != common.Listed {
			continue
		}
		survivors = append(survivors, survivor{summary: summary, owner: owner})
		survivorIDs = append(survivorIDs, id)
	}

	var summaryByID map[uuid.UUID]*socialapi.ShelfSocialSummary
	if len(survivorIDs) > 0 {
		summaries, err := h.social.ShelvesSummary(r.Context(), bearer, survivorIDs)
		if err != nil {
			writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
			return
		}
		summaryByID = shelfSocialByID(summaries)
	}

	cards := make([]api.ShelfCard, len(survivors))
	for i, sv := range survivors {
		cards[i] = toShelfCard(sv.summary, sv.owner, summaryByID[sv.summary.Id])
	}
	writeJSON(w, http.StatusOK, api.ExplorePage{Shelves: cards})
}

// GetSharedProfilesByIds relays batch profile-card hydration for the admin
// queue's submitter handles; typed (not raw relay), so it's re-marshaled into the frontend's envelope.
func (h *Handlers) GetSharedProfilesByIds(w http.ResponseWriter, r *http.Request, params api.GetSharedProfilesByIdsParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	cards, err := h.users.SharedCardsByIDs(r.Context(), sess.AccessToken, params.Ids)
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "user service unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": cards})
}
