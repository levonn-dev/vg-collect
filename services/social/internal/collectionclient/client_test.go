package collectionclient

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	return reqtest.NewTestClient(t, h, func(baseURL string) *Client {
		c, err := New(baseURL)
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
}

func TestSharedShelf(t *testing.T) {
	id := uuid.New()
	ownerID := uuid.New()
	var gotPath, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + id.String() + `","name":"Retro","owner_id":"` + ownerID.String() +
			`","params":{},"slug":"retro","visibility":"listed"}`))
	})
	shelf, err := c.SharedShelf(context.Background(), "tok", id)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/shelves/"+id.String() {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if shelf.ID != id || shelf.OwnerID != ownerID || shelf.Name != "Retro" || shelf.Visibility != "listed" {
		t.Fatalf("shelf = %+v", shelf)
	}

	errClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := errClient.SharedShelf(context.Background(), "tok", id); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}
