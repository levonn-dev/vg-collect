// Package collectionclient calls the collection service with the
// user's own Bearer token, via the generated typed client. The bff
// serves these routes as verbatim relays (single-source pass-throughs
// are never cached at the bff): most methods return the upstream
// status, content type, and raw body for the statuses the bff relays,
// and ErrUpstream for everything else. LibrarySummary is the one
// typed read - the bff consumes it itself to compose recommendations.
package collectionclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/collectionapi"
)

// ErrUpstream: the collection service answered outside its relayed
// contract (or an infrastructure layer answered for it).
var ErrUpstream = errors.New("collectionclient: upstream failure")

// Result is one relayable upstream answer.
type Result struct {
	Status      int
	ContentType string
	Body        []byte
}

// Client wraps the generated collectionapi typed client.
type Client struct {
	api *collectionapi.ClientWithResponses
}

// New builds a Client against baseURL using an otelhttp transport and
// a 10-second timeout.
func New(baseURL string) (*Client, error) {
	hc := &http.Client{
		Timeout:   10 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	api, err := collectionapi.NewClientWithResponses(baseURL, collectionapi.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("collectionclient: %w", err)
	}
	return &Client{api: api}, nil
}

func bearerEditor(bearer string) collectionapi.RequestEditorFn {
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

func ct(resp *http.Response) string { return resp.Header.Get("Content-Type") }

// ListEntries relays GET /entries with the already-converted params.
func (c *Client) ListEntries(ctx context.Context, bearer string, params *collectionapi.ListEntriesParams) (Result, error) {
	resp, err := c.api.ListEntriesWithResponse(ctx, params, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: list entries: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusOK, http.StatusBadRequest)
}

// CreateEntry relays POST /entries (browser body passes through
// untouched; the collection service owns its validation).
func (c *Client) CreateEntry(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateEntryWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: create entry: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body,
		http.StatusCreated, http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway)
}

// GetEntry relays GET /entries/{id}.
func (c *Client) GetEntry(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.GetEntryWithResponse(ctx, id, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: get entry: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusOK, http.StatusNotFound)
}

// UpdateEntry relays PUT /entries/{id}.
func (c *Client) UpdateEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.UpdateEntryWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: update entry: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway)
}

// DeleteEntry relays DELETE /entries/{id}.
func (c *Client) DeleteEntry(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteEntryWithResponse(ctx, id, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: delete entry: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusNoContent, http.StatusNotFound)
}

// ReorderEntry relays POST /entries/{id}/reorder.
func (c *Client) ReorderEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.ReorderEntryWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: reorder entry: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict)
}

// ListTags relays GET /tags.
func (c *Client) ListTags(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.ListTagsWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: list tags: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusOK)
}

// CreateTag relays POST /tags.
func (c *Client) CreateTag(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateTagWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: create tag: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body,
		http.StatusCreated, http.StatusBadRequest, http.StatusConflict)
}

// RenameTag relays PUT /tags/{id}.
func (c *Client) RenameTag(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.RenameTagWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: rename tag: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict)
}

// DeleteTag relays DELETE /tags/{id}.
func (c *Client) DeleteTag(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteTagWithResponse(ctx, id, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: delete tag: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusNoContent, http.StatusNotFound)
}

// ListViews relays GET /views.
func (c *Client) ListViews(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.ListViewsWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: list views: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusOK)
}

// CreateView relays POST /views.
func (c *Client) CreateView(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateViewWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: create view: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body,
		http.StatusCreated, http.StatusBadRequest, http.StatusConflict)
}

// UpdateView relays PUT /views/{id}.
func (c *Client) UpdateView(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.UpdateViewWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: update view: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict)
}

// DeleteView relays DELETE /views/{id}.
func (c *Client) DeleteView(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteViewWithResponse(ctx, id, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: delete view: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusNoContent, http.StatusNotFound)
}

// GetDashboard relays GET /dashboard with the already-converted
// filter params (cached by the collection service, never here).
func (c *Client) GetDashboard(ctx context.Context, bearer string, params *collectionapi.GetDashboardParams) (Result, error) {
	resp, err := c.api.GetDashboardWithResponse(ctx, params, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: dashboard: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusOK, http.StatusBadRequest)
}

// GetValueHistory relays GET /dashboard/value-history (cached by the
// collection service, never here).
func (c *Client) GetValueHistory(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.GetValueHistoryWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: value history: %w", err)
	}
	return relay(resp.StatusCode(), ct(resp.HTTPResponse), resp.Body, http.StatusOK)
}

// LibrarySummary is the typed read backing the recommendations
// composition; it is not relayed to browsers.
func (c *Client) LibrarySummary(ctx context.Context, bearer string) (collectionapi.LibrarySummary, error) {
	resp, err := c.api.GetLibrarySummaryWithResponse(ctx, bearerEditor(bearer))
	if err != nil {
		return collectionapi.LibrarySummary{}, fmt.Errorf("collectionclient: library summary: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return collectionapi.LibrarySummary{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return *resp.JSON200, nil
}
