package cache

import (
	"context"
	"testing"
	"time"

	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"

	"github.com/levonn-dev/vg-collect/libs/go/valkeykit"
)

func newTestCache(t *testing.T) *Cache {
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
	return New(client)
}

func TestDashboardRoundTrip(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	if body, err := c.GetDashboard(ctx, "alice"); err != nil || body != nil {
		t.Fatalf("cold read must be a nil miss: %v %v", body, err)
	}
	if err := c.PutDashboard(ctx, "alice", []byte(`{"total_entries":5}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	body, err := c.GetDashboard(ctx, "alice")
	if err != nil || string(body) != `{"total_entries":5}` {
		t.Fatalf("hit: %s %v", body, err)
	}
	// Users do not share entries.
	if body, _ := c.GetDashboard(ctx, "bob"); body != nil {
		t.Fatal("keys are per-subject")
	}
	if err := c.InvalidateDashboard(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if body, _ := c.GetDashboard(ctx, "alice"); body != nil {
		t.Fatal("invalidate must evict")
	}
	// Invalidating an absent key is a quiet success (mutations always call it).
	if err := c.InvalidateDashboard(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardTTLExpires(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	if err := c.PutDashboard(ctx, "alice", []byte(`{}`), 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if body, err := c.GetDashboard(ctx, "alice"); err != nil || body != nil {
		t.Fatalf("entry must expire with its TTL: %v %v", body, err)
	}
}
