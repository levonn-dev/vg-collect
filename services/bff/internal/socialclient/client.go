// Package socialclient calls the social service with the user's own
// Bearer token, via the generated typed client. Most methods are
// verbatim relays (upstream problem bodies flow to the browser
// unchanged); the typed reads are composition inputs the bff itself
// consumes to build the shared shelf/profile pages, the activity feed
// and Explore browsing, and the publish/purge orchestration legs.
package socialclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/socialapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

// ErrUpstream means the social service answered outside its relayed
// contract (or an infrastructure layer answered for it).
var ErrUpstream = errors.New("socialclient: upstream failure")

// Result is one relayable upstream answer.
type Result = httpkit.RelayResult

// Client wraps the generated socialapi typed client.
type Client struct {
	api *socialapi.ClientWithResponses
}

// New builds a Client against baseURL using an otelhttp transport and
// a 10-second timeout.
func New(baseURL string) (*Client, error) {
	api, err := socialapi.NewClientWithResponses(baseURL, socialapi.WithHTTPClient(httpkit.NewHTTPClient()))
	if err != nil {
		return nil, fmt.Errorf("socialclient: %w", err)
	}
	return &Client{api: api}, nil
}

// Follow relays PUT /follows/{userId}.
func (c *Client) Follow(ctx context.Context, bearer string, userID uuid.UUID) (Result, error) {
	resp, err := c.api.FollowWithResponse(ctx, userID, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: follow: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests)
}

// Unfollow relays DELETE /follows/{userId}.
func (c *Client) Unfollow(ctx context.Context, bearer string, userID uuid.UUID) (Result, error) {
	resp, err := c.api.UnfollowWithResponse(ctx, userID, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: unfollow: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusNoContent)
}

// Like relays PUT /likes/{shelfId}.
func (c *Client) Like(ctx context.Context, bearer string, shelfID uuid.UUID) (Result, error) {
	resp, err := c.api.LikeShelfWithResponse(ctx, shelfID, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: like: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusNotFound, http.StatusTooManyRequests)
}

// Unlike relays DELETE /likes/{shelfId}.
func (c *Client) Unlike(ctx context.Context, bearer string, shelfID uuid.UUID) (Result, error) {
	resp, err := c.api.UnlikeShelfWithResponse(ctx, shelfID, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: unlike: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusNoContent)
}

// ListComments relays GET /shelves/{shelfId}/comments.
func (c *Client) ListComments(ctx context.Context, bearer string, shelfID uuid.UUID, cursor *string, limit *int) (Result, error) {
	params := &socialapi.ListShelfCommentsParams{Cursor: cursor, Limit: limit}
	resp, err := c.api.ListShelfCommentsWithResponse(ctx, shelfID, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: list comments: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK, http.StatusBadRequest)
}

// CreateComment relays POST /shelves/{shelfId}/comments with the
// browser body untouched (the social service owns its validation).
func (c *Client) CreateComment(ctx context.Context, bearer string, shelfID uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.CreateShelfCommentWithBodyWithResponse(ctx, shelfID, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: create comment: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusCreated, http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests)
}

// DeleteComment relays DELETE /comments/{commentId}.
func (c *Client) DeleteComment(ctx context.Context, bearer string, commentID uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteCommentWithResponse(ctx, commentID, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: delete comment: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusForbidden, http.StatusNotFound)
}

// ProfileSummary is the typed read backing profile-page composition:
// follower/following counts plus whether the caller follows them; not
// relayed to browsers verbatim (the bff composes its own page schema
// around it).
func (c *Client) ProfileSummary(ctx context.Context, bearer string, userID uuid.UUID) (socialapi.ProfileSocialSummary, error) {
	resp, err := c.api.GetProfileSocialSummaryWithResponse(ctx, userID, httpkit.BearerEditor(bearer))
	if err != nil {
		return socialapi.ProfileSocialSummary{}, fmt.Errorf("socialclient: profile summary: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return socialapi.ProfileSocialSummary{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// ShelvesSummary is the typed read backing shelf-card composition:
// batch like/comment counts plus the caller's like state (a zeroed
// row for every requested id, even ones with no social activity).
func (c *Client) ShelvesSummary(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
	resp, err := c.api.GetShelvesSocialSummaryWithResponse(ctx, &socialapi.GetShelvesSocialSummaryParams{Ids: ids}, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("socialclient: shelves summary: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.Summaries, nil
}

// CommentsByIDs is the typed read behind feed-excerpt hydration: live
// comments among the given ids.
func (c *Client) CommentsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.Comment, error) {
	resp, err := c.api.GetCommentsByIdsWithResponse(ctx, &socialapi.GetCommentsByIdsParams{Ids: ids}, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("socialclient: comments by ids: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.Comments, nil
}

// Feed is the typed read behind the activity feed: raw events for the
// caller, keyset-paged.
func (c *Client) Feed(ctx context.Context, bearer, tab string, cursor *string, limit int) (events []socialapi.ActivityEvent, nextCursor *string, err error) {
	params := &socialapi.GetFeedParams{Tab: socialapi.GetFeedParamsTab(tab), Cursor: cursor, Limit: &limit}
	resp, ferr := c.api.GetFeedWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if ferr != nil {
		return nil, nil, fmt.Errorf("socialclient: feed: %w", ferr)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.Events, resp.JSON200.NextCursor, nil
}

// TopShelves is the typed read behind the Explore leaderboard: shelf
// ids by live like count, most-liked first.
func (c *Client) TopShelves(ctx context.Context, bearer string, limit int) ([]uuid.UUID, error) {
	resp, err := c.api.GetTopShelvesWithResponse(ctx, &socialapi.GetTopShelvesParams{Limit: &limit}, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("socialclient: top shelves: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.ShelfIds, nil
}

// RecordPublish tells social a shelf just (re)published - the bff's
// own orchestration off a successful visibility-to-listed transition,
// never called directly by a browser.
func (c *Client) RecordPublish(ctx context.Context, bearer string, shelfID uuid.UUID) error {
	resp, err := c.api.RecordShelfPublishedWithResponse(ctx, socialapi.RecordShelfPublishedJSONRequestBody{ShelfId: shelfID}, httpkit.BearerEditor(bearer))
	if err != nil {
		return fmt.Errorf("socialclient: record publish: %w", err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return nil
}

// PurgeUserData relays the account-deletion leg: wipes the caller's
// social graph (follows, likes, comments, activity).
func (c *Client) PurgeUserData(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.PurgeUserDataWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("socialclient: purge user data: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusNoContent)
}
