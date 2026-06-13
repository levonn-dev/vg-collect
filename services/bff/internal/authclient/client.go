// Package authclient calls the auth service through the generated
// typed client and translates its problem responses into the small
// error taxonomy the bff branches on.
package authclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/authapi"
)

var (
	// ErrLoginFailed covers every "this login attempt is bad" answer
	// (unknown provider, bad state, unknown fixture, dev disabled); the
	// browser lands back on the login page either way.
	ErrLoginFailed = errors.New("authclient: login failed")
	// ErrEmailUnverified is the verified-email policy refusing a login.
	ErrEmailUnverified = errors.New("authclient: provider did not assert a verified email")
	// ErrProviderError is the identity provider misbehaving (retryable).
	ErrProviderError = errors.New("authclient: identity provider error")
	// ErrRefreshRejected is returned for a non-reuse 401: the token is
	// expired, unknown, or the user was deleted. The session is dead;
	// no chain was revoked and no jtis need denylisting.
	ErrRefreshRejected = errors.New("authclient: refresh token rejected")
	// ErrUserUnavailable means refresh could not consult the role
	// source; the token was NOT consumed and the same one retries.
	ErrUserUnavailable = errors.New("authclient: role source unavailable")
)

// ReusedError is refresh-token reuse: the chain is revoked and any
// possibly-live access-token jtis ride along for denylisting (non-empty
// only on the first detection).
type ReusedError struct {
	RevokeJTIs []string
}

func (e *ReusedError) Error() string { return "authclient: refresh token reuse detected" }

// TokenPair mirrors the auth service's token response.
type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int64
	RefreshExpiresIn int64
}

// Client wraps the generated authapi typed client.
type Client struct {
	api *authapi.ClientWithResponses
}

// New builds a Client against baseURL using an otelhttp transport and a
// 10-second timeout.
func New(baseURL string) (*Client, error) {
	hc := &http.Client{
		Timeout:   10 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	api, err := authapi.NewClientWithResponses(baseURL, authapi.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("authclient: %w", err)
	}
	return &Client{api: api}, nil
}

// Start begins a real-provider login and returns the authorize URL.
func (c *Client) Start(ctx context.Context, provider string) (string, error) {
	resp, err := c.api.OauthStartWithResponse(ctx, authapi.StartRequest{
		Provider: authapi.StartRequestProvider(provider),
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

// Callback completes a real-provider login.
func (c *Client) Callback(ctx context.Context, code, state string) (TokenPair, error) {
	resp, err := c.api.OauthCallbackWithResponse(ctx, authapi.CallbackRequest{Code: code, State: state})
	if err != nil {
		return TokenPair{}, fmt.Errorf("authclient: callback: %w", err)
	}
	switch {
	case resp.JSON200 != nil:
		return toPair(*resp.JSON200), nil
	case resp.ApplicationproblemJSON400 != nil:
		return TokenPair{}, ErrLoginFailed
	case resp.ApplicationproblemJSON403 != nil:
		return TokenPair{}, ErrEmailUnverified
	case resp.ApplicationproblemJSON502 != nil:
		return TokenPair{}, ErrProviderError
	default:
		return TokenPair{}, fmt.Errorf("authclient: callback: status %d", resp.StatusCode())
	}
}

// DevToken logs a dev fixture in (the provider answers 404 when the
// dev adapter is disabled, 400 for unknown fixtures).
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

// Refresh rotates a refresh token. Returns ReusedError when the token
// has already been used (reuse detected), ErrRefreshRejected for other
// 401 cases, and ErrUserUnavailable when the role source is down.
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
