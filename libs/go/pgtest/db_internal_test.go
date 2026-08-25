package pgtest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/libs/go/ctrtest"
)

// TestServerURL_PrefersEnv pins that PGTEST_URL, when set, is returned verbatim without touching Docker.
func TestServerURL_PrefersEnv(t *testing.T) {
	t.Setenv(envURL, "postgres://example.invalid:1/adopted")
	got, err := serverURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://example.invalid:1/adopted" {
		t.Fatalf("serverURL = %q, want the env value verbatim", got)
	}
}

// TestCreateFreshDB_RecreatesFresh pins that a second call for the same name drops the
// first call's data and the returned URL points at the new database.
func TestCreateFreshDB_RecreatesFresh(t *testing.T) {
	ctx := context.Background()
	base := URL(t)
	name := ctrtest.DBName("pgtest/createfresh-probe")

	first, err := createFreshDB(ctx, base, name)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	var current string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != name {
		t.Fatalf("connected to %q, want %q (URL rewrite must point at the new database)", current, name)
	}
	if _, err := conn.Exec(ctx, "CREATE TABLE marker (id int)"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	// Recreate under the same name: the marker table must be gone.
	second, err := createFreshDB(ctx, base, name)
	if err != nil {
		t.Fatal(err)
	}
	conn2, err := pgx.Connect(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn2.Close(ctx) }()
	var count int
	if err := conn2.QueryRow(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name = 'marker'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("marker table survived, want drop-and-recreate to start empty")
	}
}

// TestCreateFreshDB_UnreachableServer pins that a connect failure returns an error, not a panic.
func TestCreateFreshDB_UnreachableServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := createFreshDB(ctx,
		"postgres://nobody:nothing@127.0.0.1:1/nowhere?sslmode=disable&connect_timeout=1",
		"t_pgtest_unreachable_probe"); err == nil {
		t.Fatal("createFreshDB connected to a port nothing listens on")
	}
}
