package socialclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// TestRelayMethods_RouteBearerStatusAndBody checks verb, path, bearer, and
// relay fidelity across every Result-returning method.
func TestRelayMethods_RouteBearerStatusAndBody(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	var status int
	var gotMethod, gotPath, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = w.Write([]byte(`{"echo":true}`))
		}
	})

	cases := []struct {
		name         string
		call         func() (Result, error)
		method, path string
		okStatus     int
	}{
		{"Follow", func() (Result, error) {
			return c.Follow(context.Background(), "tok", id)
		}, "PUT", "/follows/" + id.String(), http.StatusNoContent},
		{"Unfollow", func() (Result, error) {
			return c.Unfollow(context.Background(), "tok", id)
		}, "DELETE", "/follows/" + id.String(), http.StatusNoContent},
		{"Like", func() (Result, error) {
			return c.Like(context.Background(), "tok", id)
		}, "PUT", "/likes/" + id.String(), http.StatusNoContent},
		{"Unlike", func() (Result, error) {
			return c.Unlike(context.Background(), "tok", id)
		}, "DELETE", "/likes/" + id.String(), http.StatusNoContent},
		{"ListComments", func() (Result, error) {
			return c.ListComments(context.Background(), "tok", id, nil, nil)
		}, "GET", "/shelves/" + id.String() + "/comments", http.StatusOK},
		{"CreateComment", func() (Result, error) {
			return c.CreateComment(context.Background(), "tok", id, []byte(`{}`))
		}, "POST", "/shelves/" + id.String() + "/comments", http.StatusCreated},
		{"DeleteComment", func() (Result, error) {
			return c.DeleteComment(context.Background(), "tok", id)
		}, "DELETE", "/comments/" + id.String(), http.StatusNoContent},
		{"PurgeUserData", func() (Result, error) {
			return c.PurgeUserData(context.Background(), "tok")
		}, "DELETE", "/user-data", http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status = tc.okStatus
			res, err := tc.call()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != tc.method || gotPath != tc.path {
				t.Fatalf("%s: routed %s %s, want %s %s", tc.name, gotMethod, gotPath, tc.method, tc.path)
			}
			if gotAuth != "Bearer tok" {
				t.Fatalf("%s: bearer = %q", tc.name, gotAuth)
			}
			if res.Status != tc.okStatus || res.ContentType != "application/json" {
				t.Fatalf("%s: relay = %+v", tc.name, res)
			}
			if tc.okStatus != http.StatusNoContent && string(res.Body) != `{"echo":true}` {
				t.Fatalf("%s: body = %s", tc.name, res.Body)
			}
		})
	}
}

// TestRelayMethods_WhitelistPerMethod proves each method's own allow-list:
// every whitelisted status relays, and a status a sibling method allows but
// this one doesn't (e.g. Like excludes 400 though Follow allows it) is ErrUpstream.
func TestRelayMethods_WhitelistPerMethod(t *testing.T) {
	id := uuid.New()
	var status int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = w.Write([]byte(`{}`))
		}
	})

	cases := []struct {
		name   string
		call   func() (Result, error)
		accept []int
		reject int
	}{
		{"Follow", func() (Result, error) { return c.Follow(context.Background(), "tok", id) },
			[]int{http.StatusNoContent, http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests},
			http.StatusForbidden},
		{"Unfollow", func() (Result, error) { return c.Unfollow(context.Background(), "tok", id) },
			[]int{http.StatusNoContent}, http.StatusBadRequest},
		{"Like", func() (Result, error) { return c.Like(context.Background(), "tok", id) },
			[]int{http.StatusNoContent, http.StatusNotFound, http.StatusTooManyRequests}, http.StatusBadRequest},
		{"Unlike", func() (Result, error) { return c.Unlike(context.Background(), "tok", id) },
			[]int{http.StatusNoContent}, http.StatusNotFound},
		{"ListComments", func() (Result, error) { return c.ListComments(context.Background(), "tok", id, nil, nil) },
			[]int{http.StatusOK, http.StatusBadRequest}, http.StatusNotFound},
		{"CreateComment", func() (Result, error) { return c.CreateComment(context.Background(), "tok", id, []byte(`{}`)) },
			[]int{http.StatusCreated, http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests},
			http.StatusForbidden},
		{"DeleteComment", func() (Result, error) { return c.DeleteComment(context.Background(), "tok", id) },
			[]int{http.StatusNoContent, http.StatusForbidden, http.StatusNotFound}, http.StatusBadRequest},
		{"PurgeUserData", func() (Result, error) { return c.PurgeUserData(context.Background(), "tok") },
			[]int{http.StatusNoContent}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, s := range tc.accept {
				status = s
				res, err := tc.call()
				if err != nil {
					t.Fatalf("status %d: want accepted, got err %v", s, err)
				}
				if res.Status != s {
					t.Fatalf("status %d: relay status = %d", s, res.Status)
				}
			}
			status = tc.reject
			if _, err := tc.call(); !errors.Is(err, ErrUpstream) {
				t.Fatalf("status %d: want ErrUpstream, got %v", tc.reject, err)
			}
		})
	}
}

