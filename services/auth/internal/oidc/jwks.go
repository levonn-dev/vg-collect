package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

type rsaJWKSDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// rsaKeyCache caches a provider's RSA verification keys by kid,
// refetching at most once per minRefetch window when a kid is unknown
// (covers provider key rotation without hammering the provider on
// garbage). Pass 0 to always refetch on unknown kid.
type rsaKeyCache struct {
	hc         *http.Client
	minRefetch time.Duration

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	lastFetch time.Time
}

func newRSAKeyCache(hc *http.Client, minRefetch time.Duration) *rsaKeyCache {
	return &rsaKeyCache{hc: hc, minRefetch: minRefetch, keys: map[string]*rsa.PublicKey{}}
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

func (c *rsaKeyCache) fetchLocked(ctx context.Context, jwksURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("oidc: fetch provider jwks: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: provider jwks status %d", resp.StatusCode)
	}
	var doc rsaJWKSDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("oidc: decode provider jwks: %w", err)
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
	// A healthy provider JWKS is never empty. A 200 carrying no usable
	// keys (transient provider degradation, e.g. mid-rotation) must not
	// evict the keys we already trust, or in-flight logins fail until the
	// next refetch. Keep the existing cache; the throttle still applies.
	if len(keys) == 0 {
		return nil
	}
	c.keys = keys
	return nil
}
