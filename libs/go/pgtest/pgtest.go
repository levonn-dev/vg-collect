// Package pgtest boots one shared postgres testcontainer per test
// binary and hands back its connection URL. Each service still owns
// its migrate/connect/reset; this package only replaces the
// hand-rolled container boot duplicated across every store, handlers,
// and migrations test fixture.
package pgtest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// container boots at most once and remembers either its URL or its
// boot error, so every caller after the first - success or failure -
// gets the same outcome instead of retrying a boot that already ran.
type container struct {
	once sync.Once
	url  string
	err  error
}

// resolve runs boot the first time it is called and returns its URL
// (or its boot error, cached the same way) on every call after that.
// Plain error return, not a *testing.T dependency: that keeps the
// once.Do memoization - including the failure leg, where a real boot
// failure isn't something a test can reliably trigger and once.Do
// would refuse to retry it anyway - unit-testable with a stub.
func (c *container) resolve(boot func(context.Context) (string, error)) (string, error) {
	c.once.Do(func() {
		c.url, c.err = boot(context.Background())
	})
	return c.url, c.err
}

var shared container

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

// URL boots the shared postgres container the first time it is called
// in a test binary and returns its connection URL on every call after
// that, including from other test files and packages sharing the
// process. Callers run their own migrate/connect against the URL (and
// their own schema reset first, if the test needs to start from an
// empty database) - each service owns its migrations, so this package
// stops at handing back a live, reachable database.
func URL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	url, err := shared.resolve(bootPostgres)
	if err != nil {
		t.Fatal(err)
	}
	return url
}