// TestProfileSummary_DecodesTyped decodes JSON200 into ProfileSocialSummary
// and forwards the caller's bearer and target user id.
func TestProfileSummary_DecodesTyped(t *testing.T) {
	uid := uuid.New()
	var gotPath, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"follower_count":4,"following_count":9,"viewer_follows":true}`))
	})
	sum, err := c.ProfileSummary(context.Background(), "tok-1", uid)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/profiles/"+uid.String()+"/summary" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok-1" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if sum.FollowerCount != 4 || sum.FollowingCount != 9 || !sum.ViewerFollows {
		t.Fatalf("summary = %+v", sum)
	}
}

// TestShelvesSummary_DecodesTyped proves the batch shelf-summary typed read decodes the summaries envelope.
func TestShelvesSummary_DecodesTyped(t *testing.T) {
	shelfID := uuid.New()
	var gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/shelves/summary" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"summaries":[{"shelf_id":"` + shelfID.String() + `","like_count":3,"comment_count":1,"viewer_likes":true}]}`))
	})
	summaries, err := c.ShelvesSummary(context.Background(), "tok-2", []uuid.UUID{shelfID})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-2" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if len(summaries) != 1 || summaries[0].LikeCount != 3 || summaries[0].CommentCount != 1 || !summaries[0].ViewerLikes {
		t.Fatalf("summaries = %+v", summaries)
	}
}

// TestCommentsByIDs_DecodesTyped proves the feed-hydration typed read decodes the comments envelope.
func TestCommentsByIDs_DecodesTyped(t *testing.T) {
	commentID, shelfID, authorID := uuid.New(), uuid.New(), uuid.New()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/comments/by-ids" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"comments":[{"id":"` + commentID.String() + `","shelf_id":"` + shelfID.String() +
			`","author_id":"` + authorID.String() + `","body":"nice","created_at":"2026-01-01T00:00:00Z"}]}`))
	})
	comments, err := c.CommentsByIDs(context.Background(), "tok", []uuid.UUID{commentID})
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Id != commentID || comments[0].Body != "nice" {
		t.Fatalf("comments = %+v", comments)
	}
}

// TestFeed_DecodesTypedAndForwardsParams forwards tab/cursor/limit as query
// params and decodes the events envelope plus keyset next_cursor.
func TestFeed_DecodesTypedAndForwardsParams(t *testing.T) {
	evID, actor, target := uuid.New(), uuid.New(), uuid.New()
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/feed" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"events":[{"id":"` + evID.String() + `","actor_id":"` + actor.String() +
			`","target_user_id":"` + target.String() + `","verb":"followed_user","created_at":"2026-01-01T00:00:00Z"}],"next_cursor":"abc"}`))
	})
	cur := "cur-1"
	events, next, err := c.Feed(context.Background(), "tok", "following", &cur, 25)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "tab=following") || !strings.Contains(gotQuery, "cursor=cur-1") || !strings.Contains(gotQuery, "limit=25") {
		t.Fatalf("query = %s", gotQuery)
	}
	if len(events) != 1 || events[0].Verb != "followed_user" || events[0].Id != evID {
		t.Fatalf("events = %+v", events)
	}
	if next == nil || *next != "abc" {
		t.Fatalf("next cursor = %v", next)
	}
}

// TestTopShelves_DecodesTyped proves the Explore leaderboard typed read decodes the ordered shelf id list.
func TestTopShelves_DecodesTyped(t *testing.T) {
	shelfA, shelfB := uuid.New(), uuid.New()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/explore/top-shelves" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"shelf_ids":["` + shelfA.String() + `","` + shelfB.String() + `"]}`))
	})
	ids, err := c.TopShelves(context.Background(), "tok", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != shelfA || ids[1] != shelfB {
		t.Fatalf("ids = %+v", ids)
	}
}

