package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
)

type rsaJWKSDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// rsaKeyCache caches a provider's RSA keys by kid, refetching at most once per
// minRefetch on an unknown kid; 0 means always refetch.
type rsaKeyCache struct {
	hc         *http.Client
	minRefetch time.Duration
	hist       metric.Float64Histogram // shared provider round-trip histogram (nil ok)
	provider   string

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	lastFetch time.Time
}

func newRSAKeyCache(hc *http.Client, minRefetch time.Duration, hist metric.Float64Histogram, provider string) *rsaKeyCache {
	return &rsaKeyCache{hc: hc, minRefetch: minRefetch, hist: hist, provider: provider,
		keys: map[string]*rsa.PublicKey{}}
}

func (c *rsaKeyCache) get(ctx context.Context, jwksURL, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	if time.Since(c.lastFetch) < c.minRefetch {
		return nil, fmt.Errorf("oidc: unknown provider kid %q", kid)
	}
	if err := c.fetchLocked(ctx, jwksURL); err != nil {
		return nil, err
	}
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("oidc: unknown provider kid %q after refetch", kid)
}

// fetchLocked measures the provider JWKS round trip (op=jwks) around the actual fetch.
func (c *rsaKeyCache) fetchLocked(ctx context.Context, jwksURL string) error {
	start := time.Now()
	err := c.refreshLocked(ctx, jwksURL)
	recordProviderRequest(ctx, c.hist, c.provider, opJWKS, start, err != nil)
	return err
}

func (c *rsaKeyCache) refreshLocked(ctx context.Context, jwksURL string) error {
	// Same fetch-decode skeleton as fetchDiscovery/redeemCode: OauthCallback's errors.As
	// must see the same *ProviderError type, or a JWKS outage misclassifies as a rejected login.
	var doc rsaJWKSDoc
	if err := doJSON(ctx, c.hc, http.MethodGet, jwksURL, nil, nil, "jwks", &doc); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: int(new(big.Int).SetBytes(eb).Int64()),
		}
	}
	c.lastFetch = time.Now()
	// A 200 with no usable keys (mid-rotation provider hiccup) must not evict
	// cached keys, or in-flight logins fail until the next refetch; the throttle still applies.
	if len(keys) == 0 {
		return nil
	}
	c.keys = keys
	return nil
}
