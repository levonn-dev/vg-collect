package cache_test

import (
	"context"
	"sync"
	"testing"
	"time"

	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/cache"
)

// One Valkey container serves this whole package. Each test still
// starts on an empty keyspace via the FlushAll in newTestCache. No
// Terminate: the testcontainers reaper collects the container when
// the test process exits.
var sharedVK struct {
	once sync.Once
	url  string
	err  error
}

func newTestCache(t *testing.T) *cache.Cache {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	sharedVK.once.Do(func() {
		vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
		if err != nil {
			sharedVK.err = err
			return
		}
		sharedVK.url, sharedVK.err = vk.ConnectionString(ctx)
	})
	if sharedVK.err != nil {
		t.Fatal(sharedVK.err)
	}
	client, err := valkeykit.Connect(ctx, sharedVK.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	return cache.New(client)
}

func TestSearch_RoundTripKindsAndExpiry(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	if hit, err := c.GetSearch(ctx, "game", "zelda"); err != nil || hit != nil {
		t.Fatalf("cold cache must miss: %v, %v", hit, err)
	}
	if err := c.PutSearch(ctx, "game", "zelda", []byte(`{"degraded":false}`), 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	hit, err := c.GetSearch(ctx, "game", "zelda")
	if err != nil || string(hit) != `{"degraded":false}` {
		t.Fatalf("hit: %s, %v", hit, err)
	}
	// Kind and query are both part of the key.
	if hit, _ := c.GetSearch(ctx, "hardware", "zelda"); hit != nil {
		t.Fatal("kinds must not share entries")
	}
	if hit, _ := c.GetSearch(ctx, "game", "mario"); hit != nil {
		t.Fatal("queries must not share entries")
	}
	time.Sleep(300 * time.Millisecond)
	if hit, _ := c.GetSearch(ctx, "game", "zelda"); hit != nil {
		t.Fatal("entry must expire with its TTL")
	}
}

func TestProduct_RoundTripAndInvalidate(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	id := "11111111-1111-1111-1111-111111111111"

	if err := c.PutProduct(ctx, id, []byte(`{"id":"x"}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	hit, err := c.GetProduct(ctx, id)
	if err != nil || string(hit) != `{"id":"x"}` {
		t.Fatalf("hit: %s, %v", hit, err)
	}
	if err := c.InvalidateProduct(ctx, id); err != nil {
		t.Fatal(err)
	}
	if hit, _ := c.GetProduct(ctx, id); hit != nil {
		t.Fatal("invalidate must drop the entry")
	}
	// Invalidating an absent key is a clean no-op.
	if err := c.InvalidateProduct(ctx, id); err != nil {
		t.Fatal(err)
	}
}
