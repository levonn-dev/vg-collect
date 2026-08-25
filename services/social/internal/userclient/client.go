// Package userclient reads profile cards from the user service's
// /shared surface, relaying the end-user's own bearer.
package userclient

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
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
	api, err := userapi.NewClientWithResponses(baseURL, userapi.WithHTTPClient(httpkit.NewHTTPClient()))
	if err != nil {
		return nil, fmt.Errorf("userclient: %w", err)
	}
	return &Client{api: api}, nil
}

// CardsByIDs batch-loads cards at all visibilities (the attribution
// surface); callers apply their own gates, e.g. non-private for follows.
func (c *Client) CardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]Card, error) {
	resp, err := c.api.GetSharedProfilesByIdsWithResponse(ctx,
		&userapi.GetSharedProfilesByIdsParams{Ids: ids}, httpkit.BearerEditor(bearer))
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
