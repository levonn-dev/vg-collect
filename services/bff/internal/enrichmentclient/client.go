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

// FX relays GET /fx/latest. A 502 is a relayable answer (cold fx
// cache upstream), not a client failure.
func (c *Client) FX(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.GetFxLatestWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: fx: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusBadGateway)
}

// ListPlatforms relays GET /platforms. A 502 is a relayable answer
// (cold platform catalog upstream), not a client failure.
func (c *Client) ListPlatforms(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.ListPlatformsWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: platforms: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusBadGateway)
}

// UnmatchedProducts relays GET /admin/products/unmatched. Enrichment
// enforces the admin role, so its 403 is a relayable user answer
// here, not an infrastructure fault.
func (c *Client) UnmatchedProducts(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (Result, error) {
	resp, err := c.api.ListUnmatchedProductsWithResponse(ctx, params, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: unmatched products: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusForbidden)
}

// CommunityProducts relays GET /admin/products/community. Enrichment
// enforces the admin role, so its 403 is a relayable user answer
// here, not an infrastructure fault.
func (c *Client) CommunityProducts(ctx context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (Result, error) {
	resp, err := c.api.ListCommunityProductsWithResponse(ctx, params, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: community products: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusForbidden)
}

// SetProductMapping relays PUT /admin/products/{id}/pricecharting
// with the browser's body untouched; every contract answer (identity
// conflicts included) passes through.
func (c *Client) SetProductMapping(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.SetProductMappingWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: set product mapping: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusBadGateway)
}

// DeleteProduct relays DELETE /admin/products/{id} - the guarded
// residue mop (204; 409 product_matched for matched products). The
// bff ran the entry-reference check before calling.
func (c *Client) DeleteProduct(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteProductWithResponse(ctx, id, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: delete product: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusNoContent, http.StatusForbidden, http.StatusNotFound, http.StatusConflict)
}

// CreateCommunityProduct relays the admin community mint.
func (c *Client) CreateCommunityProduct(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateCommunityProductWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: create community product: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusCreated, http.StatusBadRequest, http.StatusForbidden)
}

// PromoteProduct relays the in-place promotion.
func (c *Client) PromoteProduct(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.PromoteProductWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: promote product: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusBadGateway)
}

// PromoteCandidates relays the sweep worklist read.
func (c *Client) PromoteCandidates(ctx context.Context, bearer string, params *enrichapi.ListPromoteCandidatesParams) (Result, error) {
	resp, err := c.api.ListPromoteCandidatesWithResponse(ctx, params, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: promote candidates: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusOK, http.StatusForbidden)
}

// DismissPromoteCandidate relays a candidate dismissal.
func (c *Client) DismissPromoteCandidate(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.DismissPromoteCandidateWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: dismiss candidate: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusNoContent, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)
}

// TriggerRefresh relays POST /admin/refresh (202 started, 403, 409
// refresh_in_progress).
func (c *Client) TriggerRefresh(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.TriggerRefreshWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: trigger refresh: %w", err)
	}
	return relay(resp.StatusCode(), resp.HTTPResponse.Header.Get("Content-Type"), resp.Body,
		http.StatusAccepted, http.StatusForbidden, http.StatusConflict)
}

// Score calls the recommendation scorer with the user's own token,
// returning the raw 200 body plus its decoded degraded flag (the
// caller caches only non-degraded results). Any other answer is an
// infrastructure fault.
func (c *Client) Score(ctx context.Context, bearer string, req enrichapi.ScoreRequest) ([]byte, bool, error) {
	resp, err := c.api.ScoreRecommendationsWithResponse(ctx, req, bearerEditor(bearer))
	if err != nil {
		return nil, false, fmt.Errorf("enrichmentclient: score: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, false, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.Body, resp.JSON200.Degraded, nil
}
