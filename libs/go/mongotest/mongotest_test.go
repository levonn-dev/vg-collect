package mongotest_test

import (
	"context"
	"embed"
	"flag"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/levonn-dev/vgkeep/libs/go/mongotest"
)

//go:embed testdata/migrations/*.json
var testMigrations embed.FS

// TestURL_BootsConnectsAndQueries pins that URL returns a live connection string that round-trips a real document.
func TestURL_BootsConnectsAndQueries(t *testing.T) {
	url := mongotest.URL(t)
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(url))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	coll := client.Database(mongotest.DBName(t)).Collection("probe")
	if _, err := coll.InsertOne(ctx, bson.M{"k": "v"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var got bson.M
	if err := coll.FindOne(ctx, bson.M{"k": "v"}).Decode(&got); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got["k"] != "v" {
		t.Fatalf("found doc = %v, want k=v", got)
	}
}

// TestFreshDB_MigratesAndResetsBetweenCalls pins that FreshDB applies migrations and resets data between calls.
func TestFreshDB_MigratesAndResetsBetweenCalls(t *testing.T) {
	ctx := context.Background()

	db := mongotest.FreshDB(t, testMigrations, "testdata/migrations")
	if _, err := db.Collection("t").InsertOne(ctx, bson.M{"k": "v"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	count, err := db.Collection("t").CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("count after insert: %v", err)
	}
	if count != 1 {
		t.Fatalf("count after insert = %d, want 1", count)
	}

	// A second call reuses the shared container but must hand back a reset, re-migrated database.
	db2 := mongotest.FreshDB(t, testMigrations, "testdata/migrations")
	count, err = db2.Collection("t").CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("count after reset: %v", err)
	}
	if count != 0 {
		t.Fatalf("count after reset = %d, want 0 (FreshDB must reset between calls)", count)
	}
}

// TestURL_SharedAcrossCalls pins that a second call in the same binary reuses the first call's container.
func TestURL_SharedAcrossCalls(t *testing.T) {
	first := mongotest.URL(t)
	second := mongotest.URL(t)
	if first != second {
		t.Fatalf("URL varied across calls: %q vs %q, want the shared per-suite singleton", first, second)
	}
}

// TestDBName_StableAndPrefixed pins that DBName is stable within a binary and carries the t_ sweep prefix.
func TestDBName_StableAndPrefixed(t *testing.T) {
	name := mongotest.DBName(t)
	if name != mongotest.DBName(t) {
		t.Fatalf("DBName varied across calls in one binary: %q vs %q", name, mongotest.DBName(t))
	}
	if !strings.HasPrefix(name, "t_") {
		t.Fatalf("DBName %q lacks the t_ sweep prefix", name)
	}
}

// TestURL_SkipsUnderShort flips the test.short flag at runtime, the only way to drive
// testing.Short() from inside a test, and checks URL honors it.
func TestURL_SkipsUnderShort(t *testing.T) {
	orig := flag.Lookup("test.short").Value.String()
	if err := flag.Set("test.short", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := flag.Set("test.short", orig); err != nil {
			t.Fatal(err)
		}
	})

	var sub *testing.T
	t.Run("short", func(st *testing.T) {
		sub = st
		mongotest.URL(st)
		st.Error("URL returned instead of skipping under -short")
	})
	if !sub.Skipped() {
		t.Fatal("want the subtest skipped by URL's -short check")
	}
}
