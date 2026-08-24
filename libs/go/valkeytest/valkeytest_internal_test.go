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

// TestMain disables the testcontainers reaper when the shared server
// is adopted: the only container this binary then boots is the boot
// test's throwaway, which terminates itself, so the reaper is pure
// risk - its startup wait is hardcoded to the 60s default inside
// testcontainers, the one window the kit's 180s deadlines cannot
// cover when the Docker daemon stalls. Bare runs keep the reaper: the
// shared singleton container relies on it for cleanup.
func TestMain(m *testing.M) {
	if os.Getenv(envURL) != "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
	os.Exit(m.Run())
}

// TestServerURL_PrefersEnv pins the adoption seam: with VALKEYTEST_URL
// set, serverURL must hand it back verbatim without touching Docker
// (the value is a sentinel no daemon could produce).
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

// TestBootValkey_CanceledContext pins bootValkey's error return
// without paying for a container: a pre-canceled context must fail
// the boot instead of hanging on the daemon.
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

// TestBootValkey_BootsAndAnswers exercises the real fallback path -
// env cleared, serverURL falling through to bootValkey. Under the
// Taskfile the suite adopts the shared server and this path never
// runs, so without this test the fallback everyone relies on for bare
// `go test` would only ever be proven outside the gates. One small
// throwaway container per run is the price; the test terminates it.
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
	// Terminate what this test booted: under the adopted-server gates
	// the reaper is off (see TestMain), so nothing else would.
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

// TestAllocateDB_UnreachableServer pins the connect-failure return:
// nothing listens on the target, so the error must come back instead
// of dying inside a helper.
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

// TestAllocateDB_DistinctFlushedIndexes drives the reservation
// contract against a real server: consecutive allocations must hand
// out different indexes (that is the whole isolation story for
// concurrent binaries), never index 0 (the allocator's home), and a
// freshly reserved database must come back flushed even when a
// previous run left keys in it.
func TestAllocateDB_DistinctFlushedIndexes(t *testing.T) {
	ctx := context.Background()
	// Strip this binary's own index off URL(t) to recover the server
	// base; this works identically in adopted and booted modes.
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

	// Predict the next index in the cycle and plant a stale key there,
	// standing in for residue from a run whose sweep never happened.
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

// TestAllocateDB_TracksRunScope pins the seam the Taskfile's scoped
// clean depends on: with TESTDS_RUN set, the allocated index must be
// recorded in the run's set in database 0.
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
