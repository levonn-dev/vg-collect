package mongotest

import (
	"context"
	"embed"
	"testing"
	"time"
)

//go:embed testdata/migrations/*.json
var faultMigrations embed.FS

// TestFreshDB_UnreachableServer pins that mongokit.Connect fails before any drop or migrate.
func TestFreshDB_UnreachableServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := freshDB(ctx, "mongodb://127.0.0.1:1/db?connectTimeoutMS=300&serverSelectionTimeoutMS=300", "t_mongotest_unreach", faultMigrations, "testdata/migrations")
	if err == nil {
		t.Fatal("freshDB connected to a port nothing listens on")
	}
}

// TestFreshDB_BadMigrationSource pins that a missing migration source dir surfaces as an error after connect and drop succeed.
func TestFreshDB_BadMigrationSource(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	_, err := freshDB(context.Background(), URL(t), "t_mongotest_badmig", faultMigrations, "no/such/dir")
	if err == nil {
		t.Fatal("freshDB succeeded with a missing migration source dir")
	}
}

// TestServerURL_PrefersEnv pins that MONGOTEST_URL, when set, is returned verbatim without touching Docker.
func TestServerURL_PrefersEnv(t *testing.T) {
	t.Setenv(envURL, "mongodb://example.invalid:1/adopted")
	got, err := serverURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "mongodb://example.invalid:1/adopted" {
		t.Fatalf("serverURL = %q, want the env value verbatim", got)
	}
}

// TestBootMongo_CanceledContext pins that a pre-canceled context fails the boot instead of hanging on the daemon.
func TestBootMongo_CanceledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("docker client interaction")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootMongo(ctx); err == nil {
		t.Fatal("bootMongo succeeded with a canceled context")
	}
}
