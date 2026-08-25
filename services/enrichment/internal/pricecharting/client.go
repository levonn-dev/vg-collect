package pricecharting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://www.pricecharting.com"

// Client is the real keyed PriceCharting client, limited to the
// documented 1 call/s budget (past it, calls block and the account
// risks revocation). Bulk needs go through the provider's CSV download instead.
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
		limiter: rate.NewLimiter(1, 1),
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
	}
}

// get performs one keyed GET and returns the raw body with the HTTP
// status. Error answers carry the JSON envelope even on 400/500-range
// statuses, so a non-200 body is still worth decoding.
func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, int, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, 0, fmt.Errorf("pricecharting: limiter: %w", err)
	}
	params.Set("t", c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("pricecharting: request: %w", err)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("pricecharting: %s: %w", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("pricecharting: %s: read: %w", path, err)
	}
	return body, resp.StatusCode, nil
}

// Search queries /api/products (multiple results, prices included);
// a zero-hit query is success with an empty array, not an error envelope.
func (c *Client) Search(ctx context.Context, q string) ([]Product, error) {
	raw, httpStatus, err := c.get(ctx, "/api/products", url.Values{"q": {q}})
	if err != nil {
		return nil, err
	}
	var body struct {
		Status       string    `json:"status"`
		ErrorMessage string    `json:"error-message"`
		Products     []Product `json:"products"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		if httpStatus != http.StatusOK {
			return nil, fmt.Errorf("pricecharting: /api/products: status %d", httpStatus)
		}
		return nil, fmt.Errorf("pricecharting: /api/products: decode: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("pricecharting: search: %s", body.ErrorMessage)
	}
	return body.Products, nil
}

// notFoundMessage is the live API's envelope wording for an unknown
// id; any other message is a provider error, not a missing product.
const notFoundMessage = "no such product"

// Product fetches one product by id. Only the unknown-id wording maps
// to ErrNotFound, so a credential outage surfaces as an error, not a phantom 404.
func (c *Client) Product(ctx context.Context, id int64) (Product, error) {
	raw, httpStatus, err := c.get(ctx, "/api/product", url.Values{"id": {strconv.FormatInt(id, 10)}})
	if err != nil {
		return Product{}, err
	}
	// Two passes over the same bytes: product fields sit beside status
	// in one flat object; embedding Product would shadow the envelope's UnmarshalJSON.
	var env struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error-message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		if httpStatus != http.StatusOK {
			return Product{}, fmt.Errorf("pricecharting: /api/product: status %d", httpStatus)
		}
		return Product{}, fmt.Errorf("pricecharting: /api/product: decode: %w", err)
	}
	if env.Status == "error" {
		if strings.EqualFold(env.ErrorMessage, notFoundMessage) {
			return Product{}, ErrNotFound
		}
		return Product{}, fmt.Errorf("pricecharting: product: %s", env.ErrorMessage)
	}
	if env.Status != "success" {
		return Product{}, fmt.Errorf("pricecharting: /api/product: status %d, envelope status %q", httpStatus, env.Status)
	}
	var p Product
	if err := json.Unmarshal(raw, &p); err != nil {
		return Product{}, fmt.Errorf("pricecharting: /api/product: decode: %w", err)
	}
	return p, nil
}
