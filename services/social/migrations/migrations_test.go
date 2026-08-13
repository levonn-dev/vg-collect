package migrations_test

import (
	"context"
	"testing"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/services/social/migrations"
)

// newTestDB resets the shared pgtest container to an empty public
// schema, so this package's migration steps always start on the same
// blank slate the old per-test container gave them, whether or not
// another test in this binary already ran.
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
