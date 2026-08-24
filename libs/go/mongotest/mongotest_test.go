package mongotest_test

import (
	"context"
	"flag"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/levonn-dev/vgkeep/libs/go/mongotest"
)

// TestURL_BootsConnectsAndQueries drives the whole point of the
// package: URL must hand back a live MongoDB connection string a
// plain mongo.Connect can use, with no migration or schema of its own
// required, and that connection must round-trip a real document.
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

// TestURL_SharedAcrossCalls pins the fixture's entire reason to exist:
// a second call in the same test binary must reuse the first call's
// container instead of booting another one.
func TestURL_SharedAcrossCalls(t *testing.T) {
	first := mongotest.URL(t)
	second := mongotest.URL(t)
	if first != second {
		t.Fatalf("URL varied across calls: %q vs %q, want the shared per-suite singleton", first, second)
	}
}

// TestDBName_StableAndPrefixed pins the isolation contract suites
// depend on: the same binary always gets the same name (fixtures and
// tests in different files must land in one database) and the name
// carries the t_ prefix the Taskfile's post-run sweep matches.
func TestDBName_StableAndPrefixed(t *testing.T) {
	name := mongotest.DBName(t)
	if name != mongotest.DBName(t) {
		t.Fatalf("DBName varied across calls in one binary: %q vs %q", name, mongotest.DBName(t))
	}
	if !strings.HasPrefix(name, "t_") {
		t.Fatalf("DBName %q lacks the t_ sweep prefix", name)
	}
}

// TestURL_SkipsUnderShort flips the test.short flag at runtime (there
// is no other way to drive testing.Short() from inside a test) and
// checks URL honors it, the same "go test -short" escape hatch both
// fixtures this package replaces already gave callers.
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
