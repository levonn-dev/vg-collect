// Package mongotest boots one shared MongoDB testcontainer per test
// binary and hands back its connection URL. Each suite still owns its
// own connect, database-drop reset, and migration run: this package
// only replaces the hand-rolled container boot that used to be copied
// into both of enrichment's Mongo-backed test fixtures.
package mongotest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
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

// WaitOption is the module's own readiness strategy (log line plus
// listening port) with every deadline raised from the 60s defaults to
// 180s: long enough to outlast the multi-minute freezes a loaded
// dev-host Docker daemon can hit, so a frozen daemon costs a slow
// container start instead of a failed suite. Exported for callers
// that boot their own dedicated Mongo (migration scenarios) so every
// boot in the repo rides freezes out the same way.
func WaitOption() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
		wait.ForLog("Waiting for connections").WithStartupTimeout(180*time.Second),
		wait.ForListeningPort("27017/tcp").WithStartupTimeout(180*time.Second))
}

// bootMongo starts a mongo:8 container and returns its connection
// URL. No Terminate: the testcontainers reaper collects the container
// when the test process exits.
func bootMongo(ctx context.Context) (string, error) {
	mc, err := tcmongo.Run(ctx, "mongo:8", WaitOption())
	if err != nil {
		return "", err
	}
	return mc.ConnectionString(ctx)
}

// URL boots the shared MongoDB container the first time it is called
// in a test binary and returns its connection URL on every call after
// that, including from other test files and packages sharing the
// process. Callers run their own connect against the URL, plus their
// own database-drop-and-remigrate reset - each service owns its
// migrations, so this package stops at handing back a live, reachable
// database.
func URL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	url, err := shared.resolve(bootMongo)
	if err != nil {
		t.Fatal(err)
	}
	return url
}
