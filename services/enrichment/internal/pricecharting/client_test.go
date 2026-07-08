package pricecharting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func newTestPCClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient("test-key")
	c.baseURL = srv.URL
	// The production budget is 1 call per second (pinned by its own
	// test); paying it here would cost a wall-clock second per call.
	c.limiter = rate.NewLimiter(rate.Inf, 1)
	return c
}

func TestNewClient_RespectsDocumentedRateLimit(t *testing.T) {
	c := NewClient("k")
	if c.limiter.Limit() != 1 || c.limiter.Burst() != 1 {
		t.Fatalf("documented budget is 1 call per second, got limit %v burst %d", c.limiter.Limit(), c.limiter.Burst())
	}
}

func TestClient_SearchDecodesPennies(t *testing.T) {
	var gotURL string
	c := newTestPCClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		// The live API serializes ids as JSON strings.
		_, _ = w.Write([]byte(`{"status":"success","products":[
			{"id":"5011","product-name":"Chrono Trigger","console-name":"Super Nintendo","loose-price":17244,"cib-price":42995,"new-price":53000}
		]}`))
	})
	got, err := c.Search(context.Background(), "chrono trigger")
	if err != nil || len(got) != 1 {
		t.Fatalf("search: %+v, %v", got, err)
	}
	p := got[0]
	if p.ID != 5011 || p.Name != "Chrono Trigger" || p.ConsoleName != "Super Nintendo" {
		t.Fatalf("fields: %+v", p)
	}
	if p.LoosePriceCents == nil || *p.LoosePriceCents != 17244 || *p.CIBPriceCents != 42995 || *p.NewPriceCents != 53000 {
		t.Fatalf("pennies decode: %+v", p)
	}
	if !strings.Contains(gotURL, "/api/products?") || !strings.Contains(gotURL, "t=test-key") || !strings.Contains(gotURL, "q=chrono+trigger") {
		t.Fatalf("bad request url: %s", gotURL)
	}
}

func TestClient_ProductNotFoundAndErrors(t *testing.T) {
	c := newTestPCClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Envelope wordings, HTTP statuses, and the string id mirror
		// the live API: an unknown id answers 404 with the envelope in
		// the body. The 200-status variant guards against the provider
		// drifting back to the envelope-only signaling its docs imply.
		switch {
		case strings.Contains(r.URL.RawQuery, "id=404404"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","error-message":"No such product"}`))
		case strings.Contains(r.URL.RawQuery, "id=200200"):
			_, _ = w.Write([]byte(`{"status":"error","error-message":"No such product"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"success","id":"5011","product-name":"Chrono Trigger","console-name":"Super Nintendo","loose-price":17244}`))
		}
	})
	p, err := c.Product(context.Background(), 5011)
	if err != nil || p.ID != 5011 || *p.LoosePriceCents != 17244 {
		t.Fatalf("product: %+v, %v", p, err)
	}
	if _, err := c.Product(context.Background(), 404404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for the 404 envelope, got %v", err)
	}
	if _, err := c.Product(context.Background(), 200200); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for the 200 envelope, got %v", err)
	}
}

func TestClient_ProductCredentialErrorIsNotNotFound(t *testing.T) {
	c := newTestPCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":"error","error-message":"Unknown access token"}`))
	})
	_, err := c.Product(context.Background(), 5011)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("credential failure must not read as a missing product: %v", err)
	}
	if !strings.Contains(err.Error(), "Unknown access token") {
		t.Fatalf("error must carry the envelope message, got %v", err)
	}
}

func TestClient_SearchZeroHitsIsEmptySuccess(t *testing.T) {
	c := newTestPCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"products":[],"status":"success"}`))
	})
	got, err := c.Search(context.Background(), "zzzz")
	if err != nil || len(got) != 0 {
		t.Fatalf("zero hits must be an empty success: %+v, %v", got, err)
	}
}

func TestProduct_IDDecodesStringAndNumber(t *testing.T) {
	var s Product
	if err := json.Unmarshal([]byte(`{"id":"6889","product-name":"X"}`), &s); err != nil || s.ID != 6889 || s.Name != "X" {
		t.Fatalf("string id: %+v, %v", s, err)
	}
	var n Product
	if err := json.Unmarshal([]byte(`{"id":7,"product-name":"Y"}`), &n); err != nil || n.ID != 7 {
		t.Fatalf("numeric id: %+v, %v", n, err)
	}
	var bad Product
	if err := json.Unmarshal([]byte(`{"id":"abc"}`), &bad); err == nil || !strings.Contains(err.Error(), "product id") {
		t.Fatalf("want id parse error, got %v", err)
	}
}

func TestClient_SearchErrorEnvelope(t *testing.T) {
	c := newTestPCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":"error","error-message":"invalid token"}`))
	})
	if _, err := c.Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("want the envelope message carried, got %v", err)
	}
}

func TestClient_NonJSONErrorFallsBackToStatus(t *testing.T) {
	c := newTestPCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<html>boom</html>`))
	})
	if _, err := c.Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("want the HTTP status fallback, got %v", err)
	}
	if _, err := c.Product(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "status 500") || errors.Is(err, ErrNotFound) {
		t.Fatalf("want a non-NotFound status fallback, got %v", err)
	}
}

func TestClient_TimeoutAndMalformed(t *testing.T) {
	slow := newTestPCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"status":"success","products":[]}`))
	})
	slow.httpc.Timeout = 30 * time.Millisecond
	if _, err := slow.Search(context.Background(), "x"); err == nil {
		t.Fatal("want timeout error")
	}

	bad := newTestPCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":`))
	})
	if _, err := bad.Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("want decode error, got %v", err)
	}
}
