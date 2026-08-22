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

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/collectionapi"
	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

// ErrUpstream means the collection service answered outside its
// relayed contract (or an infrastructure layer answered for it).
var ErrUpstream = errors.New("collectionclient: upstream failure")

// ErrShelfNotFound is returned when the collection service issues a
// parsed problem+json 404 for a single-shelf resolve: unknown and
// private shelves are deliberately indistinguishable.
var ErrShelfNotFound = errors.New("collectionclient: shelf not found")

// Result is one relayable upstream answer.
type Result = httpkit.RelayResult

// Client wraps the generated collectionapi typed client.
type Client struct {
	api *collectionapi.ClientWithResponses
}

// New builds a Client against baseURL using an otelhttp transport and
// a 10-second timeout.
func New(baseURL string) (*Client, error) {
	api, err := collectionapi.NewClientWithResponses(baseURL, collectionapi.WithHTTPClient(httpkit.NewHTTPClient()))
	if err != nil {
		return nil, fmt.Errorf("collectionclient: %w", err)
	}
	return &Client{api: api}, nil
}

// CountProductReferences asks collection for a product's entry
// reference count - the admin delete's safety read. Collection
// enforces role admin, so its 403 is a relayable user answer.
func (c *Client) CountProductReferences(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.CountProductReferencesWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: count product references: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// CreateSubmission relays POST /entries/{id}/submission.
func (c *Client) CreateSubmission(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.CreateSubmissionWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: create submission: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusCreated, http.StatusBadRequest, http.StatusNotFound,
		http.StatusConflict, http.StatusTooManyRequests)
}

// GetSubmission relays GET /entries/{id}/submission.
func (c *Client) GetSubmission(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.GetSubmissionWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: get submission: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusNotFound)
}

// CancelSubmission relays DELETE /entries/{id}/submission.
func (c *Client) CancelSubmission(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.CancelSubmissionWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: cancel submission: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusNotFound)
}

// AckSubmission relays POST /entries/{id}/submission/ack.
func (c *Client) AckSubmission(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.AckSubmissionResolutionWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: ack submission: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusNotFound)
}

// ListSubmissions relays the admin queue read.
func (c *Client) ListSubmissions(ctx context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (Result, error) {
	resp, err := c.api.ListSubmissionsWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: list submissions: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// SubmitVerdict relays the admin verdict with the browser's body
// untouched; every contract answer passes through.
func (c *Client) SubmitVerdict(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.SubmitVerdictWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: submit verdict: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusBadGateway)
}

// TriggerRematch relays POST /internal/rematch-entries (202 started,
// 403, 409 rematch_in_progress).
func (c *Client) TriggerRematch(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.InternalRematchEntriesWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: trigger rematch: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusAccepted, http.StatusForbidden, http.StatusConflict)
}

// Resnapshot relays POST /internal/resnapshot (200 sweep summary,
// 403; collection also accepts a service token, but the bff only
// ever forwards the admin's own bearer).
func (c *Client) Resnapshot(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.InternalResnapshotWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: resnapshot: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// NormalizePlatforms relays POST /internal/normalize-platforms (200
// sweep summary, 403; collection also accepts a service token, but
// the bff only ever forwards the admin's own bearer).
func (c *Client) NormalizePlatforms(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.InternalNormalizePlatformsWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: normalize platforms: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// NormalizeRegions relays POST /internal/normalize-regions (200 sweep
// summary, 403).
func (c *Client) NormalizeRegions(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.InternalNormalizeRegionsWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: normalize regions: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusForbidden)
}

// ListEntries relays GET /entries with the already-converted params.
func (c *Client) ListEntries(ctx context.Context, bearer string, params *collectionapi.ListEntriesParams) (Result, error) {
	resp, err := c.api.ListEntriesWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: list entries: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK, http.StatusBadRequest)
}

// CreateEntry relays POST /entries (browser body passes through
// untouched; the collection service owns its validation).
func (c *Client) CreateEntry(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateEntryWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: create entry: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusCreated, http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway)
}

