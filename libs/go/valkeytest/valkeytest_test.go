package valkeytest_test

import (
	"context"
	"flag"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/libs/go/valkeytest"
)

// TestURL_BootsConnectsAndRoundTrips pins that URL returns a live connection string valkeykit.Connect can reach.
func TestURL_BootsConnectsAndRoundTrips(t *testing.T) {
	url := valkeytest.URL(t)
	ctx := context.Background()
	client, err := valkeykit.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Set(ctx, "valkeytest-probe", "1", 0).Err(); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := client.Get(ctx, "valkeytest-probe").Result()
	if err != nil || got != "1" {
		t.Fatalf("get: got=%q err=%v", got, err)
	}
}

// TestURL_SharedAcrossCalls pins that a second call in the same binary reuses the first call's container.
func TestURL_SharedAcrossCalls(t *testing.T) {
	first := valkeytest.URL(t)
	second := valkeytest.URL(t)
	if first != second {
		t.Fatalf("URL varied across calls: %q vs %q, want the shared per-suite singleton", first, second)
	}
}

// TestURL_SkipsUnderShort flips the test.short flag at runtime, the only way to drive
// testing.Short() from inside a test, and checks URL honors it.
func TestURL_SkipsUnderShort(t *testing.T) {
	orig := flag.Lookup("test.short").Value.String()
	if err := flag.Set("test.short", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := flag.Set("test.short", orig); err != nil {
			t.Fatal(err)
		}
	})

	var sub *testing.T
	t.Run("short", func(st *testing.T) {
		sub = st
		valkeytest.URL(st)
		st.Error("URL returned instead of skipping under -short")
	})
	if !sub.Skipped() {
		t.Fatal("want the subtest skipped by URL's -short check")
	}
}
