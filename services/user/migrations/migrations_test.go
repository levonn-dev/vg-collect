package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/services/user/migrations"
)

func newTestDB(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("user"), tcpostgres.WithUsername("u"), tcpostgres.WithPassword("p"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// TestHandleBackfillCollisionFree drives the schema to just before the
// handle backfill (000003), seeds three users whose derived handles
// collide across dedupe partitions in a way a single-pass suffix pass
// cannot resolve ("Alice", "Alice!!!", "Alice2" all fold toward
// "alice"/"alice2"), then migrates up and checks the backfill lands on
// unique, deterministic handles instead of aborting the CREATE UNIQUE
// INDEX.
func TestHandleBackfillCollisionFree(t *testing.T) {
	url := newTestDB(t)
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	pgxURL := strings.Replace(url, "postgres://", "pgx5://", 1)
	m, err := migrate.NewWithSourceInstance("iofs", src, pgxURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(2); err != nil {
		t.Fatalf("migrate to 2: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	const insert = `INSERT INTO users (email, display_name, created_at) VALUES ($1, $2, $3)`
	base := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(ctx, insert, "alice1@example.com", "Alice", base); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, insert, "alice2@example.com", "Alice!!!", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, insert, "alice3@example.com", "Alice2", base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(3); err != nil {
		t.Fatalf("migrate to 3 (handle backfill): %v", err)
	}

	rows, err := conn.Query(ctx,
		`SELECT email, handle, handle_key FROM users ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ email, handle, key string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.email, &r.handle, &r.key); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	// "Alice" keeps its unsuffixed value (oldest, so it claims "alice"
	// first). "Alice!!!" also folds to "alice", loses the race, and
	// claims "alice2" - which is what "Alice2" would have kept
	// untouched under the old buggy single-pass dedupe. Since "Alice2"
	// is processed last and finds "alice2" already claimed, it must
	// itself probe to "alice22". No two rows ever share a fold key.
	want := []row{
		{"alice1@example.com", "Alice", "alice"},
		{"alice2@example.com", "Alice2", "alice2"},
		{"alice3@example.com", "Alice22", "alice22"},
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
	keys := map[string]bool{}
	for _, r := range got {
		if keys[r.key] {
			t.Fatalf("duplicate handle_key %q among %+v", r.key, got)
		}
		keys[r.key] = true
	}
}

// TestHandleBackfillReservedFold seeds a single user whose display_name
// derives to a reserved handle fold ("Search" folds to "search", one of
// the ReservedHandles in services/user/internal/store/handle.go) and
// checks the 000003 backfill suffixes it instead of minting the
// reserved handle itself - the app's mint path already refuses these;
// the backfill must refuse them too.
func TestHandleBackfillReservedFold(t *testing.T) {
	url := newTestDB(t)
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	pgxURL := strings.Replace(url, "postgres://", "pgx5://", 1)
	m, err := migrate.NewWithSourceInstance("iofs", src, pgxURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(2); err != nil {
		t.Fatalf("migrate to 2: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	const insert = `INSERT INTO users (email, display_name, created_at) VALUES ($1, $2, $3)`
	base := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(ctx, insert, "searcher@example.com", "Search", base); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(3); err != nil {
		t.Fatalf("migrate to 3 (handle backfill): %v", err)
	}

	var handle, key string
	if err := conn.QueryRow(ctx,
		`SELECT handle, handle_key FROM users WHERE email = $1`, "searcher@example.com",
	).Scan(&handle, &key); err != nil {
		t.Fatal(err)
	}
	if handle != "Search2" {
		t.Fatalf("handle = %q, want %q", handle, "Search2")
	}
	if key != "search2" {
		t.Fatalf("handle_key = %q, want %q (reserved fold %q must not be assigned)", key, "search2", "search")
	}
}

// TestHandleBackfillSuffixBoundaryMatchesAppDerivation pins the
// dedupe-suffix clamp at its exact boundary: a display_name whose
// derived handle is exactly 30 characters with an underscore at
// position 29 clamps (for the length-1 suffix "2") right at that
// underscore. services/user/internal/store/store.go's Upsert probe
// loop always trims a trailing underscore a clamp exposes before
// appending the suffix digit; the backfill must land on the identical
// string ("...A2"), not the pre-fix "...A_2" - the fold is the same
// either way, but the stored typed form would otherwise diverge from
// what the app would have minted for the same input.
func TestHandleBackfillSuffixBoundaryMatchesAppDerivation(t *testing.T) {
	url := newTestDB(t)
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	pgxURL := strings.Replace(url, "postgres://", "pgx5://", 1)
	m, err := migrate.NewWithSourceInstance("iofs", src, pgxURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(2); err != nil {
		t.Fatalf("migrate to 2: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	base28A := strings.Repeat("A", 28)
	displayName := base28A + "_Z" // 30 chars; clamp(29) lands exactly on the underscore
	const insert = `INSERT INTO users (email, display_name, created_at) VALUES ($1, $2, $3)`
	base := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(ctx, insert, "boundary1@example.com", displayName, base); err != nil {
		t.Fatal(err)
	}
	// Same display_name derives the same 30-char handle, so this row
	// collides with row 1's claimed fold and must probe to a suffixed
	// value.
	if _, err := conn.Exec(ctx, insert, "boundary2@example.com", displayName, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(3); err != nil {
		t.Fatalf("migrate to 3 (handle backfill): %v", err)
	}

	var handle, key string
	if err := conn.QueryRow(ctx,
		`SELECT handle, handle_key FROM users WHERE email = $1`, "boundary2@example.com",
	).Scan(&handle, &key); err != nil {
		t.Fatal(err)
	}
	wantHandle := base28A + "2"
	if handle != wantHandle {
		t.Fatalf("handle = %q, want %q (app-parity: rtrim drops the underscore the clamp exposed)", handle, wantHandle)
	}
	wantKey := strings.ToLower(wantHandle)
	if key != wantKey {
		t.Fatalf("handle_key = %q, want %q", key, wantKey)
	}
}
