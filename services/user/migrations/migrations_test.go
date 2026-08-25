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

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/services/user/migrations"
)

// newTestDB resets the shared pgtest container to an empty public schema,
// so migration steps always start from a blank slate regardless of prior tests.
func newTestDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatal(err)
		}
	}
	_ = conn.Close(ctx)
	return url
}

// Seeds three users whose derived handles collide across dedupe
// partitions ("Alice", "Alice!!!", "Alice2" all fold toward
// "alice"/"alice2") in a way a single-pass suffix cannot resolve, then
// checks migration 000003 lands on unique handles without aborting the CREATE UNIQUE INDEX.
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
	// "Alice" claims "alice" first (oldest). "Alice!!!" also folds to "alice",
	// loses the race, and claims "alice2" (which "Alice2" alone would have
	// kept under the old buggy dedupe). "Alice2", processed last, finds
	// "alice2" taken and probes to "alice22". No two rows share a fold key.
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

// Seeds a user whose display_name derives to a reserved fold ("Search"
// -> "search", see ReservedHandles in handle.go); the 000003 backfill must
// suffix it instead of minting the reserved handle, matching the app's mint path.
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

// Pins the suffix clamp boundary: a 30-char handle with an underscore at
// position 29 clamps there for the length-1 suffix "2". Upsert's probe
// loop trims the trailing underscore the clamp exposes before appending
// the suffix; the backfill must match ("...A2", not "...A_2") for app parity.
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
	// Same display_name derives the same handle, so this row collides with
	// row 1's claimed fold and must probe to a suffixed value.
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
