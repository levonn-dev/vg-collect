// Package cache is the collection service's Valkey surface: the
// composed dashboard body, cached briefly and invalidated by the
// owner's own mutations. Values are marshaled response bodies, so a
// hit costs no recompute. FAIL-OPEN DECISIONS BELONG TO CALLERS:
// errors are returned verbatim and the handler treats them as a miss
// (log + continue). Misses are nil, not errors. No per-operation
// timeouts here (network-level timeouts belong to the client options).
package cache

import (
	"context"
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

func dashboardKey(sub string) string { return "dashboard:v1:" + sub }

// GetDashboard returns the cached dashboard body for sub, or nil.
func (c *Cache) GetDashboard(ctx context.Context, sub string) ([]byte, error) {
	v, err := c.rdb.Get(ctx, dashboardKey(sub)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: get dashboard: %w", err)
	}
	// Copied out of the client's reply string so callers own the bytes.
	return []byte(v), nil
}

// PutDashboard caches a composed dashboard body.
func (c *Cache) PutDashboard(ctx context.Context, sub string, body []byte, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, dashboardKey(sub), body, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put dashboard: %w", err)
	}
	return nil
}

// InvalidateDashboard drops the user's dashboard entry (their own
// mutations must be visible immediately, not after the TTL).
func (c *Cache) InvalidateDashboard(ctx context.Context, sub string) error {
	if err := c.rdb.Del(ctx, dashboardKey(sub)).Err(); err != nil {
		return fmt.Errorf("cache: invalidate dashboard: %w", err)
	}
	return nil
}
