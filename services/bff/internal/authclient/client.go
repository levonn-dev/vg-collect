// Package authclient calls the auth service and translates its problem
// responses into the small error taxonomy the bff branches on.
package authclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/authapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

var (
	// ErrLoginFailed covers any "this login attempt is bad" answer: unknown
	// provider, bad state, unknown fixture, or dev disabled.
	ErrLoginFailed = errors.New("authclient: login failed")
	// ErrEmailUnverified is the verified-email policy refusing a login.
	ErrEmailUnverified = errors.New("authclient: provider did not assert a verified email")
	// ErrProviderError is the identity provider misbehaving (retryable).
	ErrProviderError = errors.New("authclient: identity provider error")
	// ErrRefreshRejected is a non-reuse 401 (expired, unknown, or deleted
	// user): the session is dead, no chain revoked, no jtis to denylist.
	ErrRefreshRejected = errors.New("authclient: refresh token rejected")
	// ErrUserUnavailable means the role source is down and the unconsumed token retries unchanged.
	ErrUserUnavailable = errors.New("authclient: role source unavailable")
	// ErrLinkConflict means the identity is already linked (auth 409 identity_already_linked).
	ErrLinkConflict = errors.New("authclient: identity already linked to another account")
	// ErrLinkEmailUnverified is the verified-email policy refusing a link.
	ErrLinkEmailUnverified = errors.New("authclient: provider did not assert a verified email for link")
	// ErrLastIdentity is the refusal to unlink an account's only login.
	ErrLastIdentity = errors.New("authclient: cannot unlink the last login")
	// ErrIdentityNotFound means no such identity on this account.
	ErrIdentityNotFound = errors.New("authclient: identity not found")
)

// ReusedError is refresh-token reuse: the chain is revoked; RevokeJTIs
// holds live access-token jtis to denylist, non-empty only on first detection.
type ReusedError struct {
	RevokeJTIs []string
}

func (e *ReusedError) Error() string { return "authclient: refresh token reuse detected" }

// TokenPair mirrors the auth service's token response. LinkedProvider
// is set only when Callback/DevLink completed a link flow, not a login.
type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int64
	RefreshExpiresIn int64
	LinkedProvider   *string
}

// Client wraps the generated authapi typed client.
type Client struct {
	api *authapi.ClientWithResponses
}

// New builds a Client against baseURL with an otelhttp transport and a 10s timeout.
func New(baseURL string) (*Client, error) {
	api, err := authapi.NewClientWithResponses(baseURL, authapi.WithHTTPClient(httpkit.NewHTTPClient()))
	if err != nil {
		return nil, fmt.Errorf("authclient: %w", err)
	}
	return &Client{api: api}, nil
}

// Start begins a real-provider login and returns the authorize URL.
func (c *Client) Start(ctx context.Context, provider string) (string, error) {
	resp, err := c.api.OauthStartWithResponse(ctx, authapi.StartRequest{
		Provider: authapi.OAuthProvider(provider),
	})
	if err != nil {
		return "", fmt.Errorf("authclient: start: %w", err)
	}
	switch {
	case resp.JSON200 != nil:
		return resp.JSON200.AuthorizeUrl, nil
	case resp.ApplicationproblemJSON400 != nil:
		return "", ErrLoginFailed
	case resp.ApplicationproblemJSON502 != nil:
		return "", ErrProviderError
	default:
		return "", fmt.Errorf("authclient: start: status %d", resp.StatusCode())
	}
}

// Callback completes a login, or a link if the consumed state was a link flow.
func (c *Client) Callback(ctx context.Context, code, state string) (TokenPair, error) {
	resp, err := c.api.OauthCallbackWithResponse(ctx, authapi.CallbackRequest{Code: code, State: state})
	if err != nil {
		return TokenPair{}, fmt.Errorf("authclient: callback: %w", err)
	}
	switch {
	case resp.JSON200 != nil:
		p := *resp.JSON200
		return TokenPair{
			AccessToken:      p.AccessToken,
			RefreshToken:     p.RefreshToken,
			ExpiresIn:        p.ExpiresIn,
			RefreshExpiresIn: p.RefreshExpiresIn,
			LinkedProvider:   p.LinkedProvider,
		}, nil
	case resp.ApplicationproblemJSON400 != nil:
		return TokenPair{}, ErrLoginFailed
	case resp.ApplicationproblemJSON403 != nil:
		if p := resp.ApplicationproblemJSON403; p.Code != nil && *p.Code == "link_email_unverified" {
			return TokenPair{}, ErrLinkEmailUnverified
		}
		return TokenPair{}, ErrEmailUnverified
	case resp.ApplicationproblemJSON409 != nil:
		return TokenPair{}, ErrLinkConflict
	case resp.ApplicationproblemJSON502 != nil:
		return TokenPair{}, ErrProviderError
	default:
		return TokenPair{}, fmt.Errorf("authclient: callback: status %d", resp.StatusCode())
	}
}

// DevToken logs a dev fixture in: 404 when the dev adapter is disabled, 400 for unknown fixtures.
func (c *Client) DevToken(ctx context.Context, user string) (TokenPair, error) {
	resp, err := c.api.DevTokenWithResponse(ctx, authapi.DevTokenRequest{User: user})
	if err != nil {
		return TokenPair{}, fmt.Errorf("authclient: dev token: %w", err)
	}
	switch {
	case resp.JSON200 != nil:
		return toPair(*resp.JSON200), nil
	case resp.ApplicationproblemJSON400 != nil, resp.ApplicationproblemJSON404 != nil:
		return TokenPair{}, ErrLoginFailed
	default:
		return TokenPair{}, fmt.Errorf("authclient: dev token: status %d", resp.StatusCode())
	}
}

