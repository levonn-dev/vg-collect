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

	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/userapi"
)

// ErrUserNotFound is returned when the user service issues a parsed
// problem+json 404, meaning the account is gone (not a proxy error).
var ErrUserNotFound = errors.New("userclient: user not found")

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
