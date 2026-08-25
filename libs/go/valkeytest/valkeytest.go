// Package valkeytest hands each test binary a live Valkey connection URL with the binary's own
// logical database index baked in. The server is the shared container from VALKEYTEST_URL when
// set, else a per-binary testcontainer. Each suite resets per-test with FlushDB, never FlushAll:
// on the shared server FlushAll would wipe every other binary's database and the allocator too.
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

// envURL names the shared Valkey server the Taskfile sets via VALKEYTEST_URL; when set,
// the kit adopts it instead of booting a container.
const envURL = "VALKEYTEST_URL"

// allocatorKey lives in database 0, INCRed once per test binary to reserve a logical database
// index. Database 0 is never handed out, so no suite's flush can touch it.
const allocatorKey = "valkeytest_next_db"

// serverURL resolves the server for this binary's tests: the shared server when present, else a booted container.
func serverURL(ctx context.Context) (string, error) {
	if v := os.Getenv(envURL); v != "" {
		return v, nil
	}
	return bootValkey(ctx)
}

// bootedContainer retains the container bootValkey started, so the kit's own boot test can
// terminate it; every other caller leaves cleanup to the reaper.
var bootedContainer testcontainers.Container

// bootValkey starts a valkey/valkey:8-alpine container and returns its connection URL. The
// readiness wait's deadline is raised from the 60s default to 180s, to outlast dev-host Docker
// freezes. No Terminate: the testcontainers reaper collects the container when the process exits.
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

// URL reserves a logical database index once per test binary and returns the connection URL
// with that index in its path on every later call, including from other test files sharing
// the process. valkeykit.Connect (go-redis ParseURL) honors the index, isolating concurrent
// binaries under go test -p 2.
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

// allocateDB reserves the next logical database index for this binary via an atomic INCR on
// allocatorKey, modulo the server's database count (CONFIG GET, so both the fallback
// container's 16 and the Taskfile container's 64 work), then flushes that index. The flush
// makes counter wraparound between concurrent binaries harmless and covers a run whose
// post-run sweep never happened.
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
	// Records the index under the run's scope (also in database 0), so the Taskfile's
	// deferred clean can flush just this run's databases.
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
