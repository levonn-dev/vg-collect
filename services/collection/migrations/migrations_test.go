package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/services/collection/migrations"
)

// newTestDB resets the shared pgtest container to an empty public schema so
// migration steps always start from a blank slate, regardless of run order.
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

func TestMigrateUpIsIdempotent(t *testing.T) {
	url := newTestDB(t)
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatalf("second up must be a no-change success: %v", err)
	}
}

func TestMigrateDownUpCycle(t *testing.T) {
	url := newTestDB(t)
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
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
	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up after down: %v", err)
	}
}

func TestSchemaGuards(t *testing.T) {
	url := newTestDB(t)
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	const productID = "22222222-2222-2222-2222-222222222222"
	const insert = `INSERT INTO entries
		(user_id, product_id, item_type, display_name, region, packaging,
		 pricing_mode, pricing_product_id, status, backlog_rank,
		 platform_igdb_id, platform_name, igdb_game_id, has_box, box_condition,
		 has_manual, manual_condition)
		VALUES ('11111111-1111-1111-1111-111111111111',
		        $1, $2, 'X', 'pal', 'cib', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`

	ok := func(t *testing.T, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, insert, args...); err != nil {
			t.Fatalf("expected insert to pass: %v", err)
		}
	}
	violates := func(t *testing.T, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, insert, args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("expected a check violation, got %v", err)
		}
	}

	// A well-formed backlog game with a rank passes.
	ok(t, productID, "game", "auto", nil, "backlog", "n", int64(6), "SNES", int64(1000), false, nil, false, nil)
	// A custom entry (no product) with disabled pricing and a free-text platform passes.
	ok(t, nil, "game", "disabled", nil, "beaten", nil, nil, "Homebrew Handheld", nil, false, nil, false, nil)
	// Backlog without a rank violates the rank invariant.
	violates(t, productID, "game", "auto", nil, "backlog", nil, nil, nil, nil, false, nil, false, nil)
	// A rank outside the backlog violates it too.
	violates(t, productID, "game", "auto", nil, "beaten", "n", nil, nil, nil, false, nil, false, nil)
	// Proxy pricing without its override product.
	violates(t, productID, "game", "proxy", nil, "beaten", nil, nil, nil, nil, false, nil, false, nil)
	// Auto pricing without an own product (customs cannot be auto).
	violates(t, nil, "game", "auto", nil, "beaten", nil, nil, nil, nil, false, nil, false, nil)
	// An igdb platform id without its name.
	violates(t, productID, "game", "auto", nil, "beaten", nil, int64(6), nil, nil, false, nil, false, nil)
	// An IGDB game id on hardware.
	violates(t, productID, "console", "auto", nil, "beaten", nil, nil, nil, int64(1000), false, nil, false, nil)
	// An IGDB game id on a custom entry without a proxy to carry it.
	violates(t, nil, "game", "disabled", nil, "beaten", nil, nil, nil, int64(1000), false, nil, false, nil)
	// A proxying custom entry may carry the identity of its target.
	ok(t, nil, "game", "proxy", productID, "beaten", nil, nil, nil, int64(1000), false, nil, false, nil)
	// A box grade without a box.
	violates(t, productID, "game", "auto", nil, "beaten", nil, nil, nil, nil, false, "mint", false, nil)
	// A manual grade without a manual.
	violates(t, productID, "game", "auto", nil, "beaten", nil, nil, nil, nil, false, nil, false, "good")

	// Tag names are unique per user case-insensitively (citext).
	if _, err := pool.Exec(ctx,
		`INSERT INTO tags (user_id, name) VALUES ('11111111-1111-1111-1111-111111111111', 'RPG')`); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO tags (user_id, name) VALUES ('11111111-1111-1111-1111-111111111111', 'rpg')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected a case-insensitive unique violation, got %v", err)
	}

	// Deleting an entry cascades its tag links.
	var entryID, tagID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM entries LIMIT 1`).Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM tags LIMIT 1`).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO entry_tags (entry_id, tag_id) VALUES ($1, $2)`, entryID, tagID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM entries WHERE id = $1`, entryID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entry_tags`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("entry_tags must cascade, %d rows remain", n)
	}
}

// TestSlugBackfillCollisionFree pins 000009: seeds views whose derived slugs
// collide across dedupe partitions, then checks the backfill lands on unique slugs.
func TestSlugBackfillCollisionFree(t *testing.T) {
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
	if err := m.Migrate(8); err != nil {
		t.Fatalf("migrate to 8: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	const userID = "11111111-1111-1111-1111-111111111111"
	const insert = `INSERT INTO saved_views (user_id, name, params, created_at)
		VALUES ($1, $2, '{"v":1}', $3)`
	base := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(ctx, insert, userID, "Games", base); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, insert, userID, "Games!!!", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, insert, userID, "Games2", base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(9); err != nil {
		t.Fatalf("migrate to 9 (slug backfill): %v", err)
	}

	rows, err := conn.Query(ctx,
		`SELECT name, slug, slug_key FROM saved_views WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ name, slug, key string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.slug, &r.key); err != nil {
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
	// Games claims "games" unsuffixed (oldest). Games!!! also folds to "games",
	// loses, and claims "games2" before Games2 arrives; Games2 then probes past
	// "games2" to "games22". No two rows share a fold key.
	want := []row{
		{"Games", "Games", "games"},
		{"Games!!!", "Games2", "games2"},
		{"Games2", "Games22", "games22"},
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
	keys := map[string]bool{}
	for _, r := range got {
		if keys[r.key] {
			t.Fatalf("duplicate slug_key %q among %+v", r.key, got)
		}
		keys[r.key] = true
	}
}

// TestSlugBackfillSuffixBoundaryMatchesAppDerivation pins the clamp at a
// 30-char slug with an underscore at position 29: after trimming, the
// backfill must land on "...A2", matching store_views.go's dedupe exactly.
func TestSlugBackfillSuffixBoundaryMatchesAppDerivation(t *testing.T) {
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
	if err := m.Migrate(8); err != nil {
		t.Fatalf("migrate to 8: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	const userID = "11111111-1111-1111-1111-111111111111"
	base28A := strings.Repeat("A", 28)
	// Distinct names (uniqueness forbids literal dupes) both derive the same
	// 30-char slug: name1's doubled underscore collapses to match name2's single one.
	name1 := base28A + "__Z"
	name2 := base28A + "_Z"
	const insert = `INSERT INTO saved_views (user_id, name, params, created_at)
		VALUES ($1, $2, '{"v":1}', $3)`
	base := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(ctx, insert, userID, name1, base); err != nil {
		t.Fatal(err)
	}
	// name2 derives the same slug as name1, so it collides and must probe to a suffixed value.
	if _, err := conn.Exec(ctx, insert, userID, name2, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(9); err != nil {
		t.Fatalf("migrate to 9 (slug backfill): %v", err)
	}

	rows, err := conn.Query(ctx,
		`SELECT slug, slug_key FROM saved_views WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ slug, key string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.slug, &r.key); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	wantSlug := base28A + "2"
	if got[1].slug != wantSlug {
		t.Fatalf("second row slug = %q, want %q (app-parity: rtrim drops the underscore the clamp exposed)", got[1].slug, wantSlug)
	}
	wantKey := strings.ToLower(wantSlug)
	if got[1].key != wantKey {
		t.Fatalf("second row slug_key = %q, want %q", got[1].key, wantKey)
	}
}

// TestEntryLocalizedColumns pins 000010: the localized-presentation trio
// lands as nullable text so a region with no localization stores NULL.
func TestEntryLocalizedColumns(t *testing.T) {
	url := newTestDB(t)
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	for _, col := range []string{"localized_name", "localized_name_translit", "localized_cover_url"} {
		var dataType, nullable string
		err := conn.QueryRow(ctx, `
			SELECT data_type, is_nullable FROM information_schema.columns
			WHERE table_name = 'entries' AND column_name = $1`, col).Scan(&dataType, &nullable)
		if err != nil {
			t.Fatalf("%s: %v", col, err)
		}
		if dataType != "text" || nullable != "YES" {
			t.Fatalf("%s is %s/%s, want text/YES", col, dataType, nullable)
		}
	}
}

func TestEntryCreditColumns(t *testing.T) {
	url := newTestDB(t)
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	for _, col := range []string{"developers", "publishers"} {
		var dataType, udt, nullable string
		err := conn.QueryRow(ctx, `
			SELECT data_type, udt_name, is_nullable FROM information_schema.columns
			WHERE table_name = 'entries' AND column_name = $1`, col).Scan(&dataType, &udt, &nullable)
		if err != nil {
			t.Fatalf("%s: %v", col, err)
		}
		if dataType != "ARRAY" || udt != "_text" || nullable != "YES" {
			t.Fatalf("%s is %s/%s/%s, want ARRAY/_text/YES", col, dataType, udt, nullable)
		}
	}
}

func TestCustomPricingConstraints(t *testing.T) {
	url := newTestDB(t)
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	insert := func(mode string, valueCents *int64, setAt *time.Time) error {
		_, err := conn.Exec(ctx, `
			INSERT INTO entries (user_id, item_type, display_name, region, packaging,
				pricing_mode, custom_value_cents, custom_value_set_at, status, backlog_rank)
			VALUES (gen_random_uuid(), 'game', 'x', 'ntsc_u', 'loose', $1, $2, $3, 'backlog', 'a')`,
			mode, valueCents, setAt)
		return err
	}
	wantCheck := func(err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("want CHECK violation (23514), got %v", err)
		}
	}
	now := time.Now()
	v := int64(12345)

	// custom mode requires a value.
	wantCheck(insert("custom", nil, nil))
	// the pair travels together.
	wantCheck(insert("disabled", &v, nil))
	wantCheck(insert("disabled", nil, &now))
	// unknown mode still rejected; custom accepted with the pair.
	wantCheck(insert("bogus", nil, nil))
	if err := insert("custom", &v, &now); err != nil {
		t.Fatalf("valid custom insert: %v", err)
	}
	if err := insert("disabled", &v, &now); err != nil {
		t.Fatalf("memory pair under another mode must be allowed: %v", err)
	}
	// negative value rejected.
	neg := int64(-1)
	wantCheck(insert("custom", &neg, &now))
}

// TestEntryRegionOpenWorld pins 000013: a free-text region persists like a
// known one instead of tripping a 23514 violation, since the CHECK retires.
func TestEntryRegionOpenWorld(t *testing.T) {
	url := newTestDB(t)
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	_, err = conn.Exec(ctx, `
		INSERT INTO entries (user_id, item_type, display_name, region, packaging,
			pricing_mode, status, backlog_rank)
		VALUES (gen_random_uuid(), 'game', 'x', 'Korea', 'loose', 'disabled', 'backlog', 'a')`)
	if err != nil {
		t.Fatalf("a free-text region must be accepted once the CHECK retires: %v", err)
	}
}

// TestEntryRegionOpenWorldDownRejects: down to 12 restores entries_region_check
// (free-text region rejected); back up to 13 accepts it again.
func TestEntryRegionOpenWorldDownRejects(t *testing.T) {
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
	if err := m.Migrate(13); err != nil {
		t.Fatalf("migrate to 13: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	const insert = `INSERT INTO entries (user_id, item_type, display_name, region, packaging,
		pricing_mode, status, backlog_rank)
		VALUES (gen_random_uuid(), 'game', 'x', 'Korea', 'loose', 'disabled', 'backlog', 'a')`

	if err := m.Migrate(12); err != nil {
		t.Fatalf("down to 12: %v", err)
	}
	_, err = conn.Exec(ctx, insert)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || !strings.Contains(pgErr.ConstraintName, "entries_region_check") {
		t.Fatalf("down migration must restore entries_region_check, got %v", err)
	}

	if err := m.Migrate(13); err != nil {
		t.Fatalf("back up to 13: %v", err)
	}
	if _, err := conn.Exec(ctx, insert); err != nil {
		t.Fatalf("re-up must accept the free-text region again: %v", err)
	}
}
