// Package mongotest hands each test binary a live MongoDB connection URL plus DBName, and
// FreshDB for a reset, migrated, connected database on top. The server is the shared container
// from MONGOTEST_URL when set, else a per-binary testcontainer.
package mongotest

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/levonn-dev/vgkeep/libs/go/ctrtest"
	"github.com/levonn-dev/vgkeep/libs/go/mongokit"
)

var shared ctrtest.Container

// envURL names the shared MongoDB server the Taskfile sets via MONGOTEST_URL; when set,
// the kit adopts it instead of booting a container.
const envURL = "MONGOTEST_URL"

// serverURL resolves the server for this binary's tests: the shared server when present, else a booted container.
func serverURL(ctx context.Context) (string, error) {
	if v := os.Getenv(envURL); v != "" {
		return v, nil
	}
	return bootMongo(ctx)
}

// waitOption raises the module's default 60s readiness deadlines (log line plus listening
// port) to 180s, to outlast multi-minute freezes a loaded dev-host Docker daemon can hit.
func waitOption() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
		wait.ForLog("Waiting for connections").WithStartupTimeout(180*time.Second),
		wait.ForListeningPort("27017/tcp").WithStartupTimeout(180*time.Second))
}

// bootMongo starts a mongo:8 container and returns its connection URL. No Terminate: the
// testcontainers reaper collects it when the process exits.
func bootMongo(ctx context.Context) (string, error) {
	mc, err := tcmongo.Run(ctx, "mongo:8", waitOption())
	if err != nil {
		return "", err
	}
	return mc.ConnectionString(ctx)
}

// URL resolves the shared server once per test binary and returns its URL on every later
// call, including from other test files sharing the process.
func URL(t *testing.T) string {
	t.Helper()
	return shared.URL(t, serverURL)
}

// DBName returns this binary's database name on the shared server, derived from the caller's
// directory - call it directly from the package under test. Mongo creates databases lazily,
// so callers drop it themselves at fixture start; the t_ prefix is the Taskfile sweep's match.
func DBName(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(1)
	return ctrtest.DBName(filepath.Dir(file))
}

// FreshDB returns this binary's database on the shared server, reset (dropped) and migrated
// from fsys/dir. Each service supplies its own embedded migrations.
func FreshDB(t *testing.T, fsys fs.FS, dir string) *mongo.Database {
	t.Helper()
	_, file, _, _ := runtime.Caller(1)
	name := ctrtest.DBName(filepath.Dir(file))
	db, err := freshDB(context.Background(), URL(t), name, fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Client().Disconnect(context.Background()) })
	return db
}

// freshDB is FreshDB's error-returning core, split out so failure paths are testable with
// crafted inputs; t.Fatal cannot be observed from inside the same test process.
func freshDB(ctx context.Context, url, name string, fsys fs.FS, dir string) (*mongo.Database, error) {
	client, err := mongokit.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := client.Database(name).Drop(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	if err := mongokit.Migrate(ctx, url, name, fsys, dir); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	return client.Database(name), nil
}
