// Package userclient fetches user profiles on the end user's own bearer
// token (the user service allows self-access); no service credential is
// minted, unlike auth's service-role client - the bff already holds the user's token.
package userclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

// ErrUserNotFound is a parsed problem+json 404: the account is gone, not a proxy error.
var ErrUserNotFound = errors.New("userclient: user not found")

// ErrProfileNotFound is a parsed problem+json 404 on a shared-profile
// resolve; unknown and private handles are deliberately indistinguishable.
var ErrProfileNotFound = errors.New("userclient: profile not found")

// ErrUpstream means the answer was outside the relayed contract (user or infrastructure).
var ErrUpstream = errors.New("userclient: upstream failure")

// Result relays a raw upstream answer so user-service validation problems reach the browser verbatim.
type Result = httpkit.RelayResult

// Client wraps the generated userapi typed client.
type Client struct {
	api *userapi.ClientWithResponses
}

// New builds a Client against baseURL with an otelhttp transport and a 10s timeout.
func New(baseURL string) (*Client, error) {
	api, err := userapi.NewClientWithResponses(baseURL, userapi.WithHTTPClient(httpkit.NewHTTPClient()))
	if err != nil {
		return nil, fmt.Errorf("userclient: %w", err)
	}
	return &Client{api: api}, nil
}

// Get fetches the user as themselves (sub == id). Gated on the parsed
// problem body, not raw status, so a misrouted 404 page is transient, not a vanished account.
func (c *Client) Get(ctx context.Context, id, bearer string) (userapi.User, error) {
	uid, err := parseUserID(id)
	if err != nil {
		return userapi.User{}, err
	}
	resp, err := c.api.GetUserWithResponse(ctx, uid, httpkit.BearerEditor(bearer))
	if err != nil {
		return userapi.User{}, fmt.Errorf("userclient: get: %w", err)
	}
	if resp.ApplicationproblemJSON404 != nil {
		return userapi.User{}, ErrUserNotFound
	}
	if resp.JSON200 == nil {
		return userapi.User{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// Update forwards a profile PATCH as the user; body relays untouched both ways, user service owns validation.
func (c *Client) Update(ctx context.Context, id, bearer string, body []byte) (Result, error) {
	uid, err := parseUserID(id)
	if err != nil {
		return Result{}, err
	}
	resp, err := c.api.UpdateUserWithBodyWithResponse(ctx, uid, "application/json",
		bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("userclient: update: %w", err)
	}
	return Result{
		Status:      resp.StatusCode(),
		ContentType: httpkit.ContentType(resp.HTTPResponse),
		Body:        resp.Body,
	}, nil
}

// Delete removes the account row as the user (self-authorized).
func (c *Client) Delete(ctx context.Context, id, bearer string) error {
	uid, err := parseUserID(id)
	if err != nil {
		return err
	}
	resp, err := c.api.DeleteUserWithResponse(ctx, uid, httpkit.BearerEditor(bearer))
	if err != nil {
		return fmt.Errorf("userclient: delete: %w", err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return nil
}

// parseUserID converts a path-supplied id to a uuid in this package's error
// taxonomy. Duplicated in authclient; not worth a shared package for it.
func parseUserID(id string) (uuid.UUID, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("userclient: bad user id: %w", err)
	}
	return uid, nil
}

// SharedProfile resolves a handle to its cross-user profile card; gated on
// the parsed problem body, unknown and private handles both ErrProfileNotFound.
func (c *Client) SharedProfile(ctx context.Context, bearer, handle string) (userapi.ProfileCard, error) {
	resp, err := c.api.GetSharedProfileWithResponse(ctx, handle, httpkit.BearerEditor(bearer))
	if err != nil {
		return userapi.ProfileCard{}, fmt.Errorf("userclient: shared profile: %w", err)
	}
	if resp.ApplicationproblemJSON404 != nil {
		return userapi.ProfileCard{}, ErrProfileNotFound
	}
	if resp.JSON200 == nil {
		return userapi.ProfileCard{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// SharedCardsByIDs batch-resolves profile cards for hydration, returned
// regardless of visibility (actions are signed; page access is gated separately).
func (c *Client) SharedCardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
	resp, err := c.api.GetSharedProfilesByIdsWithResponse(ctx, &userapi.GetSharedProfilesByIdsParams{Ids: ids}, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("userclient: shared cards by ids: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.Profiles, nil
}

// SearchProfiles relays a listed-handle substring search verbatim (the SPA's people-search box).
func (c *Client) SearchProfiles(ctx context.Context, bearer, q string) (Result, error) {
	resp, err := c.api.SearchSharedProfilesWithResponse(ctx, &userapi.SearchSharedProfilesParams{Q: q}, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("userclient: search profiles: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return Result{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return Result{
		Status:      resp.StatusCode(),
		ContentType: httpkit.ContentType(resp.HTTPResponse),
		Body:        resp.Body,
	}, nil
}
