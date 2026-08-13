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

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/socialapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/socialclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

// profilePageShelvesLimit caps the shelf list embedded in a profile
// page; the dedicated feed and Explore browse routes page further
// and are not bound by it.
const profilePageShelvesLimit = 50

// effectiveShelf resolves a shelf AND its owner card, applying the
// two-sided effective-visibility rule: the shelf must be non-private
// (collection already gates that) and the owner profile must be
// non-private. Unknown, private-shelf, and private-owner all converge
// on the same 404.
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

// toProfileCard maps the user service's cross-user projection onto
// the bff's own wire type (same shape; different generated package).
func toProfileCard(c userapi.ProfileCard) api.ProfileCard {
	return api.ProfileCard{
		UserId: c.UserId, Handle: c.Handle, AvatarUrl: c.AvatarUrl,
		ProfileVisibility: api.ProfileCardProfileVisibility(c.ProfileVisibility),
	}
}

// toShelfMeta maps a resolved shelf's own identity and stored view
// state (owner and social counts are ShelfPage's sibling fields).
func toShelfMeta(shelf collectionapi.SharedShelf) api.ShelfMeta {
	return api.ShelfMeta{
		Id: shelf.Id, Name: shelf.Name, Slug: shelf.Slug,
		Params: shelf.Params, PublishedAt: shelf.PublishedAt,
	}
}

func toShelfSocialSummary(s socialapi.ShelfSocialSummary) api.ShelfSocialSummary {
	return api.ShelfSocialSummary{ShelfId: s.ShelfId, LikeCount: s.LikeCount, CommentCount: s.CommentCount, ViewerLikes: s.ViewerLikes}
}

func toProfileSocialSummary(s socialapi.ProfileSocialSummary) api.ProfileSocialSummary {
	return api.ProfileSocialSummary{FollowerCount: s.FollowerCount, FollowingCount: s.FollowingCount, ViewerFollows: s.ViewerFollows}
}

// toShelfCard maps a shelf summary plus its resolved owner card and
// optional social counts onto the bff's shelf-card wire type; shared
// by the profile-page composition here and the feed and Explore
// hydration that reuse it. social nil means the page's social
// composition failed - the three social fields stay absent together,
// never a mix of some present and some not.
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

// composeShelfPage builds and writes a ShelfPage for an
// already-resolved, already-visibility-checked shelf and owner - the
// composition tail shared by GetShelfPage and GetProfileShelfPage.
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

