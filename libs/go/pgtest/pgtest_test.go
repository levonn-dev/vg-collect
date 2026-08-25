package pgtest_test

import (
	"context"
	"embed"
	"flag"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

// TestURL_BootsConnectsAndQueries pins that URL returns a live connection string pgx.Connect can use directly.
func TestURL_BootsConnectsAndQueries(t *testing.T) {
	url := pgtest.URL(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var got int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&got); err != nil {
		t.Fatalf("trivial query: %v", err)
	}
	if got != 1 {
		t.Fatalf("got = %d, want 1", got)
	}
}

// TestFreshPool_MigratesAndResetsBetweenCalls pins that FreshPool applies migrations and resets data between calls.
func TestFreshPool_MigratesAndResetsBetweenCalls(t *testing.T) {
	ctx := context.Background()

	pool := pgtest.FreshPool(t, testMigrations, "testdata/migrations")
	if _, err := pool.Exec(ctx, "INSERT INTO t (id) VALUES (1)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&count); err != nil {
		t.Fatalf("count after insert: %v", err)
	}
	if count != 1 {
		t.Fatalf("count after insert = %d, want 1", count)
	}

	// A second call reuses the shared container but must hand back a reset, re-migrated database.
	pool2 := pgtest.FreshPool(t, testMigrations, "testdata/migrations")
	if err := pool2.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&count); err != nil {
		t.Fatalf("count after reset: %v", err)
	}
	if count != 0 {
		t.Fatalf("count after reset = %d, want 0 (FreshPool must reset between calls)", count)
	}
}

// TestURL_SharedAcrossCalls pins that a second call in the same binary reuses the first call's container.
func TestURL_SharedAcrossCalls(t *testing.T) {
	first := pgtest.URL(t)
	second := pgtest.URL(t)
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
		pgtest.URL(st)
		st.Error("URL returned instead of skipping under -short")
	})
	if !sub.Skipped() {
		t.Fatal("want the subtest skipped by URL's -short check")
	}
}