// GetEntry relays GET /entries/{id}.
func (c *Client) GetEntry(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.GetEntryWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: get entry: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK, http.StatusNotFound)
}

// UpdateEntry relays PUT /entries/{id}.
func (c *Client) UpdateEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.UpdateEntryWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: update entry: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway)
}

// DeleteEntry relays DELETE /entries/{id}.
func (c *Client) DeleteEntry(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteEntryWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: delete entry: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusNoContent, http.StatusNotFound)
}

// AckRegionMismatch relays POST /entries/{id}/region-mismatch-ack.
func (c *Client) AckRegionMismatch(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.AckEntryRegionMismatchWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: ack region mismatch: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusNoContent, http.StatusNotFound)
}

// ReorderEntry relays POST /entries/{id}/reorder.
func (c *Client) ReorderEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.ReorderEntryWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: reorder entry: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict)
}

// BulkUpdateEntries relays POST /entries/bulk-update (browser body
// passes through untouched; the collection service owns both the
// bounds/no-action guards and the per-entry tag cap).
func (c *Client) BulkUpdateEntries(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.BulkUpdateEntriesWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: bulk update entries: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK, http.StatusBadRequest)
}

// ListTags relays GET /tags.
func (c *Client) ListTags(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.ListTagsWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: list tags: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK)
}

// CreateTag relays POST /tags.
func (c *Client) CreateTag(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateTagWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: create tag: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusCreated, http.StatusBadRequest, http.StatusConflict, http.StatusTooManyRequests)
}

// RenameTag relays PUT /tags/{id}.
func (c *Client) RenameTag(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.RenameTagWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: rename tag: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict)
}

// DeleteTag relays DELETE /tags/{id}.
func (c *Client) DeleteTag(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteTagWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: delete tag: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusNoContent, http.StatusNotFound)
}

// ListViews relays GET /views.
func (c *Client) ListViews(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.ListViewsWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: list views: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK)
}

// CreateView relays POST /views.
func (c *Client) CreateView(ctx context.Context, bearer string, body []byte) (Result, error) {
	resp, err := c.api.CreateViewWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: create view: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusCreated, http.StatusBadRequest, http.StatusConflict)
}

// UpdateView relays PUT /views/{id}.
func (c *Client) UpdateView(ctx context.Context, bearer string, id uuid.UUID, body []byte) (Result, error) {
	resp, err := c.api.UpdateViewWithBodyWithResponse(ctx, id, "application/json", bytes.NewReader(body), httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: update view: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream,
		http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict)
}

// DeleteView relays DELETE /views/{id}.
func (c *Client) DeleteView(ctx context.Context, bearer string, id uuid.UUID) (Result, error) {
	resp, err := c.api.DeleteViewWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: delete view: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusNoContent, http.StatusNotFound)
}

// GetDashboard relays GET /dashboard with the already-converted
// filter params (cached by the collection service, never here).
func (c *Client) GetDashboard(ctx context.Context, bearer string, params *collectionapi.GetDashboardParams) (Result, error) {
	resp, err := c.api.GetDashboardWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: dashboard: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK, http.StatusBadRequest)
}

// GetValueHistory relays GET /dashboard/value-history (cached by the
// collection service, never here).
func (c *Client) GetValueHistory(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.GetValueHistoryWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: value history: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK)
}

// PurgeUserData relays DELETE /user-data (account deletion leg: wipes
// the caller's entries, tags, and saved views).
func (c *Client) PurgeUserData(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.api.PurgeUserDataWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: purge user data: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusNoContent)
}

