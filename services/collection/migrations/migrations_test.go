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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/collection/migrations"
)

func newTestDB(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("collection"), tcpostgres.WithUsername("c"), tcpostgres.WithPassword("p"),
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
	// A custom entry (no product) with disabled pricing passes; its
	// platform may be a bare free-text name.
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
