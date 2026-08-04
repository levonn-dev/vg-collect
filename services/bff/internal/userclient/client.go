// Package userclient fetches user profiles on behalf of the end user,
// forwarding the user's own bearer token (the user service allows
// self-access). No service credential is minted, unlike the auth
// service's service-role client; the bff already holds the user's
// token from the session cookie.
package userclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
)

// ErrUserNotFound is returned when the user service issues a parsed
// problem+json 404, meaning the account is gone (not a proxy error).
var ErrUserNotFound = errors.New("userclient: user not found")

// ErrProfileNotFound is returned when the user service issues a
// parsed problem+json 404 for a shared-profile resolve: unknown and
// private handles are deliberately indistinguishable.
var ErrProfileNotFound = errors.New("userclient: profile not found")

// ErrUpstream means the user service answered outside its relayed
// contract (or an infrastructure layer answered for it).
var ErrUpstream = errors.New("userclient: upstream failure")

// Result relays a raw upstream answer so validation problems from the
// user service reach the browser verbatim.
type Result struct {
	Status      int
	ContentType string
	Body        []byte
}

// Client wraps the generated userapi typed client.
type Client struct {
	api *userapi.ClientWithResponses
}

// New builds a Client against baseURL using an otelhttp transport and a
// 10-second timeout.
func New(baseURL string) (*Client, error) {
	hc := &http.Client{
		Timeout:   10 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	api, err := userapi.NewClientWithResponses(baseURL, userapi.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("userclient: %w", err)
	}
	return &Client{api: api}, nil
}

// Get fetches the user as themselves (the user service allows
// sub == id). Gate not-found on the parsed problem body, not the raw
// status, so a misrouted 404 page is a transient error rather than a
// vanished account.
func (c *Client) Get(ctx context.Context, id, bearer string) (userapi.User, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return userapi.User{}, fmt.Errorf("userclient: bad user id: %w", err)
	}
	resp, err := c.api.GetUserWithResponse(ctx, uid, func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+bearer)
		return nil
	})
	if err != nil {
		return userapi.User{}, fmt.Errorf("userclient: get: %w", err)
	}
	if resp.ApplicationproblemJSON404 != nil {
		return userapi.User{}, ErrUserNotFound
	}
	if resp.JSON200 == nil {
		return userapi.User{}, fmt.Errorf("userclient: get: status %d", resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// Update forwards a profile PATCH as the user; the body is relayed
// untouched in both directions (the user service owns validation).
func (c *Client) Update(ctx context.Context, id, bearer string, body []byte) (Result, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return Result{}, fmt.Errorf("userclient: bad user id: %w", err)
	}
	resp, err := c.api.UpdateUserWithBodyWithResponse(ctx, uid, "application/json",
		bytes.NewReader(body), func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+bearer)
			return nil
		})
	if err != nil {
		return Result{}, fmt.Errorf("userclient: update: %w", err)
	}
	return Result{
		Status:      resp.StatusCode(),
		ContentType: resp.HTTPResponse.Header.Get("Content-Type"),
		Body:        resp.Body,
	}, nil
}

// Delete removes the account row as the user (self-authorized).
func (c *Client) Delete(ctx context.Context, id, bearer string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("userclient: bad user id: %w", err)
	}
	resp, err := c.api.DeleteUserWithResponse(ctx, uid, func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+bearer)
		return nil
	})
	if err != nil {
		return fmt.Errorf("userclient: delete: %w", err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("userclient: delete: status %d", resp.StatusCode())
	}
	return nil
}

func bearerEditor(bearer string) userapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+bearer)
		return nil
	}
}

// SharedProfile resolves a handle to its cross-user profile card, the
// composition input behind the shared shelf/profile pages. Gated on
// the parsed problem body like Get: unknown and private
// handles are deliberately indistinguishable, both ErrProfileNotFound.
func (c *Client) SharedProfile(ctx context.Context, bearer, handle string) (userapi.ProfileCard, error) {
	resp, err := c.api.GetSharedProfileWithResponse(ctx, handle, bearerEditor(bearer))
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

// SharedCardsByIDs batch-resolves profile cards for hydration -
// returned regardless of visibility (actions are signed; page access
// is gated separately, by effectiveShelf and its siblings).
func (c *Client) SharedCardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
	resp, err := c.api.GetSharedProfilesByIdsWithResponse(ctx, &userapi.GetSharedProfilesByIdsParams{Ids: ids}, bearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("userclient: shared cards by ids: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.Profiles, nil
}

// SearchProfiles relays a listed-handle substring search verbatim
// (the SPA's people-search box).
func (c *Client) SearchProfiles(ctx context.Context, bearer, q string) (Result, error) {
	resp, err := c.api.SearchSharedProfilesWithResponse(ctx, &userapi.SearchSharedProfilesParams{Q: q}, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("userclient: search profiles: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return Result{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return Result{
		Status:      resp.StatusCode(),
		ContentType: resp.HTTPResponse.Header.Get("Content-Type"),
		Body:        resp.Body,
	}, nil
}
