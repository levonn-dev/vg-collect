// Package jwtauth validates vgkeep access JWTs (Ed25519, kid-aware
// JWKS) and provides auth/role middleware. It never mints tokens;
// that is the auth service's job.
package jwtauth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type jwksDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		Kid string `json:"kid"`
		X   string `json:"x"`
	} `json:"keys"`
}

type keyCache struct {
	url        string
	hc         *http.Client
	minRefetch time.Duration

	mu        sync.Mutex
	keys      map[string]ed25519.PublicKey
	lastFetch time.Time
}

func newKeyCache(url string, minRefetch time.Duration) *keyCache {
	return &keyCache{
		url:        url,
		hc:         &http.Client{Timeout: 5 * time.Second},
		minRefetch: minRefetch,
		keys:       map[string]ed25519.PublicKey{},
	}
}

// get returns the key for kid, refetching the JWKS at most once per
// minRefetch window when the kid is unknown (covers key rotation
// without letting a flood of bad kids hammer the auth service).
// The fetch deliberately happens under the mutex: concurrent
// validations serialize behind one upstream call (anti-stampede);
// at our scale that head-of-line blocking is an accepted trade.
func (c *keyCache) get(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	if time.Since(c.lastFetch) < c.minRefetch {
		return nil, fmt.Errorf("jwtauth: unknown kid %q", kid)
	}
	if err := c.fetchLocked(ctx); err != nil {
		return nil, err
	}
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("jwtauth: unknown kid %q after refetch", kid)
}

func (c *keyCache) fetchLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("jwtauth: fetch jwks: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwtauth: jwks status %d", resp.StatusCode)
	}
	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("jwtauth: decode jwks: %w", err)
	}
	keys := map[string]ed25519.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(raw)
	}
	c.keys = keys
	c.lastFetch = time.Now()
	return nil
}
