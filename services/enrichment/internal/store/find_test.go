package store

// White-box (package store, not store_test): findAll/findPage are
// unexported, so pinning their own contract directly - independent
// of any one caller's filter/projection shape - needs a test inside
// the package. Their callers' own black-box tests in
// store_test.go already re-verify the same contract end to end
// (ListPriced, ListUnmatchedProducts, and friends); this one isolates
// the two generics themselves against a scratch collection, so a
// future change to their op-wrap or zero-match behavior fails here
// first. A dedicated collection name (never touched by any other
// test in this package) keeps this test independent of the
// products-collection reset every other test in this package does.

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
	col := client.Database("enrichment").Collection("scanall_findall_test_docs")
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

// TestFindAll_ZeroMatchesIsNil pins the package's convention (matching
// every one of its ten converted call sites): a zero-match result is
// nil, not an empty slice - cur.All leaves a `var out []T` destination
// untouched when the cursor yields no documents.
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

// TestFindAll_WrapsFindIssueError pins the op-wrap on a real
// Find-issue failure: an invalid filter operator is rejected by the
// server (not the driver locally), giving a genuine wrapped error
// without needing to fake the driver.
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

// TestFindPage_CountAndFindOpsWrapIndependently pins the reason
// findPage takes two op strings instead of one: ListPromoteCandidateProducts
// uses different text for its count failure than its find failure,
// while the other two paginated readers reuse the same text for both -
// both shapes must stay reachable through the same generic.
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

	// The find-issue leg wraps under findOp, not countOp: forcing a
	// server-side filter error only on the find (the count succeeds
	// against the same filter first) proves the two ops are not
	// aliases of one shared parameter.
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
