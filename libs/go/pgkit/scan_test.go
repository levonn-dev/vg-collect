package pgkit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

// TestScanAll drives the whole contract: scan order, nil vs empty-slice seed, and error propagation.
func TestScanAll(t *testing.T) {
	ctx := context.Background()
	url := newTestPostgresURL(t)
	if err := pgkit.Migrate(url, testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(pool.Close)

	for _, id := range []int{1, 2, 3} {
		if _, err := pool.Exec(ctx, "INSERT INTO t (id) VALUES ($1)", id); err != nil {
			t.Fatalf("insert %d: %v", id, err)
		}
	}

	scanID := func(r pgx.Rows) (int, error) {
		var id int
		err := r.Scan(&id)
		return id, err
	}

	rows, err := pool.Query(ctx, "SELECT id FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got, err := pgkit.ScanAll(rows, nil, scanID)
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if want := []int{1, 2, 3}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v, want %v", got, want)
	}

	rows, err = pool.Query(ctx, "SELECT id FROM t WHERE id > 100")
	if err != nil {
		t.Fatalf("query empty: %v", err)
	}
	gotNil, err := pgkit.ScanAll(rows, nil, scanID)
	if err != nil {
		t.Fatalf("ScanAll nil seed: %v", err)
	}
	if gotNil != nil {
		t.Fatalf("got %v, want nil for a zero-row nil-seeded result", gotNil)
	}

	rows, err = pool.Query(ctx, "SELECT id FROM t WHERE id > 100")
	if err != nil {
		t.Fatalf("query empty: %v", err)
	}
	gotEmpty, err := pgkit.ScanAll(rows, []int{}, scanID)
	if err != nil {
		t.Fatalf("ScanAll empty seed: %v", err)
	}
	if gotEmpty == nil || len(gotEmpty) != 0 {
		t.Fatalf("got %v, want a non-nil empty slice", gotEmpty)
	}

	rows, err = pool.Query(ctx, "SELECT id FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	wantErr := errors.New("boom")
	if _, err := pgkit.ScanAll(rows, nil, func(pgx.Rows) (int, error) { return 0, wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

// errAfterRows is a pgx.Rows stub that yields n rows, then reports err from Err(),
// mimicking a stream failure after rows arrived (not a Scan error).
type errAfterRows struct {
	n, i int
	err  error
}

func (r *errAfterRows) Close()                                       {}
func (r *errAfterRows) Err() error                                   { return r.err }
func (r *errAfterRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *errAfterRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *errAfterRows) Values() ([]any, error)                       { return nil, nil }
func (r *errAfterRows) RawValues() [][]byte                          { return nil }
func (r *errAfterRows) Conn() *pgx.Conn                              { return nil }

func (r *errAfterRows) Next() bool {
	if r.i >= r.n {
		return false
	}
	r.i++
	return true
}

func (r *errAfterRows) Scan(dest ...any) error {
	*dest[0].(*int) = r.i
	return nil
}

// TestScanAllRowsErrReturnsPartialSlice pins that a stream error (not a Scan error) still
// returns the rows already accumulated, not nil.
func TestScanAllRowsErrReturnsPartialSlice(t *testing.T) {
	wantErr := errors.New("stream broke")
	rows := &errAfterRows{n: 3, err: wantErr}
	scanID := func(r pgx.Rows) (int, error) {
		var id int
		err := r.Scan(&id)
		return id, err
	}

	got, err := pgkit.ScanAll[int](rows, nil, scanID)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
	if want := []int{1, 2, 3}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v, want %v", got, want)
	}
}
