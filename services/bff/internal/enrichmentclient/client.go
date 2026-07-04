// Package enrichmentclient calls the enrichment service with the
// user's own Bearer token, via the generated typed client. The bff
// serves these routes as verbatim relays (single-source pass-throughs
// are never cached at the bff), so methods return the upstream status,
// content type, and raw body for the statuses the bff relays, and
// ErrUpstream for everything else.
package enrichmentclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/enrichapi"
)

// ErrUpstream: the enrichment service answered outside its relayed
// contract (or an infrastructure layer answered for it).
var ErrUpstream = errors.New("enrichmentclient: upstream failure")

// Result is one relayable upstream answer.
type Result struct {
	Status      int
	ContentType string
	Body        []byte
}

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

// relay admits the upstream answer when its status is in allowed; a
// 401/403/5xx from a service the bff holds a valid session for is an
// infrastructure fault, not a user condition.
func relay(status int, contentType string, body []byte, allowed ...int) (Result, error) {
	for _, a := range allowed {
		if status == a {
			return Result{Status: status, ContentType: contentType, Body: body}, nil
		}
	}
	return Result{}, fmt.Errorf("%w: status %d", ErrUpstream, status)
}

// Search relays GET /search.
func (c *Client) Search(ctx context.Context, bearer, typ, q string) (Result, error) {
	params := &enrichapi.SearchCatalogParams{Type: enrichapi.SearchCatalogParamsType(typ), Q: q}
	resp, err := c.api.SearchCatalogWithResponse(ctx, params, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: search: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusBadRequest)
}

// Resolve relays POST /products/resolve (the browser body passes
// through untouched; enrichment owns its validation).
func (c *Client) Resolve(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.ResolveProductWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: resolve: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway)
}

// Product relays GET /products/{id}.
func (c *Client) Product(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.GetProductWithResponse(ctx, id, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: product: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusNotFound)
}
