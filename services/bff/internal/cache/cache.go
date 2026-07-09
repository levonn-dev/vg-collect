// Package cache is the bff's Valkey surface: the jti denylist, the
// refresh singleflight (lock + published result), and the /api/me
// composition cache. Every method returns errors verbatim; FAIL-OPEN
// DECISIONS BELONG TO CALLERS (the denylist and caches harden or speed
// up an already-correct system, they are not its primary controls).
// Contexts are caller-supplied and carry their own deadlines; this
// package imposes no per-operation timeouts (network-level timeouts
// belong to the client options).
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
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

// DenylistAdd marks access-token jtis revoked for ttl (the access-token
// TTL plus leeway: after that the tokens are dead by expiry anyway).
func (c *Cache) DenylistAdd(ctx context.Context, jtis []string, ttl time.Duration) error {
	if len(jtis) == 0 {
		return nil
	}
	pipe := c.rdb.Pipeline()
	for _, jti := range jtis {
		pipe.Set(ctx, "denylist:"+jti, "1", ttl)
	}
	// Exec reports the first failed command; partial failures are
	// treated as total failures by callers under the fail-open contract.
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

// AcquireRefreshLock takes the per-session rotation lock. holder is a
// random value identifying this acquisition for the release check.
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

// PutRefreshResult publishes the sealed cookie produced by a rotation
// so concurrent requests holding the consumed token can adopt it. The
// value is AES-GCM ciphertext (exactly what goes to the browser), so
// nothing secret rests in Valkey in the clear.
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

// GetMe returns the cached /api/me body for sub, or nil when absent.
// Copied out of the client's reply string so callers own the bytes.
func (c *Cache) GetMe(ctx context.Context, sub string) ([]byte, error) {
	v, err := c.rdb.Get(ctx, "me:"+sub).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: get me: %w", err)
	}
	return []byte(v), nil
}

// PutMe caches a marshaled /api/me body.
func (c *Cache) PutMe(ctx context.Context, sub string, body []byte, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, "me:"+sub, body, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put me: %w", err)
	}
	return nil
}

// InvalidateMe drops a user's cached /api/me after a profile edit so
// the header reflects the change immediately, not at TTL expiry.
func (c *Cache) InvalidateMe(ctx context.Context, sub string) error {
	if err := c.rdb.Del(ctx, "me:"+sub).Err(); err != nil {
		return fmt.Errorf("cache: invalidate me: %w", err)
	}
	return nil
}

// GetRecs returns the cached recommendations body for sub, or nil.
func (c *Cache) GetRecs(ctx context.Context, sub string) ([]byte, error) {
	v, err := c.rdb.Get(ctx, "recs:"+sub).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: get recs: %w", err)
	}
	return []byte(v), nil
}

// PutRecs caches a composed recommendations body.
func (c *Cache) PutRecs(ctx context.Context, sub string, body []byte, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, "recs:"+sub, body, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put recs: %w", err)
	}
	return nil
}

// InvalidateRecs drops a user's recommendations after one of their
// entry mutations (the library that feeds scoring changed).
func (c *Cache) InvalidateRecs(ctx context.Context, sub string) error {
	if err := c.rdb.Del(ctx, "recs:"+sub).Err(); err != nil {
		return fmt.Errorf("cache: invalidate recs: %w", err)
	}
	return nil
}
