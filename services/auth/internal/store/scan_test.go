package store

// White-box (package store, not store_test): scanAll is unexported,
// so pinning its own contract directly - independent of any one
// caller's query shape - needs a test inside the package. The
// converted methods' own black-box tests in store_test.go already
// re-verify the same contract end to end; this one isolates scanAll
// itself against a schema-free query so a future change to its
// signature or zero-row behavior fails here first.

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
// the returned slice in cursor order.
func TestScanAll_AssemblesInOrder(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()
	rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 3) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanAll(rows, scanAllInt)
	if err != nil {
		t.Fatalf("scanAll: %v", err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("got = %v, want [1 2 3]", got)
	}
}

// TestScanAll_ZeroRowsIsNil pins the package's convention: a zero-row
// result is nil, matching both of this package's own call sites (var
// keys/ids []T, never an empty-literal seed).
func TestScanAll_ZeroRowsIsNil(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()
	rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 0) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanAll(rows, scanAllInt)
	if err != nil {
		t.Fatalf("scanAll: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
}

// TestScanAll_ScanErrorDropsPartialResults pins the failure path: a
// mid-stream scan error reports (nil, err) - the partial rows already
// assembled are discarded, matching every hand-written loop this
// generic replaces (they all `return nil, fmt.Errorf(...)` on a scan
// failure, never the partial slice).
func TestScanAll_ScanErrorDropsPartialResults(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()
	rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 3) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("boom")
	calls := 0
	got, err := scanAll(rows, func(r pgx.Rows) (int, error) {
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
