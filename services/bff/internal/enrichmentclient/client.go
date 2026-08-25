// Package enrichmentclient calls the enrichment service with the user's
// bearer token. Methods relay the upstream status, content type, and
// body verbatim (uncached) and return ErrUpstream otherwise.
package enrichmentclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

// ErrUpstream means the answer was outside the relayed contract (enrichment or infrastructure).
var ErrUpstream = errors.New("enrichmentclient: upstream failure")

// Result is one relayable upstream answer.
type Result = httpkit.RelayResult

// Client wraps the generated enrichapi typed client.
type Client struct {
	api *enrichapi.ClientWithResponses
}

// New builds a Client against baseURL with an otelhttp transport and a 10s timeout.
func New(baseURL string) (*Client, error) {
	api, err := enrichapi.NewClientWithResponses(baseURL, enrichapi.WithHTTPClient(httpkit.NewHTTPClient()))
	if err != nil {
		return nil, fmt.Errorf("enrichmentclient: %w", err)
	}
	return &Client{api: api}, nil
}

// Search relays GET /search.
func (c *Client) Search(ctx context.Context, bearer, typ, q string) (Result, error) {
	params := &enrichapi.SearchCatalogParams{Type: common.SearchResultType(typ), Q: q}
	resp, err := c.api.SearchCatalogWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: search: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest)
}

// Resolve relays POST /products/resolve; browser body passes through untouched, enrichment owns validation.
func (c *Client) Resolve(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.ResolveProductWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: resolve: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway)
}

// Product relays GET /products/{id}.
func (c *Client) Product(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.GetProductWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: product: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusNotFound)
}

// FX relays GET /fx/latest; a 502 (cold fx cache) is a relayable answer, not a client failure.
func (c *Client) FX(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.GetFxLatestWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: fx: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadGateway)
}

// ListPlatforms relays GET /platforms; a 502 (cold catalog) is a relayable answer, not a client failure.
func (c *Client) ListPlatforms(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.ListPlatformsWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: platforms: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadGateway)
}

// UnmatchedProducts relays GET /admin/products/unmatched; enrichment
// enforces the admin role, so its 403 is a relayable user answer.
func (c *Client) UnmatchedProducts(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (Result, error) {
	resp, err := c.api.ListUnmatchedProductsWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: unmatched products: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// CommunityProducts relays GET /admin/products/community; enrichment
// enforces the admin role, so its 403 is a relayable user answer.
func (c *Client) CommunityProducts(ctx context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (Result, error) {
	resp, err := c.api.ListCommunityProductsWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: community products: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// SetProductMapping relays PUT .../pricecharting with the browser body
// untouched; every contract answer, including identity conflicts, passes through.
func (c *Client) SetProductMapping(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.SetProductMappingWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: set product mapping: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusBadGateway)
}

// DeleteProduct relays DELETE /admin/products/{id}, the guarded residue
// mop (204; 409 product_matched); the bff ran the entry-reference check before calling.
func (c *Client) DeleteProduct(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteProductWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: delete product: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusForbidden, http.StatusNotFound, http.StatusConflict)
}

// CreateCommunityProduct relays the admin community mint.
func (c *Client) CreateCommunityProduct(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateCommunityProductWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: create community product: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusCreated, http.StatusBadRequest, http.StatusForbidden)
}

// PromoteProduct relays the in-place promotion.
func (c *Client) PromoteProduct(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.PromoteProductWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: promote product: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusBadGateway)
}

// PromoteCandidates relays the sweep worklist read.
func (c *Client) PromoteCandidates(ctx context.Context, bearer string, params *enrichapi.ListPromoteCandidatesParams) (Result, error) {
	resp, err := c.api.ListPromoteCandidatesWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: promote candidates: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// DismissPromoteCandidate relays a candidate dismissal.
func (c *Client) DismissPromoteCandidate(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.DismissPromoteCandidateWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: dismiss candidate: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound)
}

// TriggerRefresh relays POST /admin/refresh (202 started, 403, 409 refresh_in_progress).
func (c *Client) TriggerRefresh(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.TriggerRefreshWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: trigger refresh: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusAccepted, http.StatusForbidden, http.StatusConflict)
}

// NormalizeCommunityRegions relays POST /internal/normalize-community-regions
// (200 sweep summary, 403); enrichment also accepts a service token, but the bff forwards only the admin's bearer.
func (c *Client) NormalizeCommunityRegions(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.InternalNormalizeCommunityRegionsWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("enrichmentclient: normalize community regions: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// Score returns the raw 200 body plus its decoded degraded flag (callers
// cache only non-degraded results); any other answer is an infrastructure fault.
func (c *Client) Score(ctx context.Context, bearer string, req enrichapi.ScoreRequest) ([]byte, bool, error) {
	resp, err := c.api.ScoreRecommendationsWithResponse(ctx, req, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, false, fmt.Errorf("enrichmentclient: score: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, false, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.Body, resp.JSON200.Degraded, nil
}
