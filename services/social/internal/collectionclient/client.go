// Package collectionclient resolves shelves through collection's
// /shared surface, relaying the end-user's own bearer. Social accepts
// no unvalidated shelf writes: a like or comment first proves the
// shelf exists and is non-private for the caller.
package collectionclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/collectionapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

var (
	ErrShelfNotFound = errors.New("collectionclient: shelf not found")
	ErrUpstream      = errors.New("collectionclient: upstream failure")
)

// Shelf is the subset social needs: identity, owner (denormalized
// onto edges), and visibility (the write-time gate).
type Shelf struct {
	ID         uuid.UUID
	OwnerID    uuid.UUID
	Name       string
	Slug       string
	Visibility string
}

type Client struct {
	api *collectionapi.ClientWithResponses
}

func New(baseURL string) (*Client, error) {
	api, err := collectionapi.NewClientWithResponses(baseURL, collectionapi.WithHTTPClient(httpkit.NewHTTPClient()))
	if err != nil {
		return nil, fmt.Errorf("collectionclient: %w", err)
	}
	return &Client{api: api}, nil
}

// SharedShelf resolves a shelf id. Collection's 404 covers both
// unknown and private (no existence oracle) - both are
// ErrShelfNotFound here.
func (c *Client) SharedShelf(ctx context.Context, bearer string, id uuid.UUID) (Shelf, error) {
	resp, err := c.api.GetSharedShelfWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Shelf{}, fmt.Errorf("collectionclient: shared shelf: %w", err)
	}
	if resp.ApplicationproblemJSON404 != nil {
		return Shelf{}, ErrShelfNotFound
	}
	if resp.JSON200 == nil {
		return Shelf{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	s := resp.JSON200
	return Shelf{
		ID: s.Id, OwnerID: s.OwnerId, Name: s.Name, Slug: s.Slug,
		Visibility: string(s.Visibility),
	}, nil
}
