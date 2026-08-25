package valkeytest

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
)

// TestMain: under the shared server every boot self-terminates; the reaper's
// hardcoded 60s startup wait would be the only unprotected window.
func TestMain(m *testing.M) {
	if os.Getenv(envURL) != "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
	os.Exit(m.Run())
}

// TestServerURL_PrefersEnv pins that VALKEYTEST_URL, when set, is returned verbatim without touching Docker.
func TestServerURL_PrefersEnv(t *testing.T) {
	t.Setenv(envURL, "redis://example.invalid:1")
	got, err := serverURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "redis://example.invalid:1" {
		t.Fatalf("serverURL = %q, want the env value verbatim", got)
	}
}

// TestBootValkey_CanceledContext pins that a pre-canceled context fails the boot instead of hanging on the daemon.
func TestBootValkey_CanceledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("docker client interaction")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootValkey(ctx); err == nil {
		t.Fatal("bootValkey succeeded with a canceled context")
	}
}

// TestBootValkey_BootsAndAnswers exercises the fallback path (env cleared) that the
// adopted-server gates skip, so bare `go test` stays proven; terminates its throwaway container.
func TestBootValkey_BootsAndAnswers(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	t.Setenv(envURL, "")
	ctx := context.Background()
	booted, err := serverURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Terminate what this test booted: the reaper is off under the adopted-server gates (TestMain).
	if bootedContainer != nil {
		t.Cleanup(func() { _ = bootedContainer.Terminate(context.Background()) })
	}
	client, err := valkeykit.Connect(ctx, booted)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping the booted container: %v", err)
	}
}

// TestAllocateDB_UnreachableServer pins that a connect failure returns an error, not a panic.
func TestAllocateDB_UnreachableServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := allocateDB(ctx, "redis://127.0.0.1:1"); err == nil {
		t.Fatal("allocateDB connected to a port nothing listens on")
	}
}

// pathIndex extracts the logical database index allocateDB baked into
// a returned URL.
func pathIndex(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(u.Path, "/"))
	if err != nil {
		t.Fatalf("URL %q carries no database index: %v", rawURL, err)
	}
	return idx
}

// TestAllocateDB_DistinctFlushedIndexes pins that consecutive allocations return distinct,
// non-zero indexes, and a reserved database comes back flushed even with prior residue.
func TestAllocateDB_DistinctFlushedIndexes(t *testing.T) {
	ctx := context.Background()
	// Strip this binary's index off URL(t) to recover the server base; works in both modes.
	u, err := url.Parse(URL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = ""
	base := u.String()

	first, err := allocateDB(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	idxFirst := pathIndex(t, first)
	if idxFirst < 1 {
		t.Fatalf("allocated index %d, want >= 1 (index 0 is the allocator's)", idxFirst)
	}

	// Predict the next index in the cycle and plant a stale key there, standing in for sweep residue.
	admin, err := valkeykit.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	cfg, err := admin.ConfigGet(ctx, "databases").Result()
	if err != nil {
		t.Fatal(err)
	}
	count, err := strconv.Atoi(cfg["databases"])
	if err != nil {
		t.Fatal(err)
	}
	idxNext := idxFirst%(count-1) + 1
	u.Path = "/" + strconv.Itoa(idxNext)
	seed, err := valkeykit.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seed.Close() }()
	if err := seed.Set(ctx, "stale-residue", "1", 0).Err(); err != nil {
		t.Fatal(err)
	}

	second, err := allocateDB(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	idxSecond := pathIndex(t, second)
	if idxSecond != idxNext {
		t.Fatalf("second allocation got index %d, want the predicted %d", idxSecond, idxNext)
	}
	if idxSecond == idxFirst {
		t.Fatalf("both allocations landed on index %d, want distinct databases", idxFirst)
	}
	fresh, err := valkeykit.Connect(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	left, err := fresh.Exists(ctx, "stale-residue").Result()
	if err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatal("stale key survived allocation, want the reserved database flushed")
	}
}

// TestAllocateDB_TracksRunScope pins that with TESTDS_RUN set, the allocated index is recorded in the run's set in database 0.
func TestAllocateDB_TracksRunScope(t *testing.T) {
	ctx := context.Background()
	u, err := url.Parse(URL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = ""
	base := u.String()

	t.Setenv(envURL, base)
	t.Setenv("TESTDS_RUN", "trackprobe")
	got, err := allocateDB(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := valkeykit.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	defer func() { _ = admin.Del(ctx, "valkeytest_run_trackprobe").Err() }()
	tracked, err := admin.SMembers(ctx, "valkeytest_run_trackprobe").Result()
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(pathIndex(t, got))
	for _, m := range tracked {
		if m == want {
			return
		}
	}
	t.Fatalf("index %s not in the run's tracking set (got %v)", want, tracked)
}
