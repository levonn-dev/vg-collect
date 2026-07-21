package oidc_test

// Direct tests for the vg.auth.provider.request.duration histogram:
// every relying-party round trip (discovery, token-endpoint POST,
// provider JWKS fetch) records exactly once with bounded provider, op,
// and outcome labels, and cached paths record nothing.

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// installMeterReader routes instruments created during this test into a
// manual reader (the RP creates its histogram at construction, so the
// reader must be installed before newRP); cleanup swaps in a fresh
// readerless provider so tests stay isolated.
func installMeterReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(sdkmetric.NewMeterProvider()) })
	return reader
}

// histCounts collects the histogram and flattens its data points into
// "provider op outcome" -> count.
func histCounts(t *testing.T, reader *sdkmetric.ManualReader) map[string]uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]uint64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "vg.auth.provider.request.duration" {
				continue
			}
			if m.Unit != "s" {
				t.Fatalf("unit = %q, want s", m.Unit)
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("data = %T, want Histogram[float64]", m.Data)
			}
			for _, dp := range h.DataPoints {
				var parts [3]string
				for i, key := range []attribute.Key{"provider", "op", "outcome"} {
					v, ok := dp.Attributes.Value(key)
					if !ok {
						t.Fatalf("data point missing %s: %v", key, dp.Attributes.Encoded(attribute.DefaultEncoder()))
					}
					parts[i] = v.AsString()
				}
				out[fmt.Sprintf("%s %s %s", parts[0], parts[1], parts[2])] += dp.Count
			}
		}
	}
	return out
}

func TestProviderRequestHistogram_RecordsEveryHop(t *testing.T) {
	reader := installMeterReader(t)
	f := newFakeIDP(t)
	p := newRP(t, f, nil)
	f.registerCode("c1", "client-1", "n", jwt.MapClaims{"sub": "s"})

	if _, err := p.Exchange(context.Background(), "c1", "v", "n"); err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{
		"fake discovery ok":      1,
		"fake token_exchange ok": 1,
		"fake jwks ok":           1,
	}
	if got := histCounts(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}

	// A second exchange rides the cached discovery and the cached
	// provider key: only the token-endpoint POST records again.
	f.registerCode("c2", "client-1", "n", jwt.MapClaims{"sub": "s"})
	if _, err := p.Exchange(context.Background(), "c2", "v", "n"); err != nil {
		t.Fatal(err)
	}
	want["fake token_exchange ok"] = 2
	if got := histCounts(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts after cached second exchange = %v, want %v", got, want)
	}
}

func TestProviderRequestHistogram_DiscoveryError(t *testing.T) {
	reader := installMeterReader(t)
	f := newFakeIDP(t)
	f.discoveryStatus = http.StatusInternalServerError
	p := newRP(t, f, nil)

	if _, err := p.AuthorizeURL(context.Background(), "s", "n", "c"); err == nil {
		t.Fatal("want discovery error")
	}
	want := map[string]uint64{"fake discovery error": 1}
	if got := histCounts(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
}

func TestProviderRequestHistogram_TokenEndpointError(t *testing.T) {
	reader := installMeterReader(t)
	f := newFakeIDP(t)
	f.tokenStatus = http.StatusTooManyRequests
	p := newRP(t, f, nil)

	if _, err := p.Exchange(context.Background(), "c", "v", "n"); err == nil {
		t.Fatal("want token exchange error")
	}
	want := map[string]uint64{
		"fake discovery ok":         1,
		"fake token_exchange error": 1,
	}
	if got := histCounts(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
}

func TestProviderRequestHistogram_JWKSError(t *testing.T) {
	reader := installMeterReader(t)
	f := newFakeIDP(t)
	p := newRPRefetch(t, f, 0) // throttle disabled so the refetch runs
	f.registerCode("c1", "client-1", "n", jwt.MapClaims{"sub": "s"})

	// Prime discovery and the key cache, then break only the JWKS
	// endpoint and present a token with an unknown kid so verification
	// forces a refetch.
	if _, err := p.Exchange(context.Background(), "c1", "v", "n"); err != nil {
		t.Fatal(err)
	}
	f.jwksStatus = http.StatusInternalServerError
	bad := f.mintWithKid(jwt.MapClaims{
		"iss": f.issuer(), "aud": "client-1", "nonce": "n",
		"exp": time.Now().Add(time.Hour).Unix(), "sub": "s",
	}, "unknown-kid")
	f.tokenRawBody = `{"id_token":"` + bad + `"}`
	if _, err := p.Exchange(context.Background(), "c2", "v", "n"); err == nil {
		t.Fatal("verification against a dead JWKS must fail")
	}

	want := map[string]uint64{
		"fake discovery ok":      1,
		"fake token_exchange ok": 2,
		"fake jwks ok":           1,
		"fake jwks error":        1,
	}
	if got := histCounts(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
}
