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

// TestURL_BootsConnectsAndQueries drives the whole point of the
// package: URL must hand back a live postgres connection string a
// plain pgx.Connect can use, with no migration or schema of its own
// required.
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

// TestFreshPool_MigratesAndResetsBetweenCalls drives the whole point
// of FreshPool: the returned pool must already have the caller's
// migrations applied, and a second call must reset the previous
// call's data away instead of layering on top of it - the exact
// behavior every adopted fixture relied on the hand-rolled block for.
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

	// A second FreshPool call reuses the shared container (same
	// TestURL_SharedAcrossCalls contract below) but must hand back a
	// reset, re-migrated database: the row above must be gone.
	pool2 := pgtest.FreshPool(t, testMigrations, "testdata/migrations")
	if err := pool2.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&count); err != nil {
		t.Fatalf("count after reset: %v", err)
	}
	if count != 0 {
		t.Fatalf("count after reset = %d, want 0 (FreshPool must reset between calls)", count)
	}
}

// TestURL_SharedAcrossCalls pins the fixture's entire reason to exist:
// a second call in the same test binary must reuse the first call's
// container instead of booting another one.
func TestURL_SharedAcrossCalls(t *testing.T) {
	first := pgtest.URL(t)
	second := pgtest.URL(t)
	if first != second {
		t.Fatalf("URL varied across calls: %q vs %q, want the shared per-suite singleton", first, second)
	}
}

// TestURL_SkipsUnderShort flips the test.short flag at runtime (there
// is no other way to drive testing.Short() from inside a test) and
// checks URL honors it, the same "go test -short" escape hatch every
// fixture this package replaces already gave callers.
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
