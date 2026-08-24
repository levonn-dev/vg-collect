// Package mongotest hands each test binary a live MongoDB connection
// URL plus DBName, the binary's own database name on that server.
// The server is either the long-lived shared container the Taskfile
// manages (MONGOTEST_URL set: zero Docker traffic from the tests
// themselves) or, for bare `go test` runs outside the Taskfile, a
// one-shot testcontainer booted per binary. Each suite still owns its
// own connect, database-drop reset, and migration run; this package
// stops at handing back a reachable server and a name no concurrently
// running binary shares.
package mongotest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vgkeep/libs/go/ctrtest"
)

var shared ctrtest.Container

// envURL names the shared MongoDB server the Taskfile starts once and
// keeps running across runs (task test / test:cover set it). When it
// is set the kit adopts that server instead of booting a container.
// Unset (bare `go test`), the kit boots its own container exactly as
// before.
const envURL = "MONGOTEST_URL"

// serverURL resolves the server this binary's tests run against: the
// env-named shared server when present, a freshly booted per-binary
// container otherwise.
func serverURL(ctx context.Context) (string, error) {
	if v := os.Getenv(envURL); v != "" {
		return v, nil
	}
	return bootMongo(ctx)
}

// waitOption is the module's own readiness strategy (log line plus
// listening port) with every deadline raised from the 60s defaults to
// 180s: long enough to outlast the multi-minute freezes a loaded
// dev-host Docker daemon can hit, so a frozen daemon costs a slow
// container start instead of a failed suite.
func waitOption() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
		wait.ForLog("Waiting for connections").WithStartupTimeout(180*time.Second),
		wait.ForListeningPort("27017/tcp").WithStartupTimeout(180*time.Second))
}

// bootMongo starts a mongo:8 container and returns its connection
// URL. No Terminate: the testcontainers reaper collects the container
// when the test process exits.
func bootMongo(ctx context.Context) (string, error) {
	mc, err := tcmongo.Run(ctx, "mongo:8", waitOption())
	if err != nil {
		return "", err
	}
	return mc.ConnectionString(ctx)
}

// URL resolves the shared server the first time it is called in a
// test binary and returns its connection URL on every call after
// that, including from other test files sharing the process. Callers
// run their own connect against the URL and scope every database
// access to DBName, so two binaries running concurrently under
// `go test -p 2` never touch each other's data.
func URL(t *testing.T) string {
	t.Helper()
	return shared.URL(t, serverURL)
}

// DBName returns this test binary's own database name on the shared
// server, derived from the calling file's directory - call it
// directly from the package under test. Mongo creates databases
// lazily, so there is nothing to provision: suites drop this database
// at fixture start (their existing reset), which is also what makes
// every run start fresh, and the t_ prefix is what the Taskfile's
// post-run sweep matches when it clears the shared server.
func DBName(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(1)
	return ctrtest.DBName(filepath.Dir(file))
}
