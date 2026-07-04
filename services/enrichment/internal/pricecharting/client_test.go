package pricecharting

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestPCClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient("test-key")
	c.baseURL = srv.URL
	return c
}

func TestClient_SearchDecodesPennies(t *testing.T) {
	var gotURL string
	c := newTestPCClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		_, _ = w.Write([]byte(`{"status":"success","products":[
			{"id":5011,"product-name":"Chrono Trigger","console-name":"Super Nintendo","loose-price":17244,"cib-price":42995,"new-price":53000}
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
		if strings.Contains(r.URL.RawQuery, "id=404404") {
			_, _ = w.Write([]byte(`{"status":"error","error-message":"no product found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","id":5011,"product-name":"Chrono Trigger","console-name":"Super Nintendo","loose-price":17244}`))
	})
	p, err := c.Product(context.Background(), 5011)
	if err != nil || p.ID != 5011 || *p.LoosePriceCents != 17244 {
		t.Fatalf("product: %+v, %v", p, err)
	}
	if _, err := c.Product(context.Background(), 404404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestClient_SearchErrorEnvelope(t *testing.T) {
	c := newTestPCClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","error-message":"invalid token"}`))
	})
	if _, err := c.Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("want envelope error, got %v", err)
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
