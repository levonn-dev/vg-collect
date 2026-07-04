package pricecharting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://www.pricecharting.com"

// Client is the real keyed PriceCharting client. The API publishes no
// hard rate budget; a polite 2 req/s limiter keeps the daily walk
// gentle.
type Client struct {
	httpc   *http.Client
	limiter *rate.Limiter
	apiKey  string
	baseURL string
}

// NewClient builds a Client with the account's API token.
func NewClient(apiKey string) *Client {
	return &Client{
		httpc: &http.Client{
			Timeout:   10 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		limiter: rate.NewLimiter(2, 1),
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
	}
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("pricecharting: limiter: %w", err)
	}
	params.Set("t", c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("pricecharting: request: %w", err)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("pricecharting: %s: %w", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("pricecharting: %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("pricecharting: %s: decode: %w", path, err)
	}
	return nil
}

// Search queries /api/products (multiple results, prices included).
func (c *Client) Search(ctx context.Context, q string) ([]Product, error) {
	var body struct {
		Status       string    `json:"status"`
		ErrorMessage string    `json:"error-message"`
		Products     []Product `json:"products"`
	}
	if err := c.get(ctx, "/api/products", url.Values{"q": {q}}, &body); err != nil {
		return nil, err
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("pricecharting: search: %s", body.ErrorMessage)
	}
	return body.Products, nil
}

// Product fetches one product by id. An HTTP-200 error envelope means
// the id is unknown (ErrNotFound); any non-success envelope (including
// credential failures) maps to ErrNotFound here. Callers that reach a
// product by id without a prior Search will see a credential outage as
// not-found; distinguishing the error-message envelope is deliberately
// deferred until real API keys first arrive.
func (c *Client) Product(ctx context.Context, id int64) (Product, error) {
	var body struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error-message"`
		Product
	}
	if err := c.get(ctx, "/api/product", url.Values{"id": {strconv.FormatInt(id, 10)}}, &body); err != nil {
		return Product{}, err
	}
	if body.Status != "success" {
		return Product{}, ErrNotFound
	}
	return body.Product, nil
}
