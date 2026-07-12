package db_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/db"
	"github.com/levonn-dev/vg-collect/services/enrichment/migrations"
)

func TestUnitComposeURL_EmptyPairPassesThroughUnchanged(t *testing.T) {
	const base = "mongodb://u:p@localhost:27017/enrichment" //nolint:gosec // G101: synthetic test fixture, not a real credential
	got, err := db.ComposeURL(base, "", "")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != base {
		t.Fatalf("got %q, want byte-identical %q", got, base)
	}
}

func TestUnitComposeURL_InjectsAndEscapesReservedChars(t *testing.T) {
	const (
		base     = "mongodb://enrichment-mongo:27017/enrichment?tls=true"
		username = "enrichment"
		password = `p@ss:w/rd?#` //nolint:gosec // G101: synthetic test fixture, not a real credential
	)
	composed, err := db.ComposeURL(base, username, password)
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
	_, err := db.ComposeURL("mongodb://already:there@localhost:27017/enrichment", "enrichment", "s3cret")
	if err == nil {
		t.Fatal("want error when the base url already carries userinfo")
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
	url, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return url
}

func TestMigrate_CreatesCatalogAndIsIdempotent(t *testing.T) {
	url := newTestMongoURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, url, "enrichment", migrations.FS, "."); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second run must be a clean no-op (init containers re-run on every
	// pod start).
	if err := db.Migrate(ctx, url, "enrichment", migrations.FS, "."); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	client, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	mdb := client.Database("enrichment")

	specs, err := mdb.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range specs {
		kinds[s.Name] = s.Type
	}
	for _, name := range []string{"products", "igdb_raw", "platforms"} {
		if kinds[name] != "collection" {
			t.Fatalf("collection %s missing (got %q)", name, kinds[name])
		}
	}
	if kinds["price_snapshots"] != "timeseries" {
		t.Fatalf("price_snapshots should be a timeseries collection, got %q", kinds["price_snapshots"])
	}

	cur, err := mdb.Collection("products").Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var idx []bson.M
	if err := cur.All(ctx, &idx); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, ix := range idx {
		name, _ := ix["name"].(string)
		unique, _ := ix["unique"].(bool)
		found[name] = unique
	}
	for _, name := range []string{"products_game_identity", "products_hardware_identity", "products_pc_listing_identity"} {
		u, ok := found[name]
		if !ok || !u {
			t.Fatalf("unique index %s missing or not unique (indexes: %v)", name, found)
		}
	}
	if _, ok := found["products_name"]; !ok {
		t.Fatal("products_name index missing")
	}
}

func TestHealth(t *testing.T) {
	url := newTestMongoURL(t)
	ctx := context.Background()
	client, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := db.Health(ctx, client); err != nil {
		t.Fatalf("health: %v", err)
	}
}

// TestConnect_ReservedCharPasswordViaComposedURL starts a throwaway
// Mongo with a reserved-char root password (the testcontainers module
// sets it directly as MONGO_INITDB_ROOT_PASSWORD, unescaped), composes
// a creds-less URL plus that pair through db.ComposeURL exactly as
// main.go does, and connects with the result - proving the escaping
// round-trips against a real server rather than only net/url's own
// parser.
func TestConnect_ReservedCharPasswordViaComposedURL(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	const (
		username = "enrichment"
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

	dsn, err := db.ComposeURL(credsLessURL, username, password)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	client, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect with composed dsn: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := db.Health(ctx, client); err != nil {
		t.Fatalf("health: %v", err)
	}
}
