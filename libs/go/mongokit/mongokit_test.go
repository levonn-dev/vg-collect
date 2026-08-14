package mongokit_test

import (
	"context"
	"embed"
	"fmt"
	"net/url"
	"testing"
	"time"

	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/levonn-dev/vgkeep/libs/go/mongokit"
)

//go:embed testdata/migrations/*.json
var testMigrations embed.FS

//go:embed testdata/badmigrations/*.json
var badMigrations embed.FS

func TestUnitComposeURL_EmptyPairPassesThroughUnchanged(t *testing.T) {
	const base = "mongodb://u:p@localhost:27017/mongokit" //nolint:gosec // G101: synthetic test fixture, not a real credential
	got, err := mongokit.ComposeURL(base, "", "")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != base {
		t.Fatalf("got %q, want byte-identical %q", got, base)
	}
}

func TestUnitComposeURL_InjectsAndEscapesReservedChars(t *testing.T) {
	const (
		base     = "mongodb://mongokit-test:27017/mongokit?tls=true"
		username = "mongokit"
		password = `p@ss:w/rd?#` //nolint:gosec // G101: synthetic test fixture, not a real credential
	)
	composed, err := mongokit.ComposeURL(base, username, password)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	parsed, err := url.Parse(composed)
	if err != nil {
		t.Fatalf("parse composed url: %v", err)
	}
	if parsed.User == nil {
		t.Fatal("composed url has no userinfo")
	}
	if parsed.User.Username() != username {
		t.Fatalf("username round-trip: got %q, want %q", parsed.User.Username(), username)
	}
	gotPassword, ok := parsed.User.Password()
	if !ok {
		t.Fatal("composed url has no password")
	}
	if gotPassword != password {
		t.Fatalf("password round-trip: got %q, want %q", gotPassword, password)
	}
}

func TestUnitComposeURL_ExistingUserinfoErrors(t *testing.T) {
	_, err := mongokit.ComposeURL("mongodb://already:there@localhost:27017/mongokit", "mongokit", "s3cret")
	if err == nil {
		t.Fatal("want error when the base url already carries userinfo")
	}
}

// TestUnitComposeURL_UnparsableBaseErrors drives ComposeURL's other
// error leg: once username/password are set, a baseURL that
// url.Parse itself rejects (invalid percent-encoding) must error
// instead of proceeding on a zero-value URL.
func TestUnitComposeURL_UnparsableBaseErrors(t *testing.T) {
	_, err := mongokit.ComposeURL("mongodb://%zz", "mongokit", "s3cret")
	if err == nil {
		t.Fatal("want error for an unparsable base url")
	}
}

// newTestMongoURL starts a throwaway MongoDB and returns its URL.
// Integration helper: skipped under -short.
func newTestMongoURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	mc, err := tcmongo.Run(ctx, "mongo:8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Terminate(ctx) })
	uri, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return uri
}

func TestMigrate_CreatesCollectionAndIsIdempotent(t *testing.T) {
	uri := newTestMongoURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := mongokit.Migrate(ctx, uri, "mongokit_test", testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second run must be a clean no-op (init containers re-run on every
	// pod start).
	if err := mongokit.Migrate(ctx, uri, "mongokit_test", testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	client, err := mongokit.Connect(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	mdb := client.Database("mongokit_test")

	specs, err := mdb.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range specs {
		kinds[s.Name] = s.Type
	}
	if kinds["t"] != "collection" {
		t.Fatalf("collection t missing (got %q)", kinds["t"])
	}
}

// TestMigrate_BadSourceDir drives Migrate's migration-source error
// leg: a directory the embedded FS does not have must surface as an
// error once Connect has already succeeded.
func TestMigrate_BadSourceDir(t *testing.T) {
	uri := newTestMongoURL(t)
	ctx := context.Background()
	if err := mongokit.Migrate(ctx, uri, "mongokit_test", testMigrations, "no/such/dir"); err == nil {
		t.Fatal("want migration source error")
	}
}

// TestMigrate_UpError drives Migrate's real-migration-failure leg
// (distinct from the ErrNoChange success case): a migration file
// naming a MongoDB command that does not exist must fail m.Up() and
// surface through Migrate rather than silently succeeding.
func TestMigrate_UpError(t *testing.T) {
	uri := newTestMongoURL(t)
	ctx := context.Background()
	if err := mongokit.Migrate(ctx, uri, "mongokit_test_bad", badMigrations, "testdata/badmigrations"); err == nil {
		t.Fatal("want migrate error for an invalid migration command")
	}
}

func TestHealth(t *testing.T) {
	uri := newTestMongoURL(t)
	ctx := context.Background()
	client, err := mongokit.Connect(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := mongokit.Health(ctx, client); err != nil {
		t.Fatalf("health: %v", err)
	}
}

// TestConnect_BadURL drives Connect's client-construction error leg:
// options.Client().ApplyURI parses eagerly, so mongo.Connect surfaces
// a malformed URI without any network dial.
func TestConnect_BadURL(t *testing.T) {
	_, err := mongokit.Connect(context.Background(), "not-a-mongo-url")
	if err == nil {
		t.Fatal("want connect error for a malformed uri")
	}
}

// TestConnect_PingFail drives Connect's ping error leg. Port 1 is
// administratively prohibited, so the refusal is immediate; needs no
// docker.
func TestConnect_PingFail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := mongokit.Connect(ctx, "mongodb://127.0.0.1:1/db?connectTimeoutMS=500&serverSelectionTimeoutMS=500")
	if err == nil {
		t.Fatal("want ping error for an unreachable host")
	}
}

// TestConnect_ReservedCharPasswordViaComposedURL starts a throwaway
// Mongo with a reserved-char root password (the testcontainers module
// sets it directly as MONGO_INITDB_ROOT_PASSWORD, unescaped), composes
// a creds-less URL plus that pair through ComposeURL exactly as a
// service's main does, and connects with the result - proving the
// escaping round-trips against a real server rather than only
// net/url's own parser.
func TestConnect_ReservedCharPasswordViaComposedURL(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	const (
		username = "mongokit"
		password = `p@ss:w/rd?#` //nolint:gosec // G101: synthetic test fixture, not a real credential
	)
	ctx := context.Background()
	mc, err := tcmongo.Run(ctx, "mongo:8", tcmongo.WithUsername(username), tcmongo.WithPassword(password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Terminate(ctx) })

	host, err := mc.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := mc.MappedPort(ctx, "27017/tcp")
	if err != nil {
		t.Fatal(err)
	}
	credsLessURL := fmt.Sprintf("mongodb://%s:%s/?authSource=admin", host, port.Port())

	dsn, err := mongokit.ComposeURL(credsLessURL, username, password)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	client, err := mongokit.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect with composed dsn: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := mongokit.Health(ctx, client); err != nil {
		t.Fatalf("health: %v", err)
	}
}
