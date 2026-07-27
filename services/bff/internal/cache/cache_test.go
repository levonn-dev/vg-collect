package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"

	"github.com/levonn-dev/vg-collect/libs/go/valkeykit"
	"github.com/levonn-dev/vg-collect/services/bff/internal/cache"
)

func newTestCache(t *testing.T) *cache.Cache {
	t.Helper()
	c, _ := newTestCacheWithClient(t)
	return c
}

// newTestCacheWithClient is newTestCache but also hands back the raw
// client, for tests that need to reach around the Cache API (e.g. to
// seed or inspect a key directly).
func newTestCacheWithClient(t *testing.T) (*cache.Cache, *redis.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })
	url, err := vk.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := valkeykit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return cache.New(client), client
}

func TestDenylist(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	hit, err := c.DenylistHas(ctx, "jti-1")
	if err != nil || hit {
		t.Fatalf("empty denylist: hit=%v err=%v", hit, err)
	}
	if err := c.DenylistAdd(ctx, []string{"jti-1", "jti-2"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, jti := range []string{"jti-1", "jti-2"} {
		if hit, _ := c.DenylistHas(ctx, jti); !hit {
			t.Fatalf("%s should be denylisted", jti)
		}
	}
	if err := c.DenylistAdd(ctx, nil, time.Minute); err != nil {
		t.Fatal("empty add must be a no-op, not an error")
	}
}

func TestRefreshLock(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	ok, err := c.AcquireRefreshLock(ctx, "fam", "holder-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	ok, _ = c.AcquireRefreshLock(ctx, "fam", "holder-b", time.Minute)
	if ok {
		t.Fatal("second acquire should fail while held")
	}
	// Wrong holder cannot release.
	if err := c.ReleaseRefreshLock(ctx, "fam", "holder-b"); err != nil {
		t.Fatal(err)
	}
	ok, _ = c.AcquireRefreshLock(ctx, "fam", "holder-c", time.Minute)
	if ok {
		t.Fatal("lock should survive a non-holder release")
	}
	// Right holder releases.
	if err := c.ReleaseRefreshLock(ctx, "fam", "holder-a"); err != nil {
		t.Fatal(err)
	}
	ok, _ = c.AcquireRefreshLock(ctx, "fam", "holder-c", time.Minute)
	if !ok {
		t.Fatal("lock should be free after the holder released")
	}
}

func TestRefreshResult(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	got, err := c.GetRefreshResult(ctx, "fam")
	if err != nil || got != "" {
		t.Fatalf("absent result: got=%q err=%v", got, err)
	}
	if err := c.PutRefreshResult(ctx, "fam", "sealed-cookie", time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err = c.GetRefreshResult(ctx, "fam")
	if err != nil || got != "sealed-cookie" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestMeCache(t *testing.T) {
	c, client := newTestCacheWithClient(t)
	ctx := context.Background()

	body, err := c.GetMe(ctx, "user-1")
	if err != nil || body != nil {
		t.Fatalf("absent me: body=%q err=%v", body, err)
	}
	if err := c.PutMe(ctx, "user-1", []byte(`{"id":"user-1"}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	body, err = c.GetMe(ctx, "user-1")
	if err != nil || string(body) != `{"id":"user-1"}` {
		t.Fatalf("body=%q err=%v", body, err)
	}

	// The round-trip key is versioned: a deploy that changes the Me
	// projection bumps the version tag, so a pre-deploy body never
	// leaks into a post-deploy read.
	versioned, err := client.Get(ctx, "me:v4:user-1").Result()
	if err != nil || versioned != `{"id":"user-1"}` {
		t.Fatalf("PutMe must write the versioned key me:v4:<sub>: got=%q err=%v", versioned, err)
	}

	// A body left over from before versioning (the unversioned "me:"
	// key) must not be served: it is the old, unversioned shape.
	if err := client.Set(ctx, "me:user-2", []byte(`{"legacy":true}`), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	legacy, err := c.GetMe(ctx, "user-2")
	if err != nil || legacy != nil {
		t.Fatalf("a legacy unversioned key must miss: body=%q err=%v", legacy, err)
	}

	if err := c.InvalidateMe(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
	body, err = c.GetMe(ctx, "user-1")
	if err != nil || body != nil {
		t.Fatalf("invalidated me should be gone: body=%q err=%v", body, err)
	}
}

func TestRecsCache(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	body, err := c.GetRecs(ctx, "user-1")
	if err != nil || body != nil {
		t.Fatalf("absent recs: body=%q err=%v", body, err)
	}
	if err := c.PutRecs(ctx, "user-1", []byte(`{"degraded":false,"recommendations":[]}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	body, err = c.GetRecs(ctx, "user-1")
	if err != nil || string(body) != `{"degraded":false,"recommendations":[]}` {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if err := c.InvalidateRecs(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
	body, err = c.GetRecs(ctx, "user-1")
	if err != nil || body != nil {
		t.Fatalf("invalidated recs should be gone: body=%q err=%v", body, err)
	}
}

func TestDenylistEntriesExpire(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	if err := c.DenylistAdd(ctx, []string{"jti-ttl"}, 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if hit, _ := c.DenylistHas(ctx, "jti-ttl"); !hit {
		t.Fatal("entry should exist before its TTL")
	}
	time.Sleep(400 * time.Millisecond)
	hit, err := c.DenylistHas(ctx, "jti-ttl")
	if err != nil || hit {
		t.Fatalf("entry should expire: hit=%v err=%v", hit, err)
	}
}

func TestRefreshLockExpires(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	ok, err := c.AcquireRefreshLock(ctx, "fam", "holder-a", 200*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	time.Sleep(400 * time.Millisecond)
	ok, err = c.AcquireRefreshLock(ctx, "fam", "holder-b", time.Minute)
	if err != nil || !ok {
		t.Fatalf("a crashed holder's lock must expire: ok=%v err=%v", ok, err)
	}
}
