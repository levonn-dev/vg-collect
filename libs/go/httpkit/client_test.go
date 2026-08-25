package httpkit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func TestNewHTTPClient_TimeoutAndTransport(t *testing.T) {
	c := httpkit.NewHTTPClient()
	if c.Timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", c.Timeout)
	}
	if c.Transport == nil {
		t.Fatal("transport is nil, want an otelhttp-wrapped transport")
	}
}

func TestNewHTTPClient_DistinctInstances(t *testing.T) {
	// Every internal client mints its own *http.Client (never a shared package-level one).
	a, b := httpkit.NewHTTPClient(), httpkit.NewHTTPClient()
	if a == b {
		t.Fatal("NewHTTPClient returned the same instance twice")
	}
}

func TestBearerEditor_SetsAuthorizationHeader(t *testing.T) {
	edit := httpkit.BearerEditor("abc123")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := edit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer abc123" {
		t.Fatalf("Authorization = %q", got)
	}
}

// namedRequestEditorFn mirrors the shape every oapi-codegen client generates as its own
// RequestEditorFn. BearerEditor's return type must stay unnamed to satisfy it without a
// conversion; this test fails to compile if that ever changes.
type namedRequestEditorFn func(ctx context.Context, req *http.Request) error

func TestBearerEditor_AssignableToGeneratedEditorType(t *testing.T) {
	var fn namedRequestEditorFn = httpkit.BearerEditor("tok")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := fn(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("Authorization = %q", got)
	}
}
