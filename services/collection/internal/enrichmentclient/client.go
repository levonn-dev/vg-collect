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
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/services/collection/internal/gen/enrichapi"
)

var (
	// ErrUnknownProduct: enrichment answered 404 for the requested id.
	ErrUnknownProduct = errors.New("enrichmentclient: unknown product")
	// ErrUnavailable: a transport failure or an out-of-contract answer.
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
		for k, v := range resp.JSON200.Prices {
			out[k] = v
		}
	}
	return out, nil
}
