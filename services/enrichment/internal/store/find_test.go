package store

// White-box (package store, not store_test): queryAll/queryPage are
// unexported, so pinning their contract directly needs a test inside
// the package, isolated on a scratch table. A dedicated table keeps it
// independent of the products table every other test resets.

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/services/enrichment/migrations"
)

type scanHelperDoc struct {
	ID string
	N  int
}

func helperTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.FreshPool(t, migrations.FS, ".")
	if _, err := pool.Exec(context.Background(),
		"CREATE TABLE scan_helper_test_docs (id text PRIMARY KEY, n int NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func scanHelperDocRow(rows pgx.Rows) (scanHelperDoc, error) {
	var d scanHelperDoc
	err := rows.Scan(&d.ID, &d.N)
	return d, err
}

func mustInsertHelperDocs(t *testing.T, pool *pgxpool.Pool, docs ...scanHelperDoc) {
	t.Helper()
	for _, d := range docs {
		if _, err := pool.Exec(context.Background(),
			"INSERT INTO scan_helper_test_docs (id, n) VALUES ($1,$2)", d.ID, d.N); err != nil {
			t.Fatal(err)
		}
	}
}

// TestQueryAll_AssemblesInOrder pins the happy path: every matching
// row lands in the returned slice in the requested sort order.
func TestQueryAll_AssemblesInOrder(t *testing.T) {
	pool := helperTestPool(t)
	mustInsertHelperDocs(t, pool, scanHelperDoc{ID: "b", N: 2}, scanHelperDoc{ID: "a", N: 1}, scanHelperDoc{ID: "c", N: 3})

	got, err := queryAll(context.Background(), pool,
		"SELECT id, n FROM scan_helper_test_docs ORDER BY id", nil, scanHelperDocRow, "test op")
	if err != nil {
		t.Fatalf("queryAll: %v", err)
	}
	want := []scanHelperDoc{{ID: "a", N: 1}, {ID: "b", N: 2}, {ID: "c", N: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got = %+v, want %+v", got, want)
	}
}

// Pins the package's convention: a zero-match result is nil, not []
// (pgkit.ScanAll leaves a nil seed untouched when no row scans).
func TestQueryAll_ZeroMatchesIsNil(t *testing.T) {
	pool := helperTestPool(t)
	got, err := queryAll(context.Background(), pool,
		"SELECT id, n FROM scan_helper_test_docs WHERE n = $1", []any{-1}, scanHelperDocRow, "test op")
	if err != nil {
		t.Fatalf("queryAll: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
}

// Pins the op-wrap on a real query failure: a nonexistent column is
// server-rejected, giving a genuine wrapped error without faking the driver.
func TestQueryAll_WrapsQueryIssueError(t *testing.T) {
	pool := helperTestPool(t)
	_, err := queryAll(context.Background(), pool,
		"SELECT nope FROM scan_helper_test_docs", nil, scanHelperDocRow, "test op")
	if err == nil {
		t.Fatal("want an error for a nonexistent column")
	}
	if got := err.Error(); !errorHasPrefix(got, "store: test op: ") {
		t.Fatalf("err = %q, want it wrapped under \"store: test op: \"", got)
	}
}

func errorHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Pins why queryPage takes two op strings: the count and find legs
// fail independently, each wrapping under its own op text.
func TestQueryPage_CountAndFindOpsWrapIndependently(t *testing.T) {
	pool := helperTestPool(t)
	mustInsertHelperDocs(t, pool, scanHelperDoc{ID: "a", N: 1}, scanHelperDoc{ID: "b", N: 2}, scanHelperDoc{ID: "c", N: 3})

	page, total, err := queryPage(context.Background(), pool,
		"SELECT count(*) FROM scan_helper_test_docs",
		"SELECT id, n FROM scan_helper_test_docs ORDER BY id OFFSET 1 LIMIT 1",
		nil, nil, scanHelperDocRow, "count op", "find op")
	if err != nil {
		t.Fatalf("queryPage: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3 (the full count, independent of offset/limit)", total)
	}
	if want := []scanHelperDoc{{ID: "b", N: 2}}; !reflect.DeepEqual(page, want) {
		t.Fatalf("page = %+v, want %+v", page, want)
	}

	// Invalid SQL in the count leg wraps under countOp.
	_, _, err = queryPage(context.Background(), pool,
		"SELECT nope FROM scan_helper_test_docs",
		"SELECT id, n FROM scan_helper_test_docs", nil, nil, scanHelperDocRow, "count op", "find op")
	if err == nil {
		t.Fatal("want an error for the invalid count query")
	}
	if got := err.Error(); !errorHasPrefix(got, "store: count op: ") {
		t.Fatalf("err = %q, want it wrapped under \"store: count op: \"", got)
	}

	// Invalid SQL in the find leg wraps under findOp, with a valid count.
	_, _, err = queryPage(context.Background(), pool,
		"SELECT count(*) FROM scan_helper_test_docs",
		"SELECT nope FROM scan_helper_test_docs", nil, nil, scanHelperDocRow, "count op", "find op")
	if err == nil {
		t.Fatal("want an error for the invalid find query")
	}
	if got := err.Error(); !errorHasPrefix(got, "store: find op: ") {
		t.Fatalf("err = %q, want it wrapped under \"store: find op: \"", got)
	}
}

// TestQueryPage_ZeroMatchesIsNilWithZeroTotal mirrors queryAll's
// zero-match contract for the paginated sibling.
func TestQueryPage_ZeroMatchesIsNilWithZeroTotal(t *testing.T) {
	pool := helperTestPool(t)
	page, total, err := queryPage(context.Background(), pool,
		"SELECT count(*) FROM scan_helper_test_docs WHERE n = $1",
		"SELECT id, n FROM scan_helper_test_docs WHERE n = $1",
		[]any{-1}, []any{-1}, scanHelperDocRow, "count op", "find op")
	if err != nil {
		t.Fatalf("queryPage: %v", err)
	}
	if page != nil {
		t.Fatalf("page = %v, want nil", page)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
}
