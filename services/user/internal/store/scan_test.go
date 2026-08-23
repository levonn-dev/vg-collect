package store

// White-box (package store, not store_test): scanAll is unexported,
// so pinning its own contract directly - independent of any one
// caller's query shape - needs a test inside the package. Its
// callers' own black-box tests in store_test.go already
// re-verify the same contract end to end; this one isolates scanAll
// itself against a schema-free query so a future change to its
// signature or seed handling fails here first.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
)

func scanAllTestConn(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	return conn
}

func scanAllInt(r pgx.Rows) (int, error) {
	var n int
	err := r.Scan(&n)
	return n, err
}

// TestScanAll_AssemblesInOrder pins the happy path: every row lands in
// the returned slice in cursor order, seeded or not.
func TestScanAll_AssemblesInOrder(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()
	rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 3) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanAll(rows, nil, scanAllInt)
	if err != nil {
		t.Fatalf("scanAll: %v", err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("got = %v, want [1 2 3]", got)
	}
}

// TestScanAll_ZeroRowsKeepsCallersSeed pins this package's two
// zero-row conventions living side by side: rolesQ seeds nil (a
// role-less user reports nil, not []), GetByIDs/SearchListed seed
// []T{} (an empty batch answers [] on the wire, not null) - scanAll
// must preserve whichever one the caller passed rather than picking
// one for both.
func TestScanAll_ZeroRowsKeepsCallersSeed(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()

	t.Run("nil seed stays nil", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 0) AS n`)
		if err != nil {
			t.Fatal(err)
		}
		got, err := scanAll[int](rows, nil, scanAllInt)
		if err != nil {
			t.Fatalf("scanAll: %v", err)
		}
		if got != nil {
			t.Fatalf("got = %v, want nil", got)
		}
	})

	t.Run("empty seed stays non-nil empty", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 0) AS n`)
		if err != nil {
			t.Fatal(err)
		}
		got, err := scanAll(rows, []int{}, scanAllInt)
		if err != nil {
			t.Fatalf("scanAll: %v", err)
		}
		if got == nil {
			t.Fatal("got nil, want a non-nil empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("got = %v, want empty", got)
		}
	})
}

// TestScanAll_ScanErrorDropsPartialResults pins the failure path: a
// mid-stream scan error reports (nil, err) regardless of seed - the
// partial rows already assembled are discarded, matching every
// hand-written loop this generic replaces.
func TestScanAll_ScanErrorDropsPartialResults(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()
	rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 3) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("boom")
	calls := 0
	got, err := scanAll(rows, []int{}, func(r pgx.Rows) (int, error) {
		calls++
		if calls == 2 {
			return 0, wantErr
		}
		return scanAllInt(r)
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("got = %v, want nil on a scan error", got)
	}
}
