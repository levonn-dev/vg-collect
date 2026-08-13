package store

// White-box (package store, not store_test): scanAll is unexported,
// so pinning its own contract directly - independent of any one
// caller's query shape - needs a test inside the package. The
// converted methods' own black-box tests in store_test.go and
// sibling _test.go files already re-verify the same contract end to
// end; this one isolates scanAll itself against a schema-free query
// so a future change to its seed or op handling fails here first.
// This package's callers disagree on both axes (unlike social,
// user, and auth's single-axis packages), so every combination gets
// its own case.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
)

// stubRows implements pgx.Rows over an in-memory int list, exhausting
// after len(vals) calls to Next and reporting errAfter from Err() -
// the shape only a live query cannot reach deterministically (a
// trailing rows.Err() after a clean Next()-exhaustion is a
// connection-level condition, not something a real SELECT can be
// coaxed into on demand). Only Close/Err/Next/Scan are exercised by
// scanAll; the rest of the interface panics if ever reached.
type stubRows struct {
	vals     []int
	errAfter error
	i        int
	closed   bool
}

func (s *stubRows) Close()                                       { s.closed = true }
func (s *stubRows) Err() error                                   { return s.errAfter }
func (s *stubRows) CommandTag() pgconn.CommandTag                { panic("not used by scanAll") }
func (s *stubRows) FieldDescriptions() []pgconn.FieldDescription { panic("not used by scanAll") }
func (s *stubRows) Values() ([]any, error)                       { panic("not used by scanAll") }
func (s *stubRows) RawValues() [][]byte                          { panic("not used by scanAll") }
func (s *stubRows) Conn() *pgx.Conn                              { panic("not used by scanAll") }

func (s *stubRows) Next() bool {
	if s.i >= len(s.vals) {
		return false
	}
	s.i++
	return true
}

func (s *stubRows) Scan(dest ...any) error {
	*dest[0].(*int) = s.vals[s.i-1]
	return nil
}

var _ pgx.Rows = (*stubRows)(nil)

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
// the returned slice in cursor order, seed and op held constant.
func TestScanAll_AssemblesInOrder(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()
	rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 3) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanAll(rows, nil, "", scanAllInt)
	if err != nil {
		t.Fatalf("scanAll: %v", err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("got = %v, want [1 2 3]", got)
	}
}

