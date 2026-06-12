// Package userclient calls the user service with self-minted
// service-role Bearer tokens, via the generated typed client.
package userclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/services/auth/internal/gen/userapi"
	"github.com/levonn-dev/vg-collect/services/auth/internal/token"
)

var ErrUserNotFound = errors.New("userclient: user not found")

// User is the subset of the user-service response this service needs:
// the subject to mint for and the roles to put in the claims.
type User struct {
	ID    uuid.UUID
	Roles []string
}

type Client struct {
	api *userapi.ClientWithResponses
}

// New builds a client against baseURL. A fresh service token is minted
// per request: signing is cheap and this avoids expiry bookkeeping.
func New(baseURL string, minter *token.Minter) (*Client, error) {
	hc := &http.Client{
		Timeout:   10 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	api, err := userapi.NewClientWithResponses(baseURL,
		userapi.WithHTTPClient(hc),
		userapi.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			tok, err := minter.ServiceToken()
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+tok)
			return nil
		}))
	if err != nil {
		return nil, fmt.Errorf("userclient: %w", err)
	}
	return &Client{api: api}, nil
}

// Upsert creates or refreshes the user's profile at login and returns
// the canonical id + roles to mint claims from.
func (c *Client) Upsert(ctx context.Context, email, displayName string, avatarURL *string) (User, error) {
	resp, err := c.api.UpsertUserWithResponse(ctx, userapi.UpsertUserJSONRequestBody{
		Email: email, DisplayName: displayName, AvatarUrl: avatarURL,
	})
	if err != nil {
		return User{}, fmt.Errorf("userclient: upsert: %w", err)
	}
	if resp.JSON200 == nil {
		return User{}, fmt.Errorf("userclient: upsert: status %d", resp.StatusCode())
	}
	return toUser(*resp.JSON200), nil
}

// Get fetches a user's current roles (refresh re-reads them so role
// changes propagate at the next rotation, not at session end).
func (c *Client) Get(ctx context.Context, id uuid.UUID) (User, error) {
	resp, err := c.api.GetUserWithResponse(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("userclient: get: %w", err)
	}
	// Gate on the parsed problem body, not the raw status: only a real
	// user-service 404 (problem+json) means the user is gone. Anything
	// else shaped like a 404 (a misrouted URL, a proxy error page) must
	// surface as a plain error so the caller treats it as transient
	// instead of revoking the session.
	if resp.ApplicationproblemJSON404 != nil {
		return User{}, ErrUserNotFound
	}
	if resp.JSON200 == nil {
		return User{}, fmt.Errorf("userclient: get: status %d", resp.StatusCode())
	}
	return toUser(*resp.JSON200), nil
}

func toUser(u userapi.User) User {
	roles := make([]string, len(u.Roles))
	for i, r := range u.Roles {
		roles[i] = string(r)
	}
	return User{ID: u.Id, Roles: roles}
}
