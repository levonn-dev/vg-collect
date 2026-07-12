package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const defaultBaseURL = "https://api.frankfurter.dev"

// refreshAfter is how long a snapshot is served before the next call
// triggers an upstream refresh. The upstream publishes ECB reference
// rates once per business day, so an hour is generous.
const refreshAfter = time.Hour

// Client is the real frankfurter.dev client (no credentials). One
// snapshot is cached in memory: fresher than refreshAfter is served
// as-is, older triggers a refetch, and a failed refetch keeps serving
// the stale snapshot (hours-old rates beat no rates). Only a cold
// cache surfaces the upstream error.
type Client struct {
	httpc   *http.Client
	baseURL string

	mu        sync.Mutex
	snapshot  Rates
	fetchedAt time.Time
	// now is a test seam.
	now func() time.Time
}

// NewClient builds a Client against the public frankfurter.dev API.
func NewClient() *Client {
	return &Client{
		httpc: &http.Client{
			Timeout:   10 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		baseURL: defaultBaseURL,
		now:     time.Now,
	}
}

// Latest returns the cached snapshot, refreshing it when older than
// refreshAfter.
func (c *Client) Latest(ctx context.Context) (Rates, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fetchedAt.IsZero() && c.now().Sub(c.fetchedAt) < refreshAfter {
		return c.snapshot, nil
	}
	r, err := c.fetch(ctx)
	if err != nil {
		if !c.fetchedAt.IsZero() {
			return c.snapshot, nil
		}
		return Rates{}, err
	}
	c.snapshot = r
	c.fetchedAt = c.now()
	return c.snapshot, nil
}

func (c *Client) fetch(ctx context.Context) (Rates, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/latest?base=USD", nil)
	if err != nil {
		return Rates{}, fmt.Errorf("fx: request: %w", err)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return Rates{}, fmt.Errorf("fx: latest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Rates{}, fmt.Errorf("fx: latest: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Rates{}, fmt.Errorf("fx: latest: read: %w", err)
	}
	var r Rates
	if err := json.Unmarshal(body, &r); err != nil {
		return Rates{}, fmt.Errorf("fx: latest: decode: %w", err)
	}
	if r.Base != "USD" || len(r.Rates) == 0 {
		return Rates{}, fmt.Errorf("fx: latest: implausible snapshot (base %q, %d rates)", r.Base, len(r.Rates))
	}
	return r, nil
}
