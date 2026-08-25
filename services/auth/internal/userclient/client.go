// Package userclient calls the user service with self-minted
// service-role Bearer tokens, via the generated typed client.
package userclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/auth/internal/token"
)

var ErrUserNotFound = errors.New("userclient: user not found")

// User is the subset of the user-service response this service needs: subject and roles.
type User struct {
	ID    uuid.UUID
	Roles []string
}

type Client struct {
	api *userapi.ClientWithResponses
}

// New builds a client against baseURL; a fresh service token is minted per request (cheap, avoids expiry bookkeeping).
func New(baseURL string, minter *token.Minter) (*Client, error) {
	hc := httpkit.NewHTTPClient()
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

// Upsert finds-or-creates the user at login, returning the canonical id + roles to mint
// claims from. localeHint is the best Accept-Language tag, if any; the user service applies it only when this upsert creates the account.
func (c *Client) Upsert(ctx context.Context, email, displayName string, avatarURL *string, localeHint string) (User, error) {
	body := userapi.UpsertUserJSONRequestBody{Email: email, DisplayName: displayName, AvatarUrl: avatarURL}
	if localeHint != "" {
		body.LocaleHint = &localeHint
	}
	resp, err := c.api.UpsertUserWithResponse(ctx, body)
	if err != nil {
		return User{}, fmt.Errorf("userclient: upsert: %w", err)
	}
	if resp.JSON200 == nil {
		return User{}, fmt.Errorf("userclient: upsert: status %d", resp.StatusCode())
	}
	return toUser(*resp.JSON200), nil
}

// Get fetches a user's current roles; refresh re-reads them so role changes land at the next rotation, not session end.
func (c *Client) Get(ctx context.Context, id uuid.UUID) (User, error) {
	resp, err := c.api.GetUserWithResponse(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("userclient: get: %w", err)
	}
	// Gate on the parsed problem body, not raw status: only a real problem+json 404 means the user
	// is gone. A misrouted URL or proxy error page shaped like a 404 must surface as a plain error, not a session revoke.
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
