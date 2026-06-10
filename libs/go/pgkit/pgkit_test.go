package pgkit_test

import (
	"context"
	"embed"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

func TestConnectMigrateHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("t"), tcpostgres.WithUsername("t"), tcpostgres.WithPassword("t"),
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
	if err := pgkit.Migrate(url, testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := pgkit.Migrate(url, testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("Migrate (idempotent rerun): %v", err)
	}
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgkit.Health(ctx, pool); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO t (id) VALUES (1)"); err != nil {
		t.Fatalf("migrated table missing: %v", err)
	}
}

func TestMigrate_BadSourceDir(t *testing.T) {
	if err := pgkit.Migrate("postgres://u:p@localhost:5432/x", testMigrations, "no/such/dir"); err == nil {
		t.Fatal("want migration source error")
	}
}

func TestMigrate_BadURL(t *testing.T) {
	if err := pgkit.Migrate("not-a-url", testMigrations, "testdata/migrations"); err == nil {
		t.Fatal("want migrate init error")
	}
}

func TestConnect_BadURL(t *testing.T) {
	if _, err := pgkit.Connect(context.Background(), "://nope"); err == nil {
		t.Fatal("want parse error")
	}
}
