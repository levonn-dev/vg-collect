package cache

import (
	"context"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/libs/go/valkeytest"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	ctx := context.Background()
	client, err := valkeykit.Connect(ctx, valkeytest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
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

func TestValueHistoryRoundTripAndSharedInvalidation(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()

	if body, err := c.GetValueHistory(ctx, "alice"); err != nil || body != nil {
		t.Fatalf("cold read must be a nil miss: %v %v", body, err)
	}
	if err := c.PutValueHistory(ctx, "alice", []byte(`{"available":true}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.PutDashboard(ctx, "alice", []byte(`{"total_entries":5}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	if body, _ := c.GetValueHistory(ctx, "bob"); body != nil {
		t.Fatal("keys are per-subject")
	}
	// One mutation invalidates BOTH composed bodies: they derive from
	// the same entry set.
	if err := c.InvalidateDashboard(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if body, _ := c.GetDashboard(ctx, "alice"); body != nil {
		t.Fatal("dashboard must be evicted")
	}
	if body, _ := c.GetValueHistory(ctx, "alice"); body != nil {
		t.Fatal("value history must be evicted by the same invalidation")
	}
}