// GetProfileShelfPage resolves a shelf by (owner handle, slug) and
// composes the same page GetShelfPage does. Every miss on this route
// - unknown handle, private owner, unknown slug, private shelf -
// answers the same 404 shelf_not_found: unlike GetProfilePage, a
// profile_not_found here would leak that the handle exists (or
// doesn't) independently of the shelf, which is exactly the oracle
// effective-visibility exists to deny.
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
	if owner.ProfileVisibility == userapi.ProfileCardProfileVisibilityPrivate {
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

// socialForProfile composes a profile page's social tail: the profile
// summary and the per-shelf summaries succeed or fail together, so a
// visitor never sees half a social block (follower counts with no
// like counts on the shelves listed below them). available false
// means the caller must leave Social unset and every ShelfCard's
// social fields absent.
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

// GetProfilePage composes a user's public profile: their card, up to
// profilePageShelvesLimit of their listed shelves, and social counts.
// Owners see exactly what visitors see (an honest preview).
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
	if owner.ProfileVisibility == userapi.ProfileCardProfileVisibilityPrivate {
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

// ListShelfEntries relays a shelf's whitelisted entry projection
// after the same effective-visibility check every shelf sub-route
// applies.
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

// ListShelfComments composes a shelf's live comments after the
// effective-visibility check: social's own page is decoded and
// hydrated with an author ProfileCard per comment, batched in ONE
// SharedCardsByIDs call over the distinct non-nil author ids (mirrors
// hydrateFeed's actor-card batching). Anything other than a 200 from
// social (its own 400 on a malformed cursor included) relays
// verbatim, unchanged from before this composition existed; cursor
// and limit still pass straight through to social.
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

// rawComment is a live comment row exactly as social's own page
// serializes it, decoded locally rather than through the typed
// socialapi client. AuthorId's zero value (uuid.Nil) is what a null
// author_id decodes to for a plain, non-pointer UUID field - the
// codebase's established "absent id" sentinel (compare
// publishIfListed's view.Id == uuid.Nil) - so a purge-anonymized row
// needs no separate pointer plumbing to detect.
type rawComment struct {
	Id        uuid.UUID `json:"id"`
	ShelfId   uuid.UUID `json:"shelf_id"`
	AuthorId  uuid.UUID `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// rawCommentsPage is social's ListShelfComments 200 body, decoded
// locally so composeCommentsPage can hydrate it before recomposing
// the bff's own CommentList.
type rawCommentsPage struct {
	Comments   []rawComment `json:"comments"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

// dedupedIDs collects each item's id once, in first-seen order; when
// keep is non-nil, an id failing it is skipped entirely rather than
// deduped-and-kept - dedupedCommentAuthorIDs' only use of this, to
// drop uuid.Nil (a purged/anonymized comment author) from the batch.
// Every other call site passes a nil keep and takes every id.
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

// indexByID maps each item under idOf's key; a duplicate id is last
// write wins (none of today's call sites produce one).
func indexByID[T any](items []T, idOf func(T) uuid.UUID) map[uuid.UUID]T {
	m := make(map[uuid.UUID]T, len(items))
	for _, item := range items {
		m[idOf(item)] = item
	}
	return m
}

// indexByIDPtr is indexByID's pointer-map variant: a missing key
// reads back nil rather than a zero-valued T. shelfSocialByID is the
// one caller that needs this - toShelfCard's nil check (which leaves
// a card's social fields absent rather than zeroed) depends on it.
func indexByIDPtr[T any](items []T, idOf func(T) uuid.UUID) map[uuid.UUID]*T {
	m := make(map[uuid.UUID]*T, len(items))
	for i := range items {
		v := items[i]
		m[idOf(v)] = &v
	}
	return m
}

// dedupedCommentAuthorIDs collects each live comment's non-nil author
// id once, in first-seen order - the batch input for the single
// follow-up SharedCardsByIDs call (dedupedOwnerIDs' sibling for
// comments). A purge-anonymized comment (author_id uuid.Nil)
// contributes nothing.
func dedupedCommentAuthorIDs(comments []rawComment) []uuid.UUID {
	return dedupedIDs(comments,
		func(c rawComment) uuid.UUID { return c.AuthorId },
		func(id uuid.UUID) bool { return id != uuid.Nil })
}

// composeCommentsPage attaches an author ProfileCard to every comment
// whose author_id survived, then serializes the composed page. A
// card-fetch failure fails open: the page still serves without any
// author, since comments already carry their own body text and
// identity here is enhancement, not access-gated data - the same
// posture as a shelf or profile page's decorative social counts.
// api.Comment.AuthorId is *openapi_types.UUID (required+nullable, per
// api/bff.yaml): a purge-anonymized row's rawComment.AuthorId ==
// uuid.Nil leaves it at its zero value (nil), which serializes as the
// JSON literal null - the wire shape a null author_id needs to be
// reachable at all, not just absent-with-omitempty.
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
		item := api.Comment{Id: c.Id, ShelfId: c.ShelfId, Body: c.Body, CreatedAt: c.CreatedAt}
		if c.AuthorId != uuid.Nil {
			authorID := c.AuthorId
			item.AuthorId = &authorID
			if card, ok := cardByID[c.AuthorId]; ok {
				profileCard := toProfileCard(card)
				item.Author = &profileCard
			}
		}
		out.Comments[i] = item
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateShelfComment relays a new comment after the
// effective-visibility check; the body passes through untouched (the
// social service owns its validation).
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

// DeleteComment relays a comment tombstone without a shelf resolve:
// social already knows the row's shelf and owner and authorizes by
// row (author or shelf owner).
func (h *Handlers) DeleteComment(w http.ResponseWriter, r *http.Request, commentId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.social.DeleteComment(r.Context(), sess.AccessToken, commentId)
	h.relaySocial(w, r, res, err)
}

// relaySocial funnels every social pass-through: session check
// happened at the caller; any client error is an infrastructure fault
// answered 502 (relayCollection's twin for the social service).
func (h *Handlers) relaySocial(w http.ResponseWriter, r *http.Request, res socialclient.Result, err error) {
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
		return
	}
	writeRelay(w, res.Status, res.ContentType, res.Body)
}

// Follow and its siblings (Unfollow, Like, Unlike) are thin relays;
// the social service validates the target (self-follow, shelf
// visibility) itself, so there is no effectiveShelf call here.
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

func (h *Handlers) Like(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.social.Like(r.Context(), sess.AccessToken, shelfId)
	h.relaySocial(w, r, res, err)
}

func (h *Handlers) Unlike(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
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

// cardsByID indexes profile cards by user id for repeated per-event
// lookups after one batched SharedCardsByIDs call.
func cardsByID(cards []userapi.ProfileCard) map[uuid.UUID]userapi.ProfileCard {
	return indexByID(cards, func(c userapi.ProfileCard) uuid.UUID { return c.UserId })
}

// shelfSummariesByID indexes shelf summaries by id after one batched
// SharedShelvesByIDs call.
func shelfSummariesByID(shelves []collectionapi.SharedShelfSummary) map[uuid.UUID]collectionapi.SharedShelfSummary {
	return indexByID(shelves, func(s collectionapi.SharedShelfSummary) uuid.UUID { return s.Id })
}

// shelfSocialByID indexes social summaries by shelf id after one
// batched ShelvesSummary call; a missing key (map lookup on an id
// that was never requested, or a request that failed open) yields a
// nil *ShelfSocialSummary, which toShelfCard treats as "no counts".
func shelfSocialByID(summaries []socialapi.ShelfSocialSummary) map[uuid.UUID]*socialapi.ShelfSocialSummary {
	return indexByIDPtr(summaries, func(s socialapi.ShelfSocialSummary) uuid.UUID { return s.ShelfId })
}

// dedupedOwnerIDs collects each shelf summary's owner id once, in
// first-seen order - the batch input for a single follow-up
// SharedCardsByIDs call. A feed page or an Explore grid spans many
// owners and repeats them (one prolific lister's several shelves);
// the profile page's single owner never needs this.
func dedupedOwnerIDs(shelves []collectionapi.SharedShelfSummary) []uuid.UUID {
	return dedupedIDs(shelves, func(s collectionapi.SharedShelfSummary) uuid.UUID { return s.OwnerId }, nil)
}

// feedFillRounds bounds how many raw pages GetFeed will fetch trying
// to fill one response; feedPageMax bounds the caller's requested
// page size (the same cap social itself enforces on its own limit
// parameter).
const (
	feedFillRounds = 3
	feedPageMax    = 50
)

// GetFeed runs the fill loop: fetch a raw page from social, hydrate
// (shelf summaries, actor cards, comment excerpts), gate by tab rule,
// repeat until limit survivors or the stream is exhausted, at most
// feedFillRounds rounds. next_cursor is the raw cursor of the last
// INCLUDED event, so a page boundary can re-scan a few dropped rows -
// correct, and the cost is bounded by the drop rate. tab and a
// present cursor are validated here, before social is ever called:
// socialclient.Feed collapses every non-200 (including social's own
// 400 on either input) into one generic error, so an unvalidated bad
// tab or cursor would otherwise surface as a misleading 502.
func (h *Handlers) GetFeed(w http.ResponseWriter, r *http.Request, params api.GetFeedParams) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	switch params.Tab {
	case api.Following, api.You:
	default:
		writeProblem(w, r, http.StatusBadRequest, "invalid_param", "tab must be following or you")
		return
	}
	limit, ok := httpkit.ClampOrReject(params.Limit, 20, 1)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, "invalid_param", "limit must be at least 1")
		return
	}
	limit = min(limit, feedPageMax)
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
		hydrated, err := h.hydrateFeed(r.Context(), sess.AccessToken, claims.Sub, tab, events)
		if err != nil {
			writeProblem(w, r, http.StatusBadGateway, "upstream_error", "social service unavailable")
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

// hydrateFeed batches actor, followee, and shelf-owner cards; shelf
// summaries; comment bodies; and social counts for one raw feed page,
// then builds a *api.FeedItem per event, or nil where the tab's
// gating rule drops it. The returned slice has the same length and
// order as events, so the caller can recover each survivor's raw
// cursor by index. Every lookup here is a hard dependency (an error
// aborts the whole page, no fail-open): unlike a page's decorative
// social counts elsewhere, the gate itself depends on this data, and
// failing open would risk naming a gated object. The unused identity
// argument keeps call-site parity with the rest of the composition
// helpers in this file; the gating rule itself needs only the tab and
// the batched cards/summaries, because social already scopes tab=you's
// events to ones targeting the caller.
func (h *Handlers) hydrateFeed(ctx context.Context, bearer, _, tab string, events []socialapi.ActivityEvent) ([]*api.FeedItem, error) {
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
			return nil, fmt.Errorf("hydrate feed: shelves: %w", err)
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
		if e.Verb == socialapi.FollowedUser {
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
			return nil, fmt.Errorf("hydrate feed: cards: %w", err)
		}
	}
	cardByID := cardsByID(cards)

	var comments []socialapi.Comment
	if len(commentIDs) > 0 {
		var err error
		comments, err = h.social.CommentsByIDs(ctx, bearer, commentIDs)
		if err != nil {
			return nil, fmt.Errorf("hydrate feed: comments: %w", err)
		}
	}
	// Same value-map shape as cardsByID/shelfSummariesByID above -
	// indexByID fits with no filter and no pointer-map nuance
	// (commentByID's one reader is an `if c, ok := ...; ok` lookup,
	// so a nil-vs-zero-value miss distinction never mattered here).
	commentByID := indexByID(comments, func(c socialapi.Comment) uuid.UUID { return c.Id })

	var summaries []socialapi.ShelfSocialSummary
	if len(shelfIDs) > 0 {
		var err error
		summaries, err = h.social.ShelvesSummary(ctx, bearer, shelfIDs)
		if err != nil {
			return nil, fmt.Errorf("hydrate feed: shelves summary: %w", err)
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
			Id: e.Id, Verb: api.FeedItemVerb(e.Verb), CreatedAt: e.CreatedAt,
			Actor: toProfileCard(actor),
		}

		if e.Verb == socialapi.FollowedUser {
			followee, ok := cardByID[e.TargetUserId]
			if tab == "following" && (!ok || followee.ProfileVisibility != userapi.ProfileCardProfileVisibilityListed) {
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
		if tab == "following" && (summary.Visibility != collectionapi.SharedShelfSummaryVisibilityListed || owner.ProfileVisibility != userapi.ProfileCardProfileVisibilityListed) {
			continue // shelf or owner not listed; gated out
		}
		card := toShelfCard(summary, owner, summaryByID[summary.Id])
		item.Shelf = &card
		if e.Verb == socialapi.CommentedShelf && e.ObjectCommentId != nil {
			if c, ok := commentByID[*e.ObjectCommentId]; ok {
				item.CommentExcerpt = &c.Body
			}
		}
		items[i] = item
	}
	return items, nil
}

// explorePageMax caps a caller-requested recent page; topShelvesLimit
// is social's own fixed leaderboard size (top ignores limit/offset -
// it is not a deep-paging surface); exploreFillRounds bounds how many
// raw collection pages exploreRecent will fetch trying to fill one
// response, mirroring feedFillRounds.
const (
	explorePageMax    = 100
	topShelvesLimit   = 50
	exploreFillRounds = 3
)

// GetExplore browses shared shelves: recent (newest-published,
// paged) or top (the fixed like-count leaderboard). Both surfaces are
// listed-only by construction.
func (h *Handlers) GetExplore(w http.ResponseWriter, r *http.Request, params api.GetExploreParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	bearer := sess.AccessToken
	switch params.Sort {
	case api.Recent:
		limit, ok := httpkit.ClampOrReject(params.Limit, 20, 1)
		if !ok {
			writeProblem(w, r, http.StatusBadRequest, "invalid_param", "limit must be at least 1")
			return
		}
		offset, ok := httpkit.ClampOrReject(params.Offset, 0, 0)
		if !ok {
			writeProblem(w, r, http.StatusBadRequest, "invalid_param", "offset must be at least 0")
			return
		}
		limit = min(limit, explorePageMax)
		h.exploreRecent(w, r, bearer, limit, offset)
	case api.Top:
		h.exploreTop(w, r, bearer)
	default:
		writeProblem(w, r, http.StatusBadRequest, "invalid_param", "sort must be recent or top")
	}
}

// exploreRecent pages collection's listed shelves directly - no
// owner_ids fan-in, so there is no ~5000-listed-profile ceiling - and
// gates each page's owner for listed-ness at the bff: the shelf side
// is already listed by construction (collection's unfiltered
// /shared/shelves query only ever returns visibility=listed rows), so
// the owner card is the one check left, the same shelf-then-owner
// order effectiveShelf and hydrateFeed use. Short pages are filled by
// re-paging collection at the advanced offset - the feed fill loop's
// own pattern - up to exploreFillRounds rounds. next_offset is the
// raw collection-space offset to resume from: when a page is fully
// examined without filling the response (an all-dropped page
// included), it counts every row collection returned, so the loop
// still advances instead of stalling; when the response instead fills
// mid-page, it resumes just past the last INCLUDED row - the same
// resume-from-last-included discipline the feed fill loop uses for
// next_cursor - so no unexamined row, in that page's tail or a later
// page, is ever skipped. It is present whenever collection has more
// rows beyond wherever the loop resumes. Social's like/comment counts
// fail open (the grid still renders, just without counts) exactly
// like a shelf/profile page's social block.
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
			// dedupedOwnerIDs' input is one collection page, at most
			// limit shelves (limit is already capped at explorePageMax
			// = 100 by GetExplore) - within the user service's own
			// by-ids batch cap (also 100), so this never needs batching
			// across multiple calls.
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
				if !ok || owner.ProfileVisibility != userapi.ProfileCardProfileVisibilityListed {
					continue // owner unlisted or vanished between the listing and the card batch
				}
				cards = append(cards, toShelfCard(s, owner, summaryByID[s.Id]))
				if len(cards) == limit {
					// Resume just past the row actually
					// examined-and-included, not past the whole
					// page: any row after it - later in this same
					// page, or in a page beyond it - has not been
					// examined yet, and next_offset must not skip
					// over it (a listed shelf sitting in that
					// unexamined tail would otherwise never be
					// served).
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

// exploreTop is the fixed all-time like-count leaderboard: social
// orders it, collection supplies the shelf metadata, and the two
// listed checks (shelf, then owner) drop anything gone non-listed
// since it was liked - leaderboard order is preserved throughout.
// Every lookup here is a hard dependency, ShelvesSummary included:
// unlike Explore-recent, a social outage already 502'd at TopShelves,
// so there is no "degrade quietly" posture left to apply.
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
		if !ok || summary.Visibility != collectionapi.SharedShelfSummaryVisibilityListed {
			continue
		}
		owner, ok := ownerByID[summary.OwnerId]
		if !ok || owner.ProfileVisibility != userapi.ProfileCardProfileVisibilityListed {
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

// GetSharedProfilesByIds relays the batch profile-card hydration the
// admin queue uses for submitter handles. Unlike the pass-through
// admin relays above, the user service's answer is typed (not a raw
// body relay), so it is re-marshaled into the envelope the frontend
// expects rather than passed through verbatim.
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
