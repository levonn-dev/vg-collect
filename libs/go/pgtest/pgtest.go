// Package pgtest hands each test binary its own freshly created postgres database and
// connection URL, plus FreshPool for a reset, migrated, connected pool on top. The server
// is the shared container from PGTEST_URL when set, else a per-binary testcontainer.
package pgtest

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vgkeep/libs/go/ctrtest"
	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

var shared ctrtest.Container

// envURL names the shared postgres server the Taskfile sets via PGTEST_URL; when set, the
// kit adopts it instead of booting a container, avoiding Docker calls a frozen daemon could kill.
const envURL = "PGTEST_URL"

// serverURL resolves the server for this binary's database: the shared server when present, else a booted container.
func serverURL(ctx context.Context) (string, error) {
	if v := os.Getenv(envURL); v != "" {
		return v, nil
	}
	return bootPostgres(ctx)
}

// bootPostgres starts a postgres:17-alpine container and returns its connection URL.
// The dual wait strategy (log line seen twice, since postgres restarts after initdb, plus
// the port accepting connections) is required: either alone flaked under WSL2 Docker networking.
// No Terminate: the testcontainers reaper collects the container when the process exits.
func bootPostgres(ctx context.Context) (string, error) {
	// 180s deadlines outlast multi-minute freezes a loaded dev-host Docker daemon can hit
	// (WithWaitStrategy alone silently caps the wait at 60s).
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("pgtest"), tcpostgres.WithUsername("pgtest"), tcpostgres.WithPassword("pgtest"),
		testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(180*time.Second),
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(180*time.Second)))
	if err != nil {
		return "", err
	}
	return pg.ConnectionString(ctx, "sslmode=disable")
}

// URL returns this binary's database URL: drop-and-recreated on first call, memoized after.
// Call it directly from the package under test - the database name derives from the caller's
// directory, keeping binaries under `go test -p 2` on separate databases. Prefer FreshPool
// unless you need a reset database without a forced migrate.
func URL(t *testing.T) string {
	t.Helper()
	return urlFor(t, callerDir())
}

// FreshPool returns a connected pool against this binary's database, reset to a clean
// schema and migrated from fsys/dir. Each service supplies its own embedded migrations.
func FreshPool(t *testing.T, fsys fs.FS, dir string) *pgxpool.Pool {
	t.Helper()
	pool, err := freshPool(context.Background(), urlFor(t, callerDir()), fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// callerDir returns the directory of the file two frames up: the test file that called URL
// or FreshPool. Captured eagerly - inside the boot closure the stack belongs to ctrtest, not the caller.
func callerDir() string {
	_, file, _, _ := runtime.Caller(2)
	return filepath.Dir(file)
}

// urlFor memoizes the binary's database URL: the first call resolves the server and
// drop-creates the database; later calls return the cached URL.
func urlFor(t *testing.T, pkgDir string) string {
	t.Helper()
	name := ctrtest.DBName(pkgDir)
	return shared.URL(t, func(ctx context.Context) (string, error) {
		base, err := serverURL(ctx)
		if err != nil {
			return "", err
		}
		return createFreshDB(ctx, base, name)
	})
}

// createFreshDB drops and recreates the named database on baseURL's server, so a new
// database survives even a killed prior run. WITH (FORCE) kicks lingering connections
// so a leaked pool from a crashed binary cannot block the recreate.
func createFreshDB(ctx context.Context, baseURL, name string) (string, error) {
	conn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close(ctx) }()
	ident := pgx.Identifier{name}.Sanitize()
	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS " + ident + " WITH (FORCE)",
		"CREATE DATABASE " + ident,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return "", err
		}
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

// freshPool is FreshPool's error-returning core, split out so failure paths are testable
// with crafted inputs; t.Fatal cannot be observed from inside the same test process.
func freshPool(ctx context.Context, dbURL string, fsys fs.FS, dir string) (*pgxpool.Pool, error) {
	// Reset drops everything (schema_migrations included) and re-runs migrations for a
	// fresh, fully migrated database. Two Execs: pgx's extended protocol takes one statement at a time.
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			return nil, err
		}
	}
	_ = conn.Close(ctx)
	if err := pgkit.Migrate(dbURL, fsys, dir); err != nil {
		return nil, err
	}
	return pgkit.Connect(ctx, dbURL)
}
