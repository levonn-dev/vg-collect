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
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
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

func valueHistoryKey(sub string) string { return "dashboard:value_history:v1:" + sub }

// GetDashboard returns the cached dashboard body for sub, or nil.
func (c *Cache) GetDashboard(ctx context.Context, sub string) ([]byte, error) {
	return valkeykit.GetBytes(ctx, c.rdb, dashboardKey(sub), "cache: get dashboard")
}

// PutDashboard caches a composed dashboard body.
func (c *Cache) PutDashboard(ctx context.Context, sub string, body []byte, ttl time.Duration) error {
	return valkeykit.PutBytes(ctx, c.rdb, dashboardKey(sub), body, ttl, "cache: put dashboard")
}

// GetValueHistory returns the cached value-history body for sub, or nil.
func (c *Cache) GetValueHistory(ctx context.Context, sub string) ([]byte, error) {
	return valkeykit.GetBytes(ctx, c.rdb, valueHistoryKey(sub), "cache: get value history")
}

// PutValueHistory caches a composed value-history body.
func (c *Cache) PutValueHistory(ctx context.Context, sub string, body []byte, ttl time.Duration) error {
	return valkeykit.PutBytes(ctx, c.rdb, valueHistoryKey(sub), body, ttl, "cache: put value history")
}

// InvalidateDashboard drops the user's dashboard-derived entries (the
// dashboard body and the value-history body): their own mutations must
// be visible immediately, not after the TTL.
func (c *Cache) InvalidateDashboard(ctx context.Context, sub string) error {
	if err := c.rdb.Del(ctx, dashboardKey(sub), valueHistoryKey(sub)).Err(); err != nil {
		return fmt.Errorf("cache: invalidate dashboard: %w", err)
	}
	return nil
}
