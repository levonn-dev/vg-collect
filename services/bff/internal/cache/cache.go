// Package cache is the bff's Valkey surface: the jti denylist, the
// refresh singleflight (lock + published result), and the /api/me
// composition cache. Methods return errors verbatim; fail-open is a
// caller decision. No per-operation timeouts; contexts carry their own.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
)

type Cache struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Cache {
	return &Cache{rdb: rdb}
}

// releaseScript deletes the lock only if the caller still holds it, so
// a holder that stalled past its TTL cannot delete a successor's lock.
var releaseScript = redis.NewScript(
	`if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) else return 0 end`)

// DenylistAdd marks jtis revoked for ttl (access-token TTL plus leeway).
func (c *Cache) DenylistAdd(ctx context.Context, jtis []string, ttl time.Duration) error {
	if len(jtis) == 0 {
		return nil
	}
	pipe := c.rdb.Pipeline()
	for _, jti := range jtis {
		pipe.Set(ctx, "denylist:"+jti, "1", ttl)
	}
	// Exec reports the first failed command; treated as total failure under the fail-open contract.
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("cache: denylist add: %w", err)
	}
	return nil
}

// DenylistHas reports whether a jti was revoked before its expiry.
func (c *Cache) DenylistHas(ctx context.Context, jti string) (bool, error) {
	n, err := c.rdb.Exists(ctx, "denylist:"+jti).Result()
	if err != nil {
		return false, fmt.Errorf("cache: denylist check: %w", err)
	}
	return n > 0, nil
}

// AcquireRefreshLock takes the rotation lock; holder identifies this acquisition for release.
func (c *Cache) AcquireRefreshLock(ctx context.Context, key, holder string, ttl time.Duration) (bool, error) {
	ok, err := c.rdb.SetNX(ctx, "refresh:lock:"+key, holder, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache: acquire lock: %w", err)
	}
	return ok, nil
}

// ReleaseRefreshLock releases the lock if (and only if) holder owns it.
func (c *Cache) ReleaseRefreshLock(ctx context.Context, key, holder string) error {
	if err := releaseScript.Run(ctx, c.rdb, []string{"refresh:lock:" + key}, holder).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("cache: release lock: %w", err)
	}
	return nil
}

// PutRefreshResult publishes the sealed cookie for concurrent holders of
// the consumed token; the value is AES-GCM ciphertext, never plaintext in Valkey.
func (c *Cache) PutRefreshResult(ctx context.Context, key, sealed string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, "refresh:result:"+key, sealed, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put result: %w", err)
	}
	return nil
}

// GetRefreshResult returns the published sealed cookie, or "" when none.
func (c *Cache) GetRefreshResult(ctx context.Context, key string) (string, error) {
	v, err := c.rdb.Get(ctx, "refresh:result:"+key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: get result: %w", err)
	}
	return v, nil
}

// meKeyVersion tags the /api/me cache key with the projection shape;
// bump on any Me projection change so a deploy never serves a stale shape.
const meKeyVersion = "v4"

func meKey(sub string) string { return "me:" + meKeyVersion + ":" + sub }

// GetMe returns the cached /api/me body for sub (nil if absent), copied so callers own the bytes.
func (c *Cache) GetMe(ctx context.Context, sub string) ([]byte, error) {
	return valkeykit.GetBytes(ctx, c.rdb, meKey(sub), "cache: get me")
}

// PutMe caches a marshaled /api/me body.
func (c *Cache) PutMe(ctx context.Context, sub string, body []byte, ttl time.Duration) error {
	return valkeykit.PutBytes(ctx, c.rdb, meKey(sub), body, ttl, "cache: put me")
}

// InvalidateMe drops a user's cached /api/me so a profile edit shows immediately, not at TTL expiry.
func (c *Cache) InvalidateMe(ctx context.Context, sub string) error {
	if err := c.rdb.Del(ctx, meKey(sub)).Err(); err != nil {
		return fmt.Errorf("cache: invalidate me: %w", err)
	}
	return nil
}

// recsKeyVersion tags the /api/recommendations cache key; the cached
// bytes are enrichment's raw /recommendations:score body, verbatim - bump on upstream shape changes.
const recsKeyVersion = "v1"

func recsKey(sub string) string { return "recs:" + recsKeyVersion + ":" + sub }

// GetRecs returns the cached recommendations body for sub, or nil.
func (c *Cache) GetRecs(ctx context.Context, sub string) ([]byte, error) {
	return valkeykit.GetBytes(ctx, c.rdb, recsKey(sub), "cache: get recs")
}

// PutRecs caches a composed recommendations body.
func (c *Cache) PutRecs(ctx context.Context, sub string, body []byte, ttl time.Duration) error {
	return valkeykit.PutBytes(ctx, c.rdb, recsKey(sub), body, ttl, "cache: put recs")
}

// InvalidateRecs drops recommendations when a library mutation changes scoring input.
func (c *Cache) InvalidateRecs(ctx context.Context, sub string) error {
	if err := c.rdb.Del(ctx, recsKey(sub)).Err(); err != nil {
		return fmt.Errorf("cache: invalidate recs: %w", err)
	}
	return nil
}
