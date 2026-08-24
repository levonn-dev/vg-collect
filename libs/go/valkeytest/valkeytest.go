// Package valkeytest hands each test binary a live Valkey connection
// URL with the binary's own logical database index baked in. The
// server is either the long-lived shared container the Taskfile
// manages (VALKEYTEST_URL set: zero Docker traffic from the tests
// themselves) or, for bare `go test` runs outside the Taskfile, a
// one-shot testcontainer booted per binary. Each suite still owns its
// own connect and per-test reset - which must be FlushDB, not
// FlushAll: on the shared server FlushAll would wipe every other
// binary's database and the allocator along with it.
package valkeytest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vgkeep/libs/go/ctrtest"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
)

var shared ctrtest.Container

// envURL names the shared Valkey server the Taskfile starts once and
// keeps running across runs (task test / test:cover set it). When it
// is set the kit adopts that server instead of booting a container.
// Unset (bare `go test`), the kit boots its own container exactly as
// before.
const envURL = "VALKEYTEST_URL"

// allocatorKey lives in database 0 and is INCRed once per test binary
// to reserve a logical database index. Database 0 is never handed
// out, so no suite's flush can touch the counter; the Taskfile's
// post-run FLUSHALL resets it along with everything else.
const allocatorKey = "valkeytest_next_db"

// serverURL resolves the server this binary's tests run against: the
// env-named shared server when present, a freshly booted per-binary
// container otherwise.
func serverURL(ctx context.Context) (string, error) {
	if v := os.Getenv(envURL); v != "" {
		return v, nil
	}
	return bootValkey(ctx)
}

// bootedContainer retains the last container bootValkey started, so
// the kit's own boot test can terminate what it booted. Every other
// caller leaves cleanup to the testcontainers reaper.
var bootedContainer testcontainers.Container

// bootValkey starts a valkey/valkey:8-alpine container and returns its
// connection URL. The wait is the module's own readiness log line with
// every deadline raised from the 60s defaults to 180s: long enough to
// outlast the multi-minute freezes a loaded dev-host Docker daemon can
// hit, so a frozen daemon costs a slow container start instead of a
// failed suite. No Terminate: the testcontainers reaper collects the
// container when the test process exits.
func bootValkey(ctx context.Context) (string, error) {
	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine",
		testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
			wait.ForLog("* Ready to accept connections").WithStartupTimeout(180*time.Second)))
	if err != nil {
		return "", err
	}
	bootedContainer = vk
	return vk.ConnectionString(ctx)
}

// URL reserves a logical database on the shared server the first time
// it is called in a test binary and returns the connection URL with
// that index in its path on every call after that, including from
// other test files sharing the process. valkeykit.Connect (go-redis
// ParseURL underneath) honors the index, so every adopted call site
// lands in the binary's own database and two binaries running
// concurrently under `go test -p 2` never see each other's keys.
// Callers run their own per-test reset against it - FlushDB, never
// FlushAll (see the package comment).
func URL(t *testing.T) string {
	t.Helper()
	return shared.URL(t, func(ctx context.Context) (string, error) {
		base, err := serverURL(ctx)
		if err != nil {
			return "", err
		}
		return allocateDB(ctx, base)
	})
}

// allocateDB reserves the next logical database index for this binary
// and returns base with the index baked into the URL path. The index
// comes from an atomic INCR, so concurrent binaries can never draw
// the same one; the modulus wraps the counter around the server's
// database count (CONFIG GET, so the fallback container's 16 and the
// Taskfile container's 64 both just work), which with two concurrent
// binaries and a fresh FLUSHDB on reservation is harmless. The flush
// is also the fresh-per-run guarantee for runs whose post-run sweep
// never got to happen.
func allocateDB(ctx context.Context, base string) (string, error) {
	admin, err := valkeykit.Connect(ctx, base)
	if err != nil {
		return "", err
	}
	defer func() { _ = admin.Close() }()
	cfg, err := admin.ConfigGet(ctx, "databases").Result()
	if err != nil {
		return "", err
	}
	count, convErr := strconv.Atoi(cfg["databases"])
	if convErr != nil || count < 2 {
		return "", fmt.Errorf("valkey reports databases=%q, need a numeric value of at least 2 to hand out per-binary indexes", cfg["databases"])
	}
	n, err := admin.Incr(ctx, allocatorKey).Result()
	if err != nil {
		return "", err
	}
	idx := int(n%int64(count-1)) + 1
	// Record the index under the run's scope (also in database 0) so
	// the Taskfile's deferred clean can flush exactly this run's
	// databases instead of a concurrent run's.
	if run := os.Getenv("TESTDS_RUN"); run != "" {
		if err := admin.SAdd(ctx, "valkeytest_run_"+run, idx).Err(); err != nil {
			return "", err
		}
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + strconv.Itoa(idx)
	derived := u.String()
	client, err := valkeykit.Connect(ctx, derived)
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()
	if err := client.FlushDB(ctx).Err(); err != nil {
		return "", err
	}
	return derived, nil
}