// TestScanAll_ZeroRowsKeepsCallersSeed pins the seed axis: a nil seed
// stays nil (store_admin.go's four readers), an empty-literal seed
// stays non-nil empty (most of the rest of the package).
func TestScanAll_ZeroRowsKeepsCallersSeed(t *testing.T) {
	conn := scanAllTestConn(t)
	ctx := context.Background()

	t.Run("nil seed stays nil", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 0) AS n`)
		if err != nil {
			t.Fatal(err)
		}
		got, err := scanAll[int](rows, nil, "", scanAllInt)
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
		got, err := scanAll(rows, []int{}, "", scanAllInt)
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
// mid-stream scan error reports (nil, err) regardless of seed or op -
// the partial rows already assembled are discarded, matching every
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
	got, err := scanAll(rows, []int{}, "wrapped op", func(r pgx.Rows) (int, error) {
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

// TestScanAll_TrailingErrRespectsOp pins the op axis on the one
// branch a live query cannot reach on demand (rows.Err() surfacing
// after a clean exhaustion, a connection-level condition): op=""
// reports rows.Err() raw alongside whatever had already been
// assembled (store_dashboard.go's Spend tail and every plain reader),
// a non-empty op wraps it under "store: <op>: %w" and discards the
// partial results (store_admin.go's four readers and ListEntries).
func TestScanAll_TrailingErrRespectsOp(t *testing.T) {
	trailingErr := errors.New("connection reset")

	t.Run(`op="" reports raw error alongside partial results`, func(t *testing.T) {
		rows := &stubRows{vals: []int{1, 2}, errAfter: trailingErr}
		got, err := scanAll(rows, []int{}, "", scanAllInt)
		if !errors.Is(err, trailingErr) || err.Error() != trailingErr.Error() {
			t.Fatalf("err = %v, want the raw %v (unwrapped)", err, trailingErr)
		}
		if !reflect.DeepEqual(got, []int{1, 2}) {
			t.Fatalf("got = %v, want the partial [1 2]", got)
		}
		if !rows.closed {
			t.Fatal("rows not closed")
		}
	})

	t.Run("non-empty op wraps the error and discards partial results", func(t *testing.T) {
		rows := &stubRows{vals: []int{1, 2}, errAfter: trailingErr}
		got, err := scanAll(rows, []int{}, "list things", scanAllInt)
		if !errors.Is(err, trailingErr) {
			t.Fatalf("err = %v, want it to wrap %v", err, trailingErr)
		}
		wantText := "store: list things: " + trailingErr.Error()
		if err.Error() != wantText {
			t.Fatalf("err text = %q, want %q", err.Error(), wantText)
		}
		if got != nil {
			t.Fatalf("got = %v, want nil (discarded on a wrapped trailing error)", got)
		}
	})
}

// TestScanAll_OpEmptyCallerDispatch pins the small dispatch
// DashboardCounts's Spend tail (store_dashboard.go) and
// ListListedShelves (store_views.go) both wrote around scanAll's
// op="" contract to restore their exact pre-conversion per-branch
// behavior: `out, err := scanAll(rows, []T{}, "", scan); if out ==
// nil && err != nil { return <the function's own zero-value shape>,
// err }` (a scan error - discard everything, matching what both
// functions did before conversion) `; <merge out into the success
// value>; return <success value>, err` (a trailing rows.Err() - keep
// whatever had already been scanned, matching what both functions
// did before conversion, including leaving the error unwrapped). The
// pattern is short and used at exactly 2 sites, below this package's
// own 3-site/10-line duplication bar (see discovery-3's own stated
// threshold), so it stays inline at both call sites rather than
// becoming a third shared helper; this test pins the two sites'
// shared REASONING (the dispatch condition genuinely distinguishes
// the two original branches), not a shared function.
//
// A live query cannot deterministically produce a trailing rows.Err()
// on demand (it is a connection-level condition, not something a
// hardcoded production SELECT can be coaxed into failing on), so the
// trailing-error subtest below drives scanAll directly via stubRows,
// same as TestScanAll_TrailingErrRespectsOp above. The scan-error
// subtest uses a real connection since that failure mode - unlike a
// trailing error - is trivial to trigger on demand.
func TestScanAll_OpEmptyCallerDispatch(t *testing.T) {
	t.Run("scan error: dispatch discards, matching the pre-conversion (zero value, err)", func(t *testing.T) {
		conn := scanAllTestConn(t)
		ctx := context.Background()
		rows, err := conn.Query(ctx, `SELECT n FROM generate_series(1, 3) AS n`)
		if err != nil {
			t.Fatal(err)
		}
		wantErr := errors.New("scan boom")
		calls := 0
		out, err := scanAll(rows, []int{}, "", func(r pgx.Rows) (int, error) {
			calls++
			if calls == 2 {
				return 0, wantErr
			}
			return scanAllInt(r)
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
		if out != nil || err == nil {
			t.Fatalf("out = %v, err = %v: the dispatch's discard condition (out == nil && err != nil) did not fire on a scan error", out, err)
		}
	})

	t.Run("trailing error: dispatch keeps the partial value, matching the pre-conversion (out/total/fields, raw err)", func(t *testing.T) {
		trailingErr := errors.New("connection reset")
		rows := &stubRows{vals: []int{1, 2}, errAfter: trailingErr}
		out, err := scanAll(rows, []int{}, "", scanAllInt)
		if out == nil && err != nil {
			t.Fatal("the dispatch's discard condition wrongly fired on a trailing error - the partial value would be lost, contradicting the pre-conversion behavior")
		}
		if !reflect.DeepEqual(out, []int{1, 2}) {
			t.Fatalf("out = %v, want the partial [1 2] preserved for the caller to merge into its success value", out)
		}
		if !errors.Is(err, trailingErr) || err.Error() != trailingErr.Error() {
			t.Fatalf("err = %v, want the raw (unwrapped) %v alongside the partial value", err, trailingErr)
		}
	})
}
