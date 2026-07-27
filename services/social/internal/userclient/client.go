// Package userclient reads profile cards from the user service's
// /shared surface, relaying the end-user's own bearer.
package userclient

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/services/social/internal/gen/userapi"
)

// Card mirrors the user service's ProfileCard.
type Card struct {
	UserID     uuid.UUID
	Handle     string
	AvatarURL  *string
	Visibility string
}

type Client struct {
	api *userapi.ClientWithResponses
}

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

// CardsByIDs batch-loads cards (all visibilities - the attribution
// surface); callers apply their own gates, e.g. follow validation
// requires non-private.
func (c *Client) CardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]Card, error) {
	resp, err := c.api.GetSharedProfilesByIdsWithResponse(ctx,
		&userapi.GetSharedProfilesByIdsParams{Ids: ids},
		func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+bearer)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("userclient: cards: %w", err)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("userclient: cards: status %d", resp.StatusCode())
	}
	out := make([]Card, len(resp.JSON200.Profiles))
	for i, p := range resp.JSON200.Profiles {
		out[i] = Card{
			UserID: p.UserId, Handle: p.Handle, AvatarURL: p.AvatarUrl,
			Visibility: string(p.ProfileVisibility),
		}
	}
	return out, nil
}
