// Package cache is the enrichment service's Valkey surface: the 24h
// search query cache and the short product read cache. Values are
// marshaled response bodies, so a hit costs no recompute. FAIL-OPEN
// DECISIONS BELONG TO CALLERS: errors are returned verbatim and every
// handler treats them as a miss (log + continue). Misses are nil, not
// errors. No per-operation timeouts here (network-level timeouts
// belong to the client options).
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache wraps the service's Valkey client.
type Cache struct {
	rdb *redis.Client
}

// New builds a Cache over an already-connected client.
func New(rdb *redis.Client) *Cache {
	return &Cache{rdb: rdb}
}

// searchKey hashes the (caller-normalized) query so keys stay clean
// regardless of query content or length.
func searchKey(kind, q string) string {
	sum := sha256.Sum256([]byte(q))
	return "search:v1:" + kind + ":" + hex.EncodeToString(sum[:])
}

func productKey(id string) string { return "product:v1:" + id }

func (c *Cache) get(ctx context.Context, key, op string) ([]byte, error) {
	v, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: %s: %w", op, err)
	}
	// Copied out of the client's reply string so callers own the bytes.
	return []byte(v), nil
}

// GetSearch returns the cached response body for a query, or nil.
func (c *Cache) GetSearch(ctx context.Context, kind, q string) ([]byte, error) {
	return c.get(ctx, searchKey(kind, q), "get search")
}

// PutSearch caches a search response body.
func (c *Cache) PutSearch(ctx context.Context, kind, q string, body []byte, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, searchKey(kind, q), body, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put search: %w", err)
	}
	return nil
}

// GetProduct returns the cached product body, or nil.
func (c *Cache) GetProduct(ctx context.Context, id string) ([]byte, error) {
	return c.get(ctx, productKey(id), "get product")
}

// PutProduct caches a product body.
func (c *Cache) PutProduct(ctx context.Context, id string, body []byte, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, productKey(id), body, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put product: %w", err)
	}
	return nil
}

// InvalidateProduct drops a product's cache entry (admin mapping
// corrections must be visible immediately, not after the TTL).
func (c *Cache) InvalidateProduct(ctx context.Context, id string) error {
	if err := c.rdb.Del(ctx, productKey(id)).Err(); err != nil {
		return fmt.Errorf("cache: invalidate product: %w", err)
	}
	return nil
}

// platformsKey is the single wholesale key for the platform catalog
// (no per-query variance, unlike the hashed search keys).
const platformsKey = "platforms:v1"

// GetPlatforms returns the cached platform-catalog body, or nil.
func (c *Cache) GetPlatforms(ctx context.Context) ([]byte, error) {
	return c.get(ctx, platformsKey, "get platforms")
}

// PutPlatforms caches the platform-catalog body.
func (c *Cache) PutPlatforms(ctx context.Context, body []byte, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, platformsKey, body, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put platforms: %w", err)
	}
	return nil
}
