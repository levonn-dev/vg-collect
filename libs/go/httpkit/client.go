package httpkit

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// NewHTTPClient returns the outbound client every internal service-to-service client dials
// with: otelhttp-instrumented, and a 10-second timeout so a wedged upstream cannot hang the caller.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
}

// BearerEditor returns a request editor forwarding bearer as the Authorization header. The
// return type is deliberately the bare func signature, not a named type: every oapi-codegen
// client declares its own named RequestEditorFn over it, and only an unnamed type is
// assignable to all of them without a conversion.
func BearerEditor(bearer string) func(context.Context, *http.Request) error {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+bearer)
		return nil
	}
}
