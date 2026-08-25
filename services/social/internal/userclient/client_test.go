package userclient

import (
	"context"
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

func TestCardsByIDs(t *testing.T) {
	id := uuid.New()
	var gotPath, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"profiles":[{"user_id":"` + id.String() +
			`","handle":"alice","profile_visibility":"listed"}]}`))
	})
	cards, err := c.CardsByIDs(context.Background(), "tok", []uuid.UUID{id})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/shared/profiles/by-ids" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if len(cards) != 1 || cards[0].UserID != id || cards[0].Handle != "alice" || cards[0].Visibility != "listed" {
		t.Fatalf("cards = %+v", cards)
	}

	errClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := errClient.CardsByIDs(context.Background(), "tok", []uuid.UUID{id}); err == nil {
		t.Fatal("want an error for a 500 from the user service")
	}
}
