package oidc_test

// Tests for the vg.auth.provider.request.duration histogram: each round trip
// records once with provider/op/outcome labels; cached paths record nothing.

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/libs/go/metrictest"
)

// histCounts flattens histogram data points into "provider op outcome" -> count.
func histCounts(t *testing.T, reader *sdkmetric.ManualReader) map[string]uint64 {
	t.Helper()
	out := map[string]uint64{}
	m, ok := metrictest.ByName(metrictest.Collect(t, reader), "vg.auth.provider.request.duration")
	if !ok {
		return out
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
	return out
}

func TestProviderRequestHistogram_RecordsEveryHop(t *testing.T) {
	reader := metrictest.Install(t)
	f := newStubIDP(t)
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

	// Second exchange rides cached discovery and key: only the token-endpoint POST records again.
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
	reader := metrictest.Install(t)
	f := newStubIDP(t)
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
	reader := metrictest.Install(t)
	f := newStubIDP(t)
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
	reader := metrictest.Install(t)
	f := newStubIDP(t)
	p := newRPRefetch(t, f, 0) // throttle disabled so the refetch runs
	f.registerCode("c1", "client-1", "n", jwt.MapClaims{"sub": "s"})

	// Primes discovery/key cache, then breaks JWKS and uses an unknown kid to force a refetch.
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
