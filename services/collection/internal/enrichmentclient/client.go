// Package enrichmentclient calls the enrichment service with the
// calling user's own Bearer token (uniform bearer hops: there is no
// service credential; the NetworkPolicy plus JWT validation cover the
// hop). It returns typed data: entry creation snapshots catalog facts
// from the product, and value composition consumes the price map.
package enrichmentclient

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
)

var (
	// ErrUnknownProduct means enrichment answered 404 for the requested id.
	ErrUnknownProduct = errors.New("enrichmentclient: unknown product")
	// ErrUnavailable means a transport failure or an out-of-contract answer.
	ErrUnavailable = errors.New("enrichmentclient: enrichment unavailable")
)

// batchLimit mirrors the prices:batch contract's maxItems.
const batchLimit = 500

// Client wraps the generated enrichapi typed client.
type Client struct {
	api *enrichapi.ClientWithResponses
}

// New builds a Client against baseURL using an otelhttp transport and
// a 10-second timeout.
func New(baseURL string) (*Client, error) {
	hc := &http.Client{
		Timeout:   10 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	api, err := enrichapi.NewClientWithResponses(baseURL, enrichapi.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("enrichmentclient: %w", err)
	}
	return &Client{api: api}, nil
}

func bearerEditor(bearer string) enrichapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+bearer)
		return nil
	}
}

// GetProduct fetches one catalog product.
func (c *Client) GetProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichapi.Product, error) {
	resp, err := c.api.GetProductWithResponse(ctx, id, bearerEditor(bearer))
	if err != nil {
		return enrichapi.Product{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	switch resp.StatusCode() {
	case http.StatusOK:
		if resp.JSON200 == nil {
			return enrichapi.Product{}, fmt.Errorf("%w: malformed 200 body", ErrUnavailable)
		}
		return *resp.JSON200, nil
	case http.StatusNotFound:
		return enrichapi.Product{}, ErrUnknownProduct
	default:
		return enrichapi.Product{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode())
	}
}

// Resolve finds-or-creates the canonical product for an identity. The
// region-aware repoint paths use it to land an entry's region-correct
// sibling member; the caller's own bearer rides the hop.
func (c *Client) Resolve(ctx context.Context, bearer string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
	resp, err := c.api.ResolveProductWithResponse(ctx, req, bearerEditor(bearer))
	if err != nil {
		return enrichapi.Product{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return enrichapi.Product{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// Platform is one catalog platform with its alias knowledge, for the
// normalize-platforms lever.
type Platform struct {
	IGDBID  int64
	Name    string
	Aliases []string
}

// ListPlatforms fetches the canonical platform catalog (igdb id + name
// + aliases). The admin's own bearer rides the hop.
func (c *Client) ListPlatforms(ctx context.Context, bearer string) ([]Platform, error) {
	resp, err := c.api.ListPlatformsWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode())
	}
	out := make([]Platform, 0, len(resp.JSON200.Platforms))
	for _, p := range resp.JSON200.Platforms {
		out = append(out, Platform{IGDBID: p.IgdbId, Name: p.Name, Aliases: p.Aliases})
	}
	return out, nil
}

// CreateCommunityProduct asks enrichment to mint an anchor-less
// community product (approve_new's first phase). The admin's own
// bearer carries the role; enrichment enforces it again, so an
// unexpected 403 here is configuration skew and reads as
// unavailability.
func (c *Client) CreateCommunityProduct(ctx context.Context, bearer string, req enrichapi.CreateCommunityProductJSONRequestBody) (enrichapi.Product, error) {
	resp, err := c.api.CreateCommunityProductWithResponse(ctx, req, bearerEditor(bearer))
	if err != nil {
		return enrichapi.Product{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode() != http.StatusCreated || resp.JSON201 == nil {
		return enrichapi.Product{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode())
	}
	return *resp.JSON201, nil
}

// BatchPrices fetches current prices for a set of product ids in one
// or more contract-sized chunks (ids are deduplicated first) and
// merges the maps. An empty id set makes no call.
func (c *Client) BatchPrices(ctx context.Context, bearer string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
	uniq := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	out := make(map[string]enrichapi.ProductPrices, len(uniq))
	for start := 0; start < len(uniq); start += batchLimit {
		end := min(start+batchLimit, len(uniq))
		resp, err := c.api.BatchPricesWithResponse(ctx,
			enrichapi.BatchPricesJSONRequestBody{ProductIds: uniq[start:end]},
			bearerEditor(bearer))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
			return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode())
		}
		maps.Copy(out, resp.JSON200.Prices)
	}
	return out, nil
}

// PriceHistory fetches snapshot series for a set of product ids in one
// or more contract-sized chunks (ids are deduplicated first) and
// merges the maps. An empty id set makes no call.
func (c *Client) PriceHistory(ctx context.Context, bearer string, ids []uuid.UUID, days int) (map[string][]enrichapi.PricePoint, error) {
	uniq := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	out := make(map[string][]enrichapi.PricePoint, len(uniq))
	for start := 0; start < len(uniq); start += batchLimit {
		end := min(start+batchLimit, len(uniq))
		resp, err := c.api.BatchPriceHistoryWithResponse(ctx,
			enrichapi.BatchPriceHistoryJSONRequestBody{ProductIds: uniq[start:end], Days: &days},
			bearerEditor(bearer))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
			return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode())
		}
		maps.Copy(out, resp.JSON200.Series)
	}
	return out, nil
}
