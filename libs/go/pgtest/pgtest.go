// Package pgtest hands each test binary its own freshly created
// postgres database and the connection URL for it, plus FreshPool for
// the common case of a reset, migrated, and connected pool on top.
// The server behind that database is either the long-lived shared
// container the Taskfile manages (PGTEST_URL set: zero Docker traffic
// from the tests themselves) or, for bare `go test` runs outside the
// Taskfile, a one-shot testcontainer booted per binary. Each service
// still passes in its own migrations; this package only replaces the
// hand-rolled container boot and reset-migrate-connect sequence
// duplicated across every store, handlers, and migrations test
// fixture.
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

// envURL names the shared postgres server the Taskfile starts once and
// keeps running across runs (task test / test:cover set it). When it
// is set the kit adopts that server instead of booting a container, so
// a full suite run makes no Docker API calls a frozen daemon could
// kill. Unset (bare `go test`), the kit boots its own container
// exactly as before.
const envURL = "PGTEST_URL"

// serverURL resolves the server to put this binary's database on:
// the env-named shared server when present, a freshly booted
// per-binary container otherwise.
func serverURL(ctx context.Context) (string, error) {
	if v := os.Getenv(envURL); v != "" {
		return v, nil
	}
	return bootPostgres(ctx)
}

// bootPostgres starts a postgres:17-alpine container and returns its
// connection URL.
//
// The dual wait strategy (the startup log line, occurring twice for
// postgres's restart-after-init-db, AND the port actually accepting
// connections) is load-bearing: either alone flaked under WSL2's
// Docker networking during the per-test-container fixtures this
// package replaces. No Terminate: the testcontainers reaper collects
// the container when the test process exits.
func bootPostgres(ctx context.Context) (string, error) {
	// The 180s deadlines (outer and per-strategy: WithWaitStrategy alone
	// silently caps the whole wait at 60s) outlast the multi-minute
	// freezes a loaded dev-host Docker daemon can hit, so a frozen
	// daemon costs a slow container start instead of a failed suite.
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

// URL hands back the connection URL of this test binary's own
// database, created (drop-and-recreate, so every run starts fresh) on
// the shared server the first time it is called in the binary and
// reused by every call after that, including from other test files
// sharing the process. Call it directly from the package under test:
// the database name derives from the calling file's directory, which
// is what keeps two binaries running concurrently under `go test -p 2`
// out of each other's data. Most callers want FreshPool's
// reset-migrate-connect sequence on top of this; URL alone stays
// exported for callers that need a reset database without a forced
// migrate, such as a service's own migration-mechanics tests (partial
// migrations, down/up cycles).
func URL(t *testing.T) string {
	t.Helper()
	return urlFor(t, callerDir())
}

// FreshPool returns a connected pool against this binary's database,
// reset to a clean schema and migrated from fsys/dir. Each service
// passes its own embedded migrations - this package still never knows
// what they are, only how to reset and apply them.
func FreshPool(t *testing.T, fsys fs.FS, dir string) *pgxpool.Pool {
	t.Helper()
	pool, err := freshPool(context.Background(), urlFor(t, callerDir()), fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// callerDir returns the package directory of the file two frames up:
// the test file that called URL or FreshPool. Captured eagerly at the
// exported boundary because inside the boot closure the stack belongs
// to ctrtest, not the caller.
func callerDir() string {
	_, file, _, _ := runtime.Caller(2)
	return filepath.Dir(file)
}

// urlFor memoizes the binary's database URL: first call resolves the
// server (env or boot) and drop-creates the binary's database, later
// calls return the cached URL. Every caller in a binary is the same
// package (one test binary per package), so the first caller's derived
// name is everyone's.
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

// createFreshDB drops and recreates the named database on the server
// behind baseURL and returns baseURL pointed at it. The drop is what
// guarantees a new database per run even when the Taskfile's post-run
// sweep could not run (killed run, frozen daemon); WITH (FORCE) kicks
// lingering connections so a leaked pool from a crashed binary cannot
// block the recreate.
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

// freshPool is FreshPool's error-returning core, split out so its
// failure paths are testable with crafted inputs - a t.Fatal cannot
// be observed from inside the same test process.
func freshPool(ctx context.Context, dbURL string, fsys fs.FS, dir string) (*pgxpool.Pool, error) {
	// Reset: drop everything the previous test left (schema_migrations
	// included) and re-run the embedded migrations, so each test opens
	// on a fresh, fully migrated database - migration-seeded rows and
	// all. Two Execs because pgx's extended protocol takes one
	// statement at a time.
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
