package pgtest_test

import (
	"context"
	"flag"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
)

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
