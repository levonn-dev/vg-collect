package db_test

import (
	"context"
	"testing"
	"time"

	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/db"
	"github.com/levonn-dev/vg-collect/services/enrichment/migrations"
)

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
	for _, name := range []string{"products_game_identity", "products_hardware_identity"} {
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