// TestTypedReads_NonOKIsErrUpstream covers the non-200 branch shared by
// every typed-read method: outside-contract answers are ErrUpstream, never a silent zero value.
func TestTypedReads_NonOKIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	id := uuid.New()
	cases := map[string]func() error{
		"ProfileSummary": func() error { _, err := c.ProfileSummary(context.Background(), "tok", id); return err },
		"ShelvesSummary": func() error {
			_, err := c.ShelvesSummary(context.Background(), "tok", []uuid.UUID{id})
			return err
		},
		"CommentsByIDs": func() error {
			_, err := c.CommentsByIDs(context.Background(), "tok", []uuid.UUID{id})
			return err
		},
		"Feed":       func() error { _, _, err := c.Feed(context.Background(), "tok", "you", nil, 10); return err },
		"TopShelves": func() error { _, err := c.TopShelves(context.Background(), "tok", 10); return err },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrUpstream) {
				t.Fatalf("want ErrUpstream, got %v", err)
			}
		})
	}
}

// TestListComments_ForwardsQueryParams proves cursor/limit land on the
// request's actual query values wire-level (not just substring match), and
// nil/nil omits both params rather than forwarding empty/zero.
func TestListComments_ForwardsQueryParams(t *testing.T) {
	id := uuid.New()
	var gotQuery url.Values
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"comments":[]}`))
	})

	cursor := "cur-9"
	limit := 7
	if _, err := c.ListComments(context.Background(), "tok", id, &cursor, &limit); err != nil {
		t.Fatal(err)
	}
	if got := gotQuery.Get("cursor"); got != cursor {
		t.Fatalf("cursor query = %q, want %q", got, cursor)
	}
	if got := gotQuery.Get("limit"); got != "7" {
		t.Fatalf("limit query = %q, want %q", got, "7")
	}

	if _, err := c.ListComments(context.Background(), "tok", id, nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotQuery.Has("cursor") || gotQuery.Has("limit") {
		t.Fatalf("nil/nil must omit both params, got %v", gotQuery)
	}
}

// TestRecordPublish_Succeeds proves it posts the shelf id as JSON with the
// bearer and treats 204 as success (returns only an error, no Result).
func TestRecordPublish_Succeeds(t *testing.T) {
	shelfID := uuid.New()
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.RecordPublish(context.Background(), "tok", shelfID); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/events/shelf-published" {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("bearer = %q", gotAuth)
	}
	if gotContentType != "application/json" || !strings.Contains(string(gotBody), shelfID.String()) {
		t.Fatalf("contentType=%q body=%s", gotContentType, gotBody)
	}
}

// TestRecordPublish_NonNoContentIsErrUpstream covers RecordPublish's non-204 branch.
func TestRecordPublish_NonNoContentIsErrUpstream(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if err := c.RecordPublish(context.Background(), "tok", uuid.New()); !errors.Is(err, ErrUpstream) {
		t.Fatalf("want ErrUpstream, got %v", err)
	}
}

// TestTransportErrorSurfaces: a dead upstream wraps as a plain error, never
// ErrUpstream (reserved for an upstream that actually answered outside the contract).
func TestTransportErrorSurfaces(t *testing.T) {
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	cases := map[string]func() error{
		"Follow":         func() error { _, err := c.Follow(context.Background(), "tok", id); return err },
		"Unfollow":       func() error { _, err := c.Unfollow(context.Background(), "tok", id); return err },
		"Like":           func() error { _, err := c.Like(context.Background(), "tok", id); return err },
		"Unlike":         func() error { _, err := c.Unlike(context.Background(), "tok", id); return err },
		"ListComments":   func() error { _, err := c.ListComments(context.Background(), "tok", id, nil, nil); return err },
		"CreateComment":  func() error { _, err := c.CreateComment(context.Background(), "tok", id, nil); return err },
		"DeleteComment":  func() error { _, err := c.DeleteComment(context.Background(), "tok", id); return err },
		"PurgeUserData":  func() error { _, err := c.PurgeUserData(context.Background(), "tok"); return err },
		"ProfileSummary": func() error { _, err := c.ProfileSummary(context.Background(), "tok", id); return err },
		"ShelvesSummary": func() error {
			_, err := c.ShelvesSummary(context.Background(), "tok", []uuid.UUID{id})
			return err
		},
		"CommentsByIDs": func() error {
			_, err := c.CommentsByIDs(context.Background(), "tok", []uuid.UUID{id})
			return err
		},
		"Feed":          func() error { _, _, err := c.Feed(context.Background(), "tok", "you", nil, 10); return err },
		"TopShelves":    func() error { _, err := c.TopShelves(context.Background(), "tok", 10); return err },
		"RecordPublish": func() error { return c.RecordPublish(context.Background(), "tok", id) },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("want transport error")
			}
			if errors.Is(err, ErrUpstream) {
				t.Fatal("a transport failure is not ErrUpstream")
			}
		})
	}
}