// LibrarySummary is the typed read backing the recommendations
// composition; it is not relayed to browsers.
func (c *Client) LibrarySummary(ctx context.Context, bearer string) (collectionapi.LibrarySummary, error) {
	resp, err := c.api.GetLibrarySummaryWithResponse(ctx, httpkit.BearerEditor(bearer))
	if err != nil {
		return collectionapi.LibrarySummary{}, fmt.Errorf("collectionclient: library summary: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return collectionapi.LibrarySummary{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// SharedShelf is the typed read behind the shelf-page composition and
// the feed/Explore hydration that reuses it; not relayed to browsers
// verbatim (the bff composes its own page schema around it).
func (c *Client) SharedShelf(ctx context.Context, bearer string, id uuid.UUID) (collectionapi.SharedShelf, error) {
	resp, err := c.api.GetSharedShelfWithResponse(ctx, id, httpkit.BearerEditor(bearer))
	if err != nil {
		return collectionapi.SharedShelf{}, fmt.Errorf("collectionclient: shared shelf: %w", err)
	}
	if resp.ApplicationproblemJSON404 != nil {
		return collectionapi.SharedShelf{}, ErrShelfNotFound
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return collectionapi.SharedShelf{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// SharedShelfBySlug resolves (owner, slug) to a shelf - the
// profile-shelf page's entry point before a shelf id exists.
func (c *Client) SharedShelfBySlug(ctx context.Context, bearer string, ownerID uuid.UUID, slug string) (collectionapi.SharedShelf, error) {
	params := &collectionapi.GetSharedShelfBySlugParams{OwnerId: ownerID, Slug: slug}
	resp, err := c.api.GetSharedShelfBySlugWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return collectionapi.SharedShelf{}, fmt.Errorf("collectionclient: shared shelf by slug: %w", err)
	}
	if resp.ApplicationproblemJSON404 != nil {
		return collectionapi.SharedShelf{}, ErrShelfNotFound
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return collectionapi.SharedShelf{}, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return *resp.JSON200, nil
}

// SharedShelfEntries relays GET /shared/shelves/{shelfId}/entries -
// the composed shelf page's entries tab (the caller resolves
// visibility via SharedShelf/effectiveShelf first). limit/offset pass
// straight through; collection applies its own default when nil.
func (c *Client) SharedShelfEntries(ctx context.Context, bearer string, id uuid.UUID, limit, offset *int) (Result, error) {
	params := &collectionapi.ListSharedShelfEntriesParams{Limit: limit, Offset: offset}
	resp, err := c.api.ListSharedShelfEntriesWithResponse(ctx, id, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return Result{}, fmt.Errorf("collectionclient: shared shelf entries: %w", err)
	}
	return httpkit.Relay(resp.StatusCode(), httpkit.ContentType(resp.HTTPResponse), resp.Body, ErrUpstream, http.StatusOK, http.StatusNotFound)
}

// ListSharedShelves is the typed read behind a profile page's shelf
// list and Explore-recent's page-then-gate loop: listed shelves newest
// publish first, plus the full count beyond this page. A nil or empty
// ownerIDs pages across every listed owner (Explore-recent); a
// non-empty one scopes the page to just those owners (the profile
// page) - collectionapi.ListSharedShelvesParams.OwnerIds only gets set
// in the latter case, so an unfiltered call omits owner_ids from the
// request entirely rather than sending an empty list.
func (c *Client) ListSharedShelves(ctx context.Context, bearer string, ownerIDs []uuid.UUID, limit, offset int) ([]collectionapi.SharedShelfSummary, int, error) {
	params := &collectionapi.ListSharedShelvesParams{Limit: &limit, Offset: &offset}
	if len(ownerIDs) > 0 {
		params.OwnerIds = &ownerIDs
	}
	resp, err := c.api.ListSharedShelvesWithResponse(ctx, params, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, 0, fmt.Errorf("collectionclient: list shared shelves: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, 0, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.Shelves, resp.JSON200.TotalCount, nil
}

// SharedShelvesByIDs batch-resolves shelf summaries for hydration
// (feed and Explore excerpts); ids without a resolvable (non-private)
// shelf are simply absent from the answer.
func (c *Client) SharedShelvesByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
	resp, err := c.api.GetSharedShelvesByIdsWithResponse(ctx, &collectionapi.GetSharedShelvesByIdsParams{Ids: ids}, httpkit.BearerEditor(bearer))
	if err != nil {
		return nil, fmt.Errorf("collectionclient: shared shelves by ids: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode())
	}
	return resp.JSON200.Shelves, nil
}
