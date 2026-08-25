package pgtest

import (
	"context"
	"embed"
	"testing"
	"time"
)

//go:embed testdata/badmigrations/*.sql
var badMigrations embed.FS

// TestFreshPoolCore_UnreachableURL pins that a connect failure returns an error, not a panic.
func TestFreshPoolCore_UnreachableURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := freshPool(ctx,
		"postgres://nobody:nothing@127.0.0.1:1/nowhere?sslmode=disable&connect_timeout=1",
		badMigrations, "testdata/badmigrations")
	if err == nil {
		pool.Close()
		t.Fatal("freshPool connected to a port nothing listens on")
	}
}

// TestFreshPoolCore_BrokenMigration pins that malformed migration SQL surfaces as an error, not a half-migrated pool.
func TestFreshPoolCore_BrokenMigration(t *testing.T) {
	url := URL(t)
	pool, err := freshPool(context.Background(), url, badMigrations, "testdata/badmigrations")
	if err == nil {
		pool.Close()
		t.Fatal("freshPool succeeded over a malformed migration")
	}
}

// TestBootPostgres_CanceledContext pins that a pre-canceled context fails the boot instead of hanging on the daemon.
func TestBootPostgres_CanceledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("docker client interaction")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootPostgres(ctx); err == nil {
		t.Fatal("bootPostgres succeeded with a canceled context")
	}
}