// Refresh rotates a refresh token, returning ReusedError on detected reuse,
// ErrRefreshRejected for other 401s, or ErrUserUnavailable if the role source is down.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	resp, err := c.api.RefreshTokenWithResponse(ctx, authapi.RefreshRequest{RefreshToken: refreshToken})
	if err != nil {
		return TokenPair{}, fmt.Errorf("authclient: refresh: %w", err)
	}
	switch {
	case resp.JSON200 != nil:
		return toPair(*resp.JSON200), nil
	case resp.ApplicationproblemJSON401 != nil:
		p := resp.ApplicationproblemJSON401
		if p.Code != nil && *p.Code == "refresh_reused" {
			var jtis []string
			if p.RevokeJtis != nil {
				jtis = *p.RevokeJtis
			}
			return TokenPair{}, &ReusedError{RevokeJTIs: jtis}
		}
		return TokenPair{}, ErrRefreshRejected
	case resp.ApplicationproblemJSON503 != nil:
		return TokenPair{}, ErrUserUnavailable
	default:
		return TokenPair{}, fmt.Errorf("authclient: refresh: status %d", resp.StatusCode())
	}
}

// Revoke kills a refresh token's whole chain (logout). Idempotent.
func (c *Client) Revoke(ctx context.Context, refreshToken string) error {
	resp, err := c.api.RevokeTokenWithResponse(ctx, authapi.RevokeRequest{RefreshToken: refreshToken})
	if err != nil {
		return fmt.Errorf("authclient: revoke: %w", err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("authclient: revoke: status %d", resp.StatusCode())
	}
	return nil
}

// Providers lists the login options the auth service has enabled.
func (c *Client) Providers(ctx context.Context) ([]string, error) {
	resp, err := c.api.ListProvidersWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("authclient: providers: %w", err)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("authclient: providers: status %d", resp.StatusCode())
	}
	return resp.JSON200.Providers, nil
}

func toPair(p authapi.TokenPair) TokenPair {
	return TokenPair{
		AccessToken:      p.AccessToken,
		RefreshToken:     p.RefreshToken,
		ExpiresIn:        p.ExpiresIn,
		RefreshExpiresIn: p.RefreshExpiresIn,
	}
}

// LinkStart begins linking a real provider to the session's account.
func (c *Client) LinkStart(ctx context.Context, provider, bearer string) (string, error) {
	resp, err := c.api.OauthLinkStartWithResponse(ctx, authapi.LinkStartRequest{
		Provider: authapi.OAuthProvider(provider),
	}, httpkit.BearerEditor(bearer))
	if err != nil {
		return "", fmt.Errorf("authclient: link start: %w", err)
	}
	if resp.JSON200 == nil {
		return "", ErrLoginFailed
	}
	return resp.JSON200.AuthorizeUrl, nil
}

// DevLink links a dev fixture identity to the account in one hop (no external IdP round trip).
func (c *Client) DevLink(ctx context.Context, user, bearer string) (TokenPair, error) {
	resp, err := c.api.DevLinkWithResponse(ctx, authapi.DevLinkRequest{User: user}, httpkit.BearerEditor(bearer))
	if err != nil {
		return TokenPair{}, fmt.Errorf("authclient: dev link: %w", err)
	}
	switch {
	case resp.JSON200 != nil:
		p := *resp.JSON200
		return TokenPair{
			AccessToken:      p.AccessToken,
			RefreshToken:     p.RefreshToken,
			ExpiresIn:        p.ExpiresIn,
			RefreshExpiresIn: p.RefreshExpiresIn,
			LinkedProvider:   p.LinkedProvider,
		}, nil
	case resp.ApplicationproblemJSON409 != nil:
		return TokenPair{}, ErrLinkConflict
	default:
		return TokenPair{}, ErrLoginFailed
	}
}

// ListIdentities fetches the linked logins for the account page.
func (c *Client) ListIdentities(ctx context.Context, userID, bearer string) ([]common.Identity, error) {
	uid, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	resp, err := c.api.ListIdentitiesWithResponse(ctx, uid, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("authclient: list identities: %w", err)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("authclient: list identities: status %d", resp.StatusCode())
	}
	return resp.JSON200.Identities, nil
}

// DeleteIdentity unlinks a login; sentinel errors cover not-found and last-login refusals.
func (c *Client) DeleteIdentity(ctx context.Context, identityID uuid.UUID, bearer string) error {
	resp, err := c.api.DeleteIdentityWithResponse(ctx, identityID, httpkit.BearerEditor(bearer))
	if err != nil {
		return fmt.Errorf("authclient: delete identity: %w", err)
	}
	switch resp.StatusCode() {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return ErrIdentityNotFound
	case http.StatusConflict:
		return ErrLastIdentity
	default:
		return fmt.Errorf("authclient: delete identity: status %d", resp.StatusCode())
	}
}

// DeleteUserAuth erases the account's identities and refresh families (one leg of account deletion).
func (c *Client) DeleteUserAuth(ctx context.Context, userID, bearer string) error {
	uid, err := parseUserID(userID)
	if err != nil {
		return err
	}
	resp, err := c.api.DeleteUserAuthWithResponse(ctx, uid, httpkit.BearerEditor(bearer))
	if err != nil {
		return fmt.Errorf("authclient: delete user auth: %w", err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("authclient: delete user auth: status %d", resp.StatusCode())
	}
	return nil
}

// parseUserID converts a path-supplied id to a uuid in this package's error
// taxonomy. Duplicated in userclient; not worth a shared package for it.
func parseUserID(id string) (uuid.UUID, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("authclient: bad user id: %w", err)
	}
	return uid, nil
}
