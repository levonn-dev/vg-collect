package store

// White-box (package store, not store_test): findAll/findPage are
// unexported, so pinning their contract directly needs a test inside
// the package, isolated on a scratch collection. A dedicated
// collection name keeps it independent of the products-collection reset every other test does.

import (
	"context"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/levonn-dev/vgkeep/libs/go/mongotest"
)

type findAllDoc struct {
	ID string `bson:"_id"`
	N  int    `bson:"n"`
}

func findAllTestCollection(t *testing.T) *mongo.Collection {
	t.Helper()
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongotest.URL(t)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	col := client.Database(mongotest.DBName(t)).Collection("scanall_findall_test_docs")
	if err := col.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = col.Drop(context.Background()) })
	return col
}

func mustInsert(t *testing.T, col *mongo.Collection, docs ...findAllDoc) {
	t.Helper()
	for _, d := range docs {
		if _, err := col.InsertOne(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFindAll_AssemblesInOrder pins the happy path: every matching
// document lands in the returned slice in the requested sort order.
func TestFindAll_AssemblesInOrder(t *testing.T) {
	col := findAllTestCollection(t)
	mustInsert(t, col, findAllDoc{ID: "b", N: 2}, findAllDoc{ID: "a", N: 1}, findAllDoc{ID: "c", N: 3})

	got, err := findAll[findAllDoc](context.Background(), col, bson.D{},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}), "test op")
	if err != nil {
		t.Fatalf("findAll: %v", err)
	}
	want := []findAllDoc{{ID: "a", N: 1}, {ID: "b", N: 2}, {ID: "c", N: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got = %+v, want %+v", got, want)
	}
}

// Pins the package's convention: a zero-match result is nil, not []
// (cur.All leaves `var out []T` untouched when the cursor yields nothing).
func TestFindAll_ZeroMatchesIsNil(t *testing.T) {
	col := findAllTestCollection(t)
	got, err := findAll[findAllDoc](context.Background(), col,
		bson.D{{Key: "n", Value: -1}}, nil, "test op")
	if err != nil {
		t.Fatalf("findAll: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
}

// Pins the op-wrap on a real Find-issue failure: an invalid filter
// operator is server-rejected, giving a genuine wrapped error without faking the driver.
func TestFindAll_WrapsFindIssueError(t *testing.T) {
	col := findAllTestCollection(t)
	_, err := findAll[findAllDoc](context.Background(), col,
		bson.D{{Key: "n", Value: bson.D{{Key: "$badOperator", Value: 1}}}}, nil, "test op")
	if err == nil {
		t.Fatal("want an error for an invalid filter operator")
	}
	if got := err.Error(); !errorHasPrefix(got, "store: test op: ") {
		t.Fatalf("err = %q, want it wrapped under \"store: test op: \"", got)
	}
}

func errorHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Pins why findPage takes two op strings: some callers word count vs
// find failures differently, others reuse one text; both shapes must work.
func TestFindPage_CountAndFindOpsWrapIndependently(t *testing.T) {
	col := findAllTestCollection(t)
	mustInsert(t, col, findAllDoc{ID: "a", N: 1}, findAllDoc{ID: "b", N: 2}, findAllDoc{ID: "c", N: 3})

	page, total, err := findPage[findAllDoc](context.Background(), col, bson.D{},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(1).SetLimit(1),
		"count op", "find op")
	if err != nil {
		t.Fatalf("findPage: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3 (the full filtered count, independent of skip/limit)", total)
	}
	if want := []findAllDoc{{ID: "b", N: 2}}; !reflect.DeepEqual(page, want) {
		t.Fatalf("page = %+v, want %+v", page, want)
	}

	// The find-issue leg wraps under findOp, not countOp: the count
	// succeeds against the same filter first, proving the two ops aren't aliased.
	_, _, err = findPage[findAllDoc](context.Background(), col,
		bson.D{{Key: "n", Value: bson.D{{Key: "$badOperator", Value: 1}}}}, nil, "count op", "find op")
	if err == nil {
		t.Fatal("want an error for an invalid filter operator")
	}
	if got := err.Error(); !errorHasPrefix(got, "store: count op: ") {
		t.Fatalf("err = %q, want the COUNT to fail first and wrap under \"store: count op: \" (CountDocuments runs before Find)", got)
	}
}

// TestFindPage_ZeroMatchesIsNilWithZeroTotal mirrors findAll's
// zero-match contract for the paginated sibling.
func TestFindPage_ZeroMatchesIsNilWithZeroTotal(t *testing.T) {
	col := findAllTestCollection(t)
	page, total, err := findPage[findAllDoc](context.Background(), col,
		bson.D{{Key: "n", Value: -1}}, nil, "count op", "find op")
	if err != nil {
		t.Fatalf("findPage: %v", err)
	}
	if page != nil {
		t.Fatalf("page = %v, want nil", page)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
}
