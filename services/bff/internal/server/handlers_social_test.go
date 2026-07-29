package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/socialapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/socialclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

// stubSocialFull implements server.SocialAPI via function fields; each
// method panics when its field is unset (function-field, nil-panic -
// the convention stubEnrichment and the newer stubCollection methods
// already use).
type stubSocialFull struct {
	follow         func(ctx context.Context, bearer string, userID uuid.UUID) (socialclient.Result, error)
	unfollow       func(ctx context.Context, bearer string, userID uuid.UUID) (socialclient.Result, error)
	like           func(ctx context.Context, bearer string, shelfID uuid.UUID) (socialclient.Result, error)
	unlike         func(ctx context.Context, bearer string, shelfID uuid.UUID) (socialclient.Result, error)
	listComments   func(ctx context.Context, bearer string, shelfID uuid.UUID, cursor *string, limit *int) (socialclient.Result, error)
	createComment  func(ctx context.Context, bearer string, shelfID uuid.UUID, body []byte) (socialclient.Result, error)
	deleteComment  func(ctx context.Context, bearer string, commentID uuid.UUID) (socialclient.Result, error)
	profileSummary func(ctx context.Context, bearer string, userID uuid.UUID) (socialapi.ProfileSocialSummary, error)
	shelvesSummary func(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.ShelfSocialSummary, error)
	commentsByIDs  func(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.Comment, error)
	feed           func(ctx context.Context, bearer, tab string, cursor *string, limit int) ([]socialapi.ActivityEvent, *string, error)
	topShelves     func(ctx context.Context, bearer string, limit int) ([]uuid.UUID, error)
	recordPublish  func(ctx context.Context, bearer string, shelfID uuid.UUID) error
	purgeUserData  func(ctx context.Context, bearer string) (socialclient.Result, error)
}

var _ SocialAPI = (*stubSocialFull)(nil)

func (s *stubSocialFull) Follow(ctx context.Context, bearer string, userID uuid.UUID) (socialclient.Result, error) {
	if s.follow == nil {
		panic("unexpected social.Follow")
	}
	return s.follow(ctx, bearer, userID)
}

func (s *stubSocialFull) Unfollow(ctx context.Context, bearer string, userID uuid.UUID) (socialclient.Result, error) {
	if s.unfollow == nil {
		panic("unexpected social.Unfollow")
	}
	return s.unfollow(ctx, bearer, userID)
}

func (s *stubSocialFull) Like(ctx context.Context, bearer string, shelfID uuid.UUID) (socialclient.Result, error) {
	if s.like == nil {
		panic("unexpected social.Like")
	}
	return s.like(ctx, bearer, shelfID)
}

func (s *stubSocialFull) Unlike(ctx context.Context, bearer string, shelfID uuid.UUID) (socialclient.Result, error) {
	if s.unlike == nil {
		panic("unexpected social.Unlike")
	}
	return s.unlike(ctx, bearer, shelfID)
}

func (s *stubSocialFull) ListComments(ctx context.Context, bearer string, shelfID uuid.UUID, cursor *string, limit *int) (socialclient.Result, error) {
	if s.listComments == nil {
		panic("unexpected social.ListComments")
	}
	return s.listComments(ctx, bearer, shelfID, cursor, limit)
}

func (s *stubSocialFull) CreateComment(ctx context.Context, bearer string, shelfID uuid.UUID, body []byte) (socialclient.Result, error) {
	if s.createComment == nil {
		panic("unexpected social.CreateComment")
	}
	return s.createComment(ctx, bearer, shelfID, body)
}

func (s *stubSocialFull) DeleteComment(ctx context.Context, bearer string, commentID uuid.UUID) (socialclient.Result, error) {
	if s.deleteComment == nil {
		panic("unexpected social.DeleteComment")
	}
	return s.deleteComment(ctx, bearer, commentID)
}

func (s *stubSocialFull) ProfileSummary(ctx context.Context, bearer string, userID uuid.UUID) (socialapi.ProfileSocialSummary, error) {
	if s.profileSummary == nil {
		panic("unexpected social.ProfileSummary")
	}
	return s.profileSummary(ctx, bearer, userID)
}

func (s *stubSocialFull) ShelvesSummary(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
	if s.shelvesSummary == nil {
		panic("unexpected social.ShelvesSummary")
	}
	return s.shelvesSummary(ctx, bearer, ids)
}

func (s *stubSocialFull) CommentsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]socialapi.Comment, error) {
	if s.commentsByIDs == nil {
		panic("unexpected social.CommentsByIDs")
	}
	return s.commentsByIDs(ctx, bearer, ids)
}

func (s *stubSocialFull) Feed(ctx context.Context, bearer, tab string, cursor *string, limit int) ([]socialapi.ActivityEvent, *string, error) {
	if s.feed == nil {
		panic("unexpected social.Feed")
	}
	return s.feed(ctx, bearer, tab, cursor, limit)
}

func (s *stubSocialFull) TopShelves(ctx context.Context, bearer string, limit int) ([]uuid.UUID, error) {
	if s.topShelves == nil {
		panic("unexpected social.TopShelves")
	}
	return s.topShelves(ctx, bearer, limit)
}

func (s *stubSocialFull) RecordPublish(ctx context.Context, bearer string, shelfID uuid.UUID) error {
	if s.recordPublish == nil {
		panic("unexpected social.RecordPublish")
	}
	return s.recordPublish(ctx, bearer, shelfID)
}

func (s *stubSocialFull) PurgeUserData(ctx context.Context, bearer string) (socialclient.Result, error) {
	if s.purgeUserData == nil {
		panic("unexpected social.PurgeUserData")
	}
	return s.purgeUserData(ctx, bearer)
}

// TestUnitShelfPage_EffectiveVisibility pins effectiveShelf's
// two-sided rule (private beats unlisted beats listed; both the shelf
// and the owner profile must be non-private) and the social-summary
// degrade posture, all through GET /api/shelves/{shelfId}.
func TestUnitShelfPage_EffectiveVisibility(t *testing.T) {
	shelfID, ownerID := uuid.New(), uuid.New()
	shelf := collectionapi.SharedShelf{
		Id: shelfID, Name: "Backlog", Slug: "backlog", OwnerId: ownerID,
		Visibility: "unlisted", Params: map[string]interface{}{},
	}
	listedOwner := userapi.ProfileCard{UserId: ownerID, Handle: "alice", ProfileVisibility: "listed"}
	privateOwner := userapi.ProfileCard{UserId: ownerID, Handle: "alice", ProfileVisibility: "private"}

	setup := func(col *stubCollection, usr *stubUsersFull, soc *stubSocialFull) (*Handlers, *testEnv) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.collection, h.users, h.social = col, usr, soc
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
	}
	path := "/api/shelves/" + shelfID.String()

	t.Run("unlisted shelf, listed owner -> 200 with owner card", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionapi.SharedShelf, error) {
			return shelf, nil
		}}
		usr := &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{listedOwner}, nil
		}}
		soc := &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return []socialapi.ShelfSocialSummary{{ShelfId: shelfID, LikeCount: 3, CommentCount: 1, ViewerLikes: true}}, nil
		}}
		h, env := setup(col, usr, soc)
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		var got struct {
			Owner           struct{ Handle string } `json:"owner"`
			SocialAvailable bool                    `json:"social_available"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Owner.Handle != "alice" || !got.SocialAvailable {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("listed shelf, private owner -> 404 (bff-side half of the rule)", func(t *testing.T) {
		listedShelf := shelf
		listedShelf.Visibility = "listed"
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionapi.SharedShelf, error) {
			return listedShelf, nil
		}}
		usr := &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{privateOwner}, nil
		}}
		h, env := setup(col, usr, &stubSocialFull{})
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("collection 404 -> 404", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionapi.SharedShelf, error) {
			return collectionapi.SharedShelf{}, collectionclient.ErrShelfNotFound
		}}
		h, env := setup(col, &stubUsersFull{}, &stubSocialFull{})
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("social summary error -> 200 degraded, no social block", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionapi.SharedShelf, error) {
			return shelf, nil
		}}
		usr := &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{listedOwner}, nil
		}}
		soc := &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, errors.New("social down")
		}}
		h, env := setup(col, usr, soc)
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (degrade, not fail)", rec.Code)
		}
		if strings.Contains(rec.Body.String(), `"social":`) {
			t.Fatalf("social block must be absent when degraded: %s", rec.Body)
		}
		var got struct {
			SocialAvailable bool `json:"social_available"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.SocialAvailable {
			t.Fatal("social_available should be false")
		}
	})
}

// TestUnitProfilePage_ComposesAndHides pins GET /api/profiles/{handle}:
// the 404 on an unresolvable handle, that the shelf list is scoped to
// exactly the resolved owner, and that a social outage degrades the
// whole social block (profile summary and per-shelf counts alike)
// while still serving the shelf list.
func TestUnitProfilePage_ComposesAndHides(t *testing.T) {
	ownerID := uuid.New()
	owner := userapi.ProfileCard{UserId: ownerID, Handle: "alice", ProfileVisibility: "listed"}
	shelfSummary := collectionapi.SharedShelfSummary{
		Id: uuid.New(), Name: "Backlog", Slug: "backlog", OwnerId: ownerID,
		Visibility: "listed", EntryCount: 4, CoverUrls: []string{},
	}

	setup := func(usr *stubUsersFull, col *stubCollection, soc *stubSocialFull) (*Handlers, *testEnv) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users, h.collection, h.social = usr, col, soc
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
	}
	const path = "/api/profiles/alice"

	t.Run("unknown handle -> 404", func(t *testing.T) {
		usr := &stubUsersFull{sharedProfile: func(context.Context, string, string) (userapi.ProfileCard, error) {
			return userapi.ProfileCard{}, userclient.ErrProfileNotFound
		}}
		h, env := setup(usr, &stubCollection{}, &stubSocialFull{})
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("happy path scopes shelves to exactly the resolved owner", func(t *testing.T) {
		var gotOwnerIDs []uuid.UUID
		usr := &stubUsersFull{sharedProfile: func(context.Context, string, string) (userapi.ProfileCard, error) {
			return owner, nil
		}}
		col := &stubCollection{listSharedShelves: func(_ context.Context, _ string, ownerIDs []uuid.UUID, _, _ int) ([]collectionapi.SharedShelfSummary, int, error) {
			gotOwnerIDs = ownerIDs
			return []collectionapi.SharedShelfSummary{shelfSummary}, 1, nil
		}}
		soc := &stubSocialFull{
			profileSummary: func(context.Context, string, uuid.UUID) (socialapi.ProfileSocialSummary, error) {
				return socialapi.ProfileSocialSummary{FollowerCount: 5, FollowingCount: 2, ViewerFollows: true}, nil
			},
			shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
				return []socialapi.ShelfSocialSummary{{ShelfId: shelfSummary.Id, LikeCount: 2}}, nil
			},
		}
		h, env := setup(usr, col, soc)
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if len(gotOwnerIDs) != 1 || gotOwnerIDs[0] != ownerID {
			t.Fatalf("ListSharedShelves owner_ids = %v, want exactly [%s]", gotOwnerIDs, ownerID)
		}
		var got struct {
			Profile         struct{ Handle string } `json:"profile"`
			SocialAvailable bool                    `json:"social_available"`
			Social          struct {
				FollowerCount int `json:"follower_count"`
			} `json:"social"`
			Shelves    []struct{ Name string } `json:"shelves"`
			TotalCount int                     `json:"total_count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Profile.Handle != "alice" || !got.SocialAvailable || got.Social.FollowerCount != 5 ||
			len(got.Shelves) != 1 || got.Shelves[0].Name != "Backlog" || got.TotalCount != 1 {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("social down -> shelves still present, social_available false", func(t *testing.T) {
		usr := &stubUsersFull{sharedProfile: func(context.Context, string, string) (userapi.ProfileCard, error) {
			return owner, nil
		}}
		col := &stubCollection{listSharedShelves: func(context.Context, string, []uuid.UUID, int, int) ([]collectionapi.SharedShelfSummary, int, error) {
			return []collectionapi.SharedShelfSummary{shelfSummary}, 1, nil
		}}
		// shelvesSummary is deliberately unset: ProfileSummary failing
		// must short-circuit before it is ever called.
		soc := &stubSocialFull{profileSummary: func(context.Context, string, uuid.UUID) (socialapi.ProfileSocialSummary, error) {
			return socialapi.ProfileSocialSummary{}, errors.New("social down")
		}}
		h, env := setup(usr, col, soc)
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (degrade, not fail)", rec.Code)
		}
		var got struct {
			SocialAvailable bool                    `json:"social_available"`
			Shelves         []struct{ Name string } `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.SocialAvailable {
			t.Fatal("social_available should be false")
		}
		if len(got.Shelves) != 1 || got.Shelves[0].Name != "Backlog" {
			t.Fatalf("shelves should still be present: %+v", got.Shelves)
		}
	})
}

// TestUnitSocialRelays_PassProblemsVerbatim pins that a social relay
// carries the upstream status and body through unchanged in both
// directions: a rate-cap problem on a like, and a created comment's
// body (mirrors captureUsers'/TestUpdate_RelaysValidationProblemVerbatim's
// body-verbatim assertions).
func TestUnitSocialRelays_PassProblemsVerbatim(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	t.Run("PUT likes relays a 429 problem verbatim", func(t *testing.T) {
		shelfID := uuid.New()
		const problemBody = `{"type":"about:blank","title":"Too Many Requests","status":429,"code":"cap_exceeded","detail":"like cap reached"}`
		h.social = &stubSocialFull{like: func(context.Context, string, uuid.UUID) (socialclient.Result, error) {
			return socialclient.Result{Status: http.StatusTooManyRequests, ContentType: "application/problem+json", Body: []byte(problemBody)}, nil
		}}
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/social/likes/"+shelfID.String(), "")
		if rec.Code != http.StatusTooManyRequests || rec.Body.String() != problemBody {
			t.Fatalf("status=%d body=%s, want 429 verbatim %s", rec.Code, rec.Body, problemBody)
		}
	})

	t.Run("POST comments relays a 201 body verbatim", func(t *testing.T) {
		shelfID, ownerID := uuid.New(), uuid.New()
		const commentBody = `{"id":"11111111-1111-1111-1111-111111111111","shelf_id":"22222222-2222-2222-2222-222222222222",` +
			`"author_id":"33333333-3333-3333-3333-333333333333","body":"nice shelf","created_at":"2026-01-01T00:00:00Z"}`
		h.collection = &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionapi.SharedShelf, error) {
			return collectionapi.SharedShelf{
				Id: shelfID, Name: "Backlog", Slug: "backlog", OwnerId: ownerID,
				Visibility: "listed", Params: map[string]interface{}{},
			}, nil
		}}
		h.users = &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{{UserId: ownerID, Handle: "alice", ProfileVisibility: "listed"}}, nil
		}}
		h.social = &stubSocialFull{createComment: func(context.Context, string, uuid.UUID, []byte) (socialclient.Result, error) {
			return socialclient.Result{Status: http.StatusCreated, ContentType: "application/json", Body: []byte(commentBody)}, nil
		}}
		rec := doAuthedBody(t, h, env, http.MethodPost, "/api/shelves/"+shelfID.String()+"/comments", `{"body":"nice shelf"}`)
		if rec.Code != http.StatusCreated || rec.Body.String() != commentBody {
			t.Fatalf("status=%d body=%s, want 201 verbatim %s", rec.Code, rec.Body, commentBody)
		}
	})
}

// mockComment builds a social comment row for a stubbed ListComments
// body. AuthorId is a pointer purely for fixture-building so a nil
// marshals to the JSON literal null - the purge-anonymized shape
// composeCommentsPage must tolerate - unlike production's rawComment,
// which decodes that same null into uuid.Nil.
type mockComment struct {
	Id        uuid.UUID  `json:"id"`
	ShelfId   uuid.UUID  `json:"shelf_id"`
	AuthorId  *uuid.UUID `json:"author_id"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
}

func mockCommentsBody(t *testing.T, comments []mockComment, nextCursor *string) []byte {
	t.Helper()
	b, err := json.Marshal(struct {
		Comments   []mockComment `json:"comments"`
		NextCursor *string       `json:"next_cursor,omitempty"`
	}{Comments: comments, NextCursor: nextCursor})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// commentDTO is the subset of a composed Comment this file's tests
// decode.
type commentDTO struct {
	Id       uuid.UUID `json:"id"`
	AuthorId uuid.UUID `json:"author_id"`
	Body     string    `json:"body"`
	Author   *struct {
		Handle string `json:"handle"`
	} `json:"author,omitempty"`
}

type commentListDTO struct {
	Comments   []commentDTO `json:"comments"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

// TestUnitShelfComments_AuthorHydration pins ListShelfComments'
// composition: each live comment's author_id is hydrated into a
// ProfileCard batched in ONE deduplicated SharedCardsByIDs call, a
// purge-anonymized (author_id null) row is excluded from that batch
// and never carries author, a card-fetch failure fails open (the page
// still serves, sans authors), and cursor/limit still pass straight
// through to social.
func TestUnitShelfComments_AuthorHydration(t *testing.T) {
	shelfID, ownerID := uuid.New(), uuid.New()
	aliceID, bobID := uuid.New(), uuid.New()
	commentA, commentB, commentC, commentD := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	created := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	shelf := collectionapi.SharedShelf{
		Id: shelfID, Name: "Backlog", Slug: "backlog", OwnerId: ownerID,
		Visibility: "listed", Params: map[string]interface{}{},
	}
	owner := userapi.ProfileCard{UserId: ownerID, Handle: "owner", ProfileVisibility: "listed"}
	alice := userapi.ProfileCard{UserId: aliceID, Handle: "alice", ProfileVisibility: "listed"}
	bob := userapi.ProfileCard{UserId: bobID, Handle: "bob", ProfileVisibility: "listed"}

	// A and B are both authored by alice (dedup), C by bob (right card
	// attached to the right comment, not swapped), D is anonymized
	// (author_id null).
	rows := []mockComment{
		{Id: commentA, ShelfId: shelfID, AuthorId: &aliceID, Body: "first", CreatedAt: created},
		{Id: commentB, ShelfId: shelfID, AuthorId: &aliceID, Body: "second", CreatedAt: created},
		{Id: commentC, ShelfId: shelfID, AuthorId: &bobID, Body: "third", CreatedAt: created},
		{Id: commentD, ShelfId: shelfID, AuthorId: nil, Body: "anonymized", CreatedAt: created},
	}

	setup := func(usr *stubUsersFull, soc *stubSocialFull) (*Handlers, *testEnv) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.collection = &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionapi.SharedShelf, error) {
			return shelf, nil
		}}
		h.users, h.social = usr, soc
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
	}
	path := "/api/shelves/" + shelfID.String() + "/comments?cursor=abc&limit=3"

	t.Run("attaches the right card per author, deduplicated, anonymized excluded, cursor forwarded", func(t *testing.T) {
		var gotCursor *string
		var gotLimit *int
		var authorBatchCalls [][]uuid.UUID
		usr := &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
			authorBatchCalls = append(authorBatchCalls, ids)
			byID := map[uuid.UUID]userapi.ProfileCard{ownerID: owner, aliceID: alice, bobID: bob}
			out := make([]userapi.ProfileCard, 0, len(ids))
			for _, id := range ids {
				if c, ok := byID[id]; ok {
					out = append(out, c)
				}
			}
			return out, nil
		}}
		next := "cur2"
		soc := &stubSocialFull{listComments: func(_ context.Context, _ string, gotShelf uuid.UUID, cursor *string, limit *int) (socialclient.Result, error) {
			if gotShelf != shelfID {
				t.Fatalf("shelf id = %s, want %s", gotShelf, shelfID)
			}
			gotCursor, gotLimit = cursor, limit
			return socialclient.Result{Status: http.StatusOK, ContentType: "application/json",
				Body: mockCommentsBody(t, rows, &next)}, nil
		}}
		h, env := setup(usr, soc)
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if gotCursor == nil || *gotCursor != "abc" {
			t.Fatalf("cursor forwarded = %v, want abc", gotCursor)
		}
		if gotLimit == nil || *gotLimit != 3 {
			t.Fatalf("limit forwarded = %v, want 3", gotLimit)
		}

		var got commentListDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.NextCursor == nil || *got.NextCursor != next {
			t.Fatalf("next_cursor = %v, want %s", got.NextCursor, next)
		}
		if len(got.Comments) != 4 {
			t.Fatalf("comments = %d, want 4", len(got.Comments))
		}
		byID := map[uuid.UUID]commentDTO{}
		for _, c := range got.Comments {
			byID[c.Id] = c
		}
		if byID[commentA].Author == nil || byID[commentA].Author.Handle != "alice" {
			t.Fatalf("comment A author = %+v, want alice", byID[commentA].Author)
		}
		if byID[commentB].Author == nil || byID[commentB].Author.Handle != "alice" {
			t.Fatalf("comment B author = %+v, want alice", byID[commentB].Author)
		}
		if byID[commentC].Author == nil || byID[commentC].Author.Handle != "bob" {
			t.Fatalf("comment C author = %+v, want bob", byID[commentC].Author)
		}
		if byID[commentD].Author != nil {
			t.Fatalf("comment D (anonymized) author = %+v, want absent", byID[commentD].Author)
		}
		// Pin the wire author_id field itself (not just the hydrated
		// Author card, which is keyed off it and so would pass even if
		// the id on the wire were swapped or wrong): A and B carry
		// alice's real seeded uuid, C carries bob's, both literally -
		// and only D, the anonymized row, decodes to the zero value.
		if byID[commentA].AuthorId != aliceID {
			t.Fatalf("comment A author_id = %s, want %s (alice)", byID[commentA].AuthorId, aliceID)
		}
		if byID[commentB].AuthorId != aliceID {
			t.Fatalf("comment B author_id = %s, want %s (alice)", byID[commentB].AuthorId, aliceID)
		}
		if byID[commentC].AuthorId != bobID {
			t.Fatalf("comment C author_id = %s, want %s (bob)", byID[commentC].AuthorId, bobID)
		}
		if byID[commentD].AuthorId != uuid.Nil {
			t.Fatalf("comment D (anonymized) author_id = %s, want the zero value (wire null)", byID[commentD].AuthorId)
		}
		// Wire-truth check: commentDTO.AuthorId (uuid.UUID, non-pointer)
		// would silently decode a JSON null into uuid.Nil above without
		// erroring, so the byID assertions alone cannot prove the
		// response actually put null on the wire for comment D rather
		// than, say, a zeroed uuid. Assert the literal bytes instead
		// (same idiom as the fail-open subtest's rec.Body string check
		// below): api.Comment.AuthorId is required+nullable, so the key
		// must always be present and null for this one row.
		if !strings.Contains(rec.Body.String(), `"author_id":null`) {
			t.Fatalf("response body missing literal author_id null for the anonymized comment: %s", rec.Body)
		}

		if len(authorBatchCalls) != 2 {
			t.Fatalf("SharedCardsByIDs calls = %d, want 2 (owner lookup, then author batch)", len(authorBatchCalls))
		}
		batch := authorBatchCalls[1]
		if len(batch) != 2 {
			t.Fatalf("author batch = %v, want exactly 2 deduplicated ids", batch)
		}
		seen := map[uuid.UUID]bool{}
		for _, id := range batch {
			if seen[id] {
				t.Fatalf("author batch %v carries a duplicate", batch)
			}
			seen[id] = true
		}
		if !seen[aliceID] || !seen[bobID] {
			t.Fatalf("author batch %v, want alice and bob, not the anonymized row", batch)
		}
	})

	t.Run("card-fetch failure fails open: 200 without authors, event logged", func(t *testing.T) {
		usr := &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
			// The first call (effectiveShelf's owner lookup) still
			// succeeds; only the comment-author batch (requesting
			// alice alone) fails, so the page must still serve.
			if len(ids) == 1 && ids[0] == ownerID {
				return []userapi.ProfileCard{owner}, nil
			}
			return nil, errors.New("user service down")
		}}
		soc := &stubSocialFull{listComments: func(context.Context, string, uuid.UUID, *string, *int) (socialclient.Result, error) {
			return socialclient.Result{Status: http.StatusOK, ContentType: "application/json",
				Body: mockCommentsBody(t, rows[:1], nil)}, nil
		}}
		h, env := setup(usr, soc)
		var logBuf bytes.Buffer
		h.logger = slog.New(slog.NewTextHandler(&logBuf, nil))
		rec := doAuthed(t, h, env, http.MethodGet, "/api/shelves/"+shelfID.String()+"/comments")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (degrade, not fail), body = %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), `"author":`) {
			t.Fatalf("author must be absent when the card batch fails open: %s", rec.Body)
		}
		line := logBuf.String()
		if !strings.Contains(line, "dependency unavailable; failing open") || !strings.Contains(line, "op=comment_authors") {
			t.Fatalf("fail-open log missing op=comment_authors: %q", line)
		}
	})

	t.Run("social's own 400 on a malformed cursor still relays verbatim", func(t *testing.T) {
		const problemBody = `{"type":"about:blank","title":"Bad Request","status":400,"code":"invalid_param","detail":"malformed cursor"}`
		soc := &stubSocialFull{listComments: func(context.Context, string, uuid.UUID, *string, *int) (socialclient.Result, error) {
			return socialclient.Result{Status: http.StatusBadRequest, ContentType: "application/problem+json", Body: []byte(problemBody)}, nil
		}}
		usr := &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{owner}, nil
		}}
		h, env := setup(usr, soc)
		rec := doAuthed(t, h, env, http.MethodGet, "/api/shelves/"+shelfID.String()+"/comments?cursor=garbage")
		if rec.Code != http.StatusBadRequest || rec.Body.String() != problemBody {
			t.Fatalf("status=%d body=%s, want 400 verbatim %s", rec.Code, rec.Body, problemBody)
		}
	})
}

// feedItemDTO is the subset of a FeedItem this file's tests decode.
type feedItemDTO struct {
	Id    uuid.UUID `json:"id"`
	Verb  string    `json:"verb"`
	Actor struct {
		Handle string `json:"handle"`
	} `json:"actor"`
	Shelf *struct {
		Id uuid.UUID `json:"id"`
	} `json:"shelf,omitempty"`
	FollowedUser *struct {
		Handle string `json:"handle"`
	} `json:"followed_user,omitempty"`
	CommentExcerpt *string `json:"comment_excerpt,omitempty"`
}

type feedPageDTO struct {
	Items      []feedItemDTO `json:"items"`
	NextCursor *string       `json:"next_cursor,omitempty"`
}

// TestUnitFeed_FillLoopAndGating pins the fill loop's page-full and
// no-next-cursor termination conditions (feedFillRounds exhaustion is
// covered separately by inspection: the loop's `for` guard is the
// same three-way race for every round) together with the tab-driven
// gating rule: actions are signed (actor cards always attach) while
// objects are gated (tab=following drops a row naming a non-listed
// shelf or a non-listed followee; tab=you keeps every row because the
// objects are the caller's own).
func TestUnitFeed_FillLoopAndGating(t *testing.T) {
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	actor1, actor2, actor3, actor4 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	owner1, owner2, owner4 := uuid.New(), uuid.New(), uuid.New()
	shelf1, shelf2, shelf4 := uuid.New(), uuid.New(), uuid.New()
	followee3 := uuid.New()

	event1 := socialapi.ActivityEvent{ // listed shelf, listed owner: survives tab=following
		Id: uuid.New(), ActorId: actor1, Verb: socialapi.LikedShelf,
		ObjectShelfId: &shelf1, TargetUserId: owner1, CreatedAt: base,
	}
	event2 := socialapi.ActivityEvent{ // unlisted shelf (listed owner): dropped on tab=following
		Id: uuid.New(), ActorId: actor2, Verb: socialapi.LikedShelf,
		ObjectShelfId: &shelf2, TargetUserId: owner2, CreatedAt: base.Add(time.Second),
	}
	event3 := socialapi.ActivityEvent{ // followed_user, unlisted followee: dropped on tab=following
		Id: uuid.New(), ActorId: actor3, Verb: socialapi.FollowedUser,
		TargetUserId: followee3, CreatedAt: base.Add(2 * time.Second),
	}
	event4 := socialapi.ActivityEvent{ // round 2's sole (surviving) event
		Id: uuid.New(), ActorId: actor4, Verb: socialapi.LikedShelf,
		ObjectShelfId: &shelf4, TargetUserId: owner4, CreatedAt: base.Add(3 * time.Second),
	}

	cards := map[uuid.UUID]userapi.ProfileCard{
		actor1:    {UserId: actor1, Handle: "actor1", ProfileVisibility: "listed"},
		actor2:    {UserId: actor2, Handle: "actor2", ProfileVisibility: "listed"},
		actor3:    {UserId: actor3, Handle: "actor3", ProfileVisibility: "listed"},
		actor4:    {UserId: actor4, Handle: "actor4", ProfileVisibility: "listed"},
		owner1:    {UserId: owner1, Handle: "owner1", ProfileVisibility: "listed"},
		owner2:    {UserId: owner2, Handle: "owner2", ProfileVisibility: "listed"},
		owner4:    {UserId: owner4, Handle: "owner4", ProfileVisibility: "listed"},
		followee3: {UserId: followee3, Handle: "followee3", ProfileVisibility: "unlisted"},
	}
	shelves := map[uuid.UUID]collectionapi.SharedShelfSummary{
		shelf1: {Id: shelf1, Name: "Shelf1", Slug: "shelf1", OwnerId: owner1, Visibility: "listed", CoverUrls: []string{}},
		shelf2: {Id: shelf2, Name: "Shelf2", Slug: "shelf2", OwnerId: owner2, Visibility: "unlisted", CoverUrls: []string{}},
		shelf4: {Id: shelf4, Name: "Shelf4", Slug: "shelf4", OwnerId: owner4, Visibility: "listed", CoverUrls: []string{}},
	}

	newUsers := func() *stubUsersFull {
		return &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
			out := make([]userapi.ProfileCard, 0, len(ids))
			for _, id := range ids {
				if c, ok := cards[id]; ok {
					out = append(out, c)
				}
			}
			return out, nil
		}}
	}
	newCollection := func() *stubCollection {
		return &stubCollection{sharedShelvesByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
			out := make([]collectionapi.SharedShelfSummary, 0, len(ids))
			for _, id := range ids {
				if s, ok := shelves[id]; ok {
					out = append(out, s)
				}
			}
			return out, nil
		}}
	}
	setup := func(h *Handlers) *testEnv {
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		return &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
	}

	t.Run("tab=following: fill loop refills to page-full, gates the unlisted shelf and unlisted followee", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users, h.collection = newUsers(), newCollection()
		var rounds int
		h.social = &stubSocialFull{
			feed: func(_ context.Context, _, tab string, cursor *string, _ int) ([]socialapi.ActivityEvent, *string, error) {
				rounds++
				if tab != "following" {
					t.Fatalf("tab = %q, want following", tab)
				}
				switch rounds {
				case 1:
					if cursor != nil {
						t.Fatalf("round 1 cursor = %v, want nil", cursor)
					}
					c := "c2"
					return []socialapi.ActivityEvent{event1, event2, event3}, &c, nil
				case 2:
					if cursor == nil || *cursor != "c2" {
						t.Fatalf("round 2 cursor = %v, want c2", cursor)
					}
					return []socialapi.ActivityEvent{event4}, nil, nil
				default:
					t.Fatalf("social.Feed called a 3rd time; the loop should have stopped at page-full")
					return nil, nil, nil
				}
			},
			shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
				return nil, nil
			},
		}
		env := setup(h)

		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=2")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if rounds != 2 {
			t.Fatalf("social.Feed called %d times, want exactly 2", rounds)
		}
		var got feedPageDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 2 || got.Items[0].Id != event1.Id || got.Items[1].Id != event4.Id {
			t.Fatalf("items = %+v, want exactly [event1, event4] in order", got.Items)
		}
		wantCursor := rawCursorOf(event4)
		if got.NextCursor == nil || *got.NextCursor != wantCursor {
			t.Fatalf("next_cursor = %v, want %q (raw cursor of the last INCLUDED event)", got.NextCursor, wantCursor)
		}
	})

	t.Run("tab=you: every row survives (own-shelf rule), actor cards still attach", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users, h.collection = newUsers(), newCollection()
		h.social = &stubSocialFull{
			feed: func(_ context.Context, _, tab string, _ *string, _ int) ([]socialapi.ActivityEvent, *string, error) {
				if tab != "you" {
					t.Fatalf("tab = %q, want you", tab)
				}
				return []socialapi.ActivityEvent{event1, event2, event3}, nil, nil
			},
			shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
				return nil, nil
			},
		}
		env := setup(h)

		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=you&limit=20")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		var got feedPageDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 3 {
			t.Fatalf("items = %d, want 3 (every row survives on tab=you): %+v", len(got.Items), got.Items)
		}
		if got.Items[1].Shelf == nil || got.Items[1].Shelf.Id != shelf2 {
			t.Fatalf("the unlisted-shelf like should survive with its shelf attached: %+v", got.Items[1])
		}
		wantHandles := []string{"actor1", "actor2", "actor3"}
		for i, want := range wantHandles {
			if got.Items[i].Actor.Handle != want {
				t.Fatalf("items[%d].actor.handle = %q, want %q (actor cards always attach)", i, got.Items[i].Actor.Handle, want)
			}
		}
	})

	t.Run("tab=following: an unfillable page stops at feedFillRounds, not page-full or exhaustion", func(t *testing.T) {
		// Every round hands back one dropped (unlisted-shelf) event plus
		// a non-nil next_cursor - the stream never reports exhausted and
		// items never reach the limit, so only the round cap can stop
		// the loop.
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users, h.collection = newUsers(), newCollection()
		var rounds int
		var lastCursor string
		h.social = &stubSocialFull{
			feed: func(_ context.Context, _, tab string, _ *string, _ int) ([]socialapi.ActivityEvent, *string, error) {
				rounds++
				if rounds > feedFillRounds {
					t.Fatalf("social.Feed called a %dth time; feedFillRounds=%d must cap it", rounds, feedFillRounds)
				}
				e := event2 // unlisted shelf: always dropped on tab=following
				e.Id = uuid.New()
				lastCursor = "more-" + e.Id.String()
				c := lastCursor
				return []socialapi.ActivityEvent{e}, &c, nil
			},
			shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
				return nil, nil
			},
		}
		env := setup(h)

		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=10")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if rounds != feedFillRounds {
			t.Fatalf("social.Feed called %d times, want exactly feedFillRounds=%d", rounds, feedFillRounds)
		}
		var got feedPageDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 0 {
			t.Fatalf("items = %+v, want none (every round's row was gated out)", got.Items)
		}
		// The raw stream is not exhausted (every round still returned a
		// next_cursor) - a short page from hitting the round cap must
		// still carry a cursor so a follow-up request resumes the fill
		// instead of being told the feed is over.
		if got.NextCursor == nil || *got.NextCursor != lastCursor {
			t.Fatalf("next_cursor = %v, want %q (round %d's own raw cursor)", got.NextCursor, lastCursor, feedFillRounds)
		}
	})
}

// TestUnitFeed_UpstreamErrorsAreHard502s pins that GetFeed never
// degrades quietly the way a shelf/profile page's social block does:
// a raw social.Feed failure and a hydrateFeed batch failure (any of
// its four lookups) both answer 502, because the failed lookup is
// itself gating-relevant - fail-open here would risk naming a gated
// object.
func TestUnitFeed_UpstreamErrorsAreHard502s(t *testing.T) {
	actorID, shelfID, ownerID := uuid.New(), uuid.New(), uuid.New()
	event := socialapi.ActivityEvent{
		Id: uuid.New(), ActorId: actorID, Verb: socialapi.LikedShelf,
		ObjectShelfId: &shelfID, TargetUserId: ownerID, CreatedAt: time.Now(),
	}

	t.Run("social.Feed failure", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.social = &stubSocialFull{feed: func(context.Context, string, string, *string, int) ([]socialapi.ActivityEvent, *string, error) {
			return nil, nil, errors.New("social down")
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("hydrateFeed batch failure (collection down)", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.social = &stubSocialFull{feed: func(context.Context, string, string, *string, int) ([]socialapi.ActivityEvent, *string, error) {
			return []socialapi.ActivityEvent{event}, nil, nil
		}}
		h.collection = &stubCollection{sharedShelvesByIDs: func(context.Context, string, []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
			return nil, errors.New("collection down")
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (no partial/degraded feed page)", rec.Code)
		}
	})
}

// TestUnitFeed_ValidatesTabAndCursor pins that GetFeed rejects a bad
// tab or a malformed cursor with 400 invalid_param before social is
// ever called: socialclient.Feed collapses every non-200 (including
// social's own 400 on either input) into one generic error, so
// unvalidated bad input would otherwise surface as a misleading 502
// instead of the contract's documented 400. Every bad-input subtest
// leaves h.social's feed field unset, so an unwanted call panics
// loudly rather than passing silently; the last subtest proves a
// well-formed cursor still reaches social.Feed unchanged.
func TestUnitFeed_ValidatesTabAndCursor(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	assertInvalidParam := func(t *testing.T, path string) {
		t.Helper()
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
		}
		var p struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_param" {
			t.Fatalf("problem code = %+v, err = %v, want invalid_param", p, err)
		}
	}

	t.Run("bad tab -> 400", func(t *testing.T) {
		h.social = &stubSocialFull{}
		assertInvalidParam(t, "/api/feed?tab=bogus")
	})

	// uuids contain dashes, never dots, so splitting a cursor on its
	// FIRST dot can never mistake a uuid's own hyphens for a second
	// field - these cases must all fail for other reasons (no dot at
	// all, an empty half, or a non-uuid right half).
	badCursors := []string{"abc", "123.", ".uuid", "1.2.not-a-uuid"}
	for _, c := range badCursors {
		t.Run("garbage cursor "+c+" -> 400", func(t *testing.T) {
			h.social = &stubSocialFull{}
			assertInvalidParam(t, "/api/feed?tab=following&cursor="+c)
		})
	}

	t.Run("valid cursor is forwarded to social.Feed verbatim", func(t *testing.T) {
		valid := rawCursorOf(socialapi.ActivityEvent{Id: uuid.New(), CreatedAt: time.Now()})
		var gotCursor *string
		h.social = &stubSocialFull{feed: func(_ context.Context, _, _ string, cursor *string, _ int) ([]socialapi.ActivityEvent, *string, error) {
			gotCursor = cursor
			return nil, nil, nil
		}}
		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&cursor="+valid)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body)
		}
		if gotCursor == nil || *gotCursor != valid {
			t.Fatalf("social.Feed cursor = %v, want %q forwarded unchanged", gotCursor, valid)
		}
	})
}

// TestUnitFeed_ValidatesLimitMinimum pins that GetFeed rejects a
// sub-minimum limit with 400 invalid_param before social is ever
// called - api/bff.yaml declares minimum: 1 on feed's limit, but the
// handler used to clamp only the maximum, so limit=0 passed straight
// through. limit=1 (the minimum) must still be accepted and reach
// social.Feed unchanged.
func TestUnitFeed_ValidatesLimitMinimum(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	t.Run("limit=0 -> 400", func(t *testing.T) {
		h.social = &stubSocialFull{}
		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=0")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
		}
		var p struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_param" {
			t.Fatalf("problem code = %+v, err = %v, want invalid_param", p, err)
		}
	})

	t.Run("limit=1 (the minimum) is accepted and reaches social.Feed", func(t *testing.T) {
		var gotLimit int
		h.social = &stubSocialFull{feed: func(_ context.Context, _, _ string, _ *string, limit int) ([]socialapi.ActivityEvent, *string, error) {
			gotLimit = limit
			return nil, nil, nil
		}}
		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=1")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body)
		}
		if gotLimit != 1 {
			t.Fatalf("social.Feed limit = %d, want 1", gotLimit)
		}
	})
}

// TestUnitFeed_DropsListedShelfWithNonListedOwner pins the other half
// of the following-tab shelf gate (TestUnitFeed_FillLoopAndGating
// covers the shelf-not-listed half): a listed shelf whose owner is
// not listed is still gated out - the shelf's own visibility is
// necessary but not sufficient.
func TestUnitFeed_DropsListedShelfWithNonListedOwner(t *testing.T) {
	actorID, ownerID, shelfID := uuid.New(), uuid.New(), uuid.New()
	event := socialapi.ActivityEvent{
		Id: uuid.New(), ActorId: actorID, Verb: socialapi.LikedShelf,
		ObjectShelfId: &shelfID, TargetUserId: ownerID, CreatedAt: time.Now(),
	}
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.users = &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
		return []userapi.ProfileCard{
			{UserId: actorID, Handle: "actor", ProfileVisibility: "listed"},
			{UserId: ownerID, Handle: "owner", ProfileVisibility: "unlisted"},
		}, nil
	}}
	h.collection = &stubCollection{sharedShelvesByIDs: func(context.Context, string, []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
		return []collectionapi.SharedShelfSummary{
			{Id: shelfID, Name: "Backlog", Slug: "backlog", OwnerId: ownerID, Visibility: "listed", CoverUrls: []string{}},
		}, nil
	}}
	h.social = &stubSocialFull{
		feed: func(context.Context, string, string, *string, int) ([]socialapi.ActivityEvent, *string, error) {
			return []socialapi.ActivityEvent{event}, nil, nil
		},
		shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		},
	}
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=20")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var got feedPageDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("items = %+v, want none (listed shelf but non-listed owner must still be gated out)", got.Items)
	}
}

// TestUnitFeed_FollowedUserCard pins that a followed_user event's
// FeedItem carries the followee's card: the card is already
// batch-resolved for the tab=following gate (cardByID[e.TargetUserId])
// and must not be discarded once the gate clears it, and tab=you must
// carry it too since the map is populated for every followed_user
// event regardless of tab.
func TestUnitFeed_FollowedUserCard(t *testing.T) {
	actorID, followeeID := uuid.New(), uuid.New()
	event := socialapi.ActivityEvent{
		Id: uuid.New(), ActorId: actorID, Verb: socialapi.FollowedUser,
		TargetUserId: followeeID, CreatedAt: time.Now(),
	}
	newUsers := func() *stubUsersFull {
		return &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{
				{UserId: actorID, Handle: "actor", ProfileVisibility: "listed"},
				{UserId: followeeID, Handle: "followee", ProfileVisibility: "listed"},
			}, nil
		}}
	}
	setup := func(h *Handlers) *testEnv {
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		return &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
	}
	newSocial := func() *stubSocialFull {
		return &stubSocialFull{feed: func(context.Context, string, string, *string, int) ([]socialapi.ActivityEvent, *string, error) {
			return []socialapi.ActivityEvent{event}, nil, nil
		}}
	}

	t.Run("tab=following: listed followee survives and carries the followee card", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users, h.social = newUsers(), newSocial()
		env := setup(h)

		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=20")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		var got feedPageDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 {
			t.Fatalf("items = %+v, want exactly 1 (listed followee survives)", got.Items)
		}
		if got.Items[0].FollowedUser == nil || got.Items[0].FollowedUser.Handle != "followee" {
			t.Fatalf("followed_user = %+v, want the followee's card", got.Items[0].FollowedUser)
		}
	})

	t.Run("tab=you: follow row carries the followee card too", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users, h.social = newUsers(), newSocial()
		env := setup(h)

		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=you&limit=20")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		var got feedPageDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 {
			t.Fatalf("items = %+v, want exactly 1", got.Items)
		}
		if got.Items[0].FollowedUser == nil || got.Items[0].FollowedUser.Handle != "followee" {
			t.Fatalf("followed_user = %+v, want the followee's card", got.Items[0].FollowedUser)
		}
	})
}

// TestUnitFeed_ActorCardAttachesAtAnyVisibility pins that an event's
// actor card attaches regardless of the actor's own
// profile_visibility: SharedCardsByIDs is a plain batch lookup with
// no visibility filter of its own (unlike the tab=following object
// gate, which does filter on visibility), so an unlisted or private
// actor must still be named for their own action.
func TestUnitFeed_ActorCardAttachesAtAnyVisibility(t *testing.T) {
	t.Run("private actor liking a listed shelf: row survives, actor card still attached", func(t *testing.T) {
		actorID, ownerID, shelfID := uuid.New(), uuid.New(), uuid.New()
		event := socialapi.ActivityEvent{
			Id: uuid.New(), ActorId: actorID, Verb: socialapi.LikedShelf,
			ObjectShelfId: &shelfID, TargetUserId: ownerID, CreatedAt: time.Now(),
		}
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{
				{UserId: actorID, Handle: "actor", ProfileVisibility: "private"},
				{UserId: ownerID, Handle: "owner", ProfileVisibility: "listed"},
			}, nil
		}}
		h.collection = &stubCollection{sharedShelvesByIDs: func(context.Context, string, []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
			return []collectionapi.SharedShelfSummary{
				{Id: shelfID, Name: "Backlog", Slug: "backlog", OwnerId: ownerID, Visibility: "listed", CoverUrls: []string{}},
			}, nil
		}}
		h.social = &stubSocialFull{
			feed: func(context.Context, string, string, *string, int) ([]socialapi.ActivityEvent, *string, error) {
				return []socialapi.ActivityEvent{event}, nil, nil
			},
			shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
				return nil, nil
			},
		}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=20")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		var got feedPageDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 || got.Items[0].Actor.Handle != "actor" {
			t.Fatalf("items = %+v, want the private actor's card still attached", got.Items)
		}
	})
}

// TestUnitFeed_CommentExcerpts pins that a commented_shelf event's
// object_comment_id drives one CommentsByIDs lookup and that the
// surviving item's comment_excerpt is the live comment's body.
func TestUnitFeed_CommentExcerpts(t *testing.T) {
	actorID, ownerID := uuid.New(), uuid.New()
	shelfID, commentID := uuid.New(), uuid.New()
	event := socialapi.ActivityEvent{
		Id: uuid.New(), ActorId: actorID, Verb: socialapi.CommentedShelf,
		ObjectShelfId: &shelfID, ObjectCommentId: &commentID, TargetUserId: ownerID,
		CreatedAt: time.Now(),
	}
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.users = &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
		return []userapi.ProfileCard{
			{UserId: actorID, Handle: "actor", ProfileVisibility: "listed"},
			{UserId: ownerID, Handle: "owner", ProfileVisibility: "listed"},
		}, nil
	}}
	h.collection = &stubCollection{sharedShelvesByIDs: func(context.Context, string, []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
		return []collectionapi.SharedShelfSummary{
			{Id: shelfID, Name: "Backlog", Slug: "backlog", OwnerId: ownerID, Visibility: "listed", CoverUrls: []string{}},
		}, nil
	}}
	var gotCommentIDs []uuid.UUID
	h.social = &stubSocialFull{
		feed: func(context.Context, string, string, *string, int) ([]socialapi.ActivityEvent, *string, error) {
			return []socialapi.ActivityEvent{event}, nil, nil
		},
		shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		},
		commentsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]socialapi.Comment, error) {
			gotCommentIDs = ids
			return []socialapi.Comment{{Id: commentID, ShelfId: shelfID, AuthorId: actorID, Body: "nice shelf", CreatedAt: time.Now()}}, nil
		},
	}
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	rec := doAuthed(t, h, env, http.MethodGet, "/api/feed?tab=following&limit=20")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(gotCommentIDs) != 1 || gotCommentIDs[0] != commentID {
		t.Fatalf("social.CommentsByIDs called with %v, want exactly [%s]", gotCommentIDs, commentID)
	}
	var got feedPageDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].CommentExcerpt == nil || *got.Items[0].CommentExcerpt != "nice shelf" {
		t.Fatalf("got items = %+v", got.Items)
	}
}

// TestUnitExplore_RecentAndTop covers both Explore surfaces: recent's
// page collection (unfiltered) -> gate owners -> fill -> hydrate
// counts pipeline (happy path, owner-gate drops, the fill loop's
// refill and round cap, next_offset arithmetic, exhaustion, and a
// social outage's fail-open), and top's leaderboard-order-preserving
// drop of a non-listed shelf and a non-listed owner.
func TestUnitExplore_RecentAndTop(t *testing.T) {
	t.Run("recent: pages collection unfiltered, gates listed owners, counts hydrate", func(t *testing.T) {
		id1, id2 := uuid.New(), uuid.New()
		shelfA := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "A", Slug: "a", OwnerId: id1, Visibility: "listed", CoverUrls: []string{}}
		shelfB := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "B", Slug: "b", OwnerId: id2, Visibility: "listed", CoverUrls: []string{}}

		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{
			sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
				byID := map[uuid.UUID]userapi.ProfileCard{
					id1: {UserId: id1, Handle: "alice", ProfileVisibility: "listed"},
					id2: {UserId: id2, Handle: "bob", ProfileVisibility: "listed"},
				}
				out := make([]userapi.ProfileCard, 0, len(ids))
				for _, id := range ids {
					if c, ok := byID[id]; ok {
						out = append(out, c)
					}
				}
				return out, nil
			},
		}
		var calls int
		var ownerIDsArgNil bool
		var gotLimit, gotOffset int
		h.collection = &stubCollection{listSharedShelves: func(_ context.Context, _ string, ownerIDs []uuid.UUID, limit, offset int) ([]collectionapi.SharedShelfSummary, int, error) {
			calls++
			ownerIDsArgNil, gotLimit, gotOffset = len(ownerIDs) == 0, limit, offset
			// total=12: offset(10)+consumed(2)=12 too, so this page is the
			// exact tail end - the exhaustion (not round-cap or limit-fill)
			// path, pinned separately below.
			return []collectionapi.SharedShelfSummary{shelfA, shelfB}, 12, nil
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return []socialapi.ShelfSocialSummary{{ShelfId: shelfA.Id, LikeCount: 3}, {ShelfId: shelfB.Id, LikeCount: 1}}, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent&limit=5&offset=10")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if calls != 1 {
			t.Fatalf("collection.ListSharedShelves called %d times, want exactly 1", calls)
		}
		if !ownerIDsArgNil {
			t.Fatal("owner_ids must not be forwarded - Explore-recent pages collection unfiltered")
		}
		if gotLimit != 5 || gotOffset != 10 {
			t.Fatalf("limit/offset = %d/%d, want 5/10 (passthrough)", gotLimit, gotOffset)
		}
		var got struct {
			TotalCount *int `json:"total_count"`
			NextOffset *int `json:"next_offset"`
			Shelves    []struct {
				Name      string `json:"name"`
				LikeCount *int   `json:"like_count"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.TotalCount != nil {
			t.Fatalf("total_count = %v, want absent - recent no longer sends it", *got.TotalCount)
		}
		if got.NextOffset != nil {
			t.Fatalf("next_offset = %v, want absent (offset+consumed reached the total: exhausted)", *got.NextOffset)
		}
		if len(got.Shelves) != 2 {
			t.Fatalf("got = %+v", got)
		}
		for _, s := range got.Shelves {
			if s.LikeCount == nil {
				t.Fatalf("like_count missing on %+v", s)
			}
		}
	})

	t.Run("recent with social down: cards present, like_count absent, no social_available field", func(t *testing.T) {
		id1 := uuid.New()
		shelfA := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "A", Slug: "a", OwnerId: id1, Visibility: "listed", CoverUrls: []string{}}
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{
			sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
				return []userapi.ProfileCard{{UserId: id1, Handle: "alice", ProfileVisibility: "listed"}}, nil
			},
		}
		h.collection = &stubCollection{listSharedShelves: func(context.Context, string, []uuid.UUID, int, int) ([]collectionapi.SharedShelfSummary, int, error) {
			return []collectionapi.SharedShelfSummary{shelfA}, 1, nil
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, errors.New("social down")
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (degrade, not fail): %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "social_available") {
			t.Fatalf("explore has no social_available field at all: %s", rec.Body)
		}
		var got struct {
			Shelves []struct {
				Name      string `json:"name"`
				LikeCount *int   `json:"like_count"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Shelves) != 1 || got.Shelves[0].Name != "A" {
			t.Fatalf("cards should still be present: %+v", got.Shelves)
		}
		if got.Shelves[0].LikeCount != nil {
			t.Fatalf("like_count = %v, want nil (social degraded, counts omitted)", got.Shelves[0].LikeCount)
		}
	})

	// TestUnitExplore_RecentAndTop owner-gate: a shelf is listed by
	// query construction alone (collection's unfiltered query only ever
	// returns visibility=listed rows), so the owner card is the one
	// check left; an owner who has gone unlisted (or vanished) drops
	// their shelf while a listed sibling in the same page survives.
	t.Run("recent: owner-unlisted shelf dropped while listed sibling survives", func(t *testing.T) {
		listedOwner, unlistedOwner := uuid.New(), uuid.New()
		kept := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Kept", Slug: "kept", OwnerId: listedOwner, Visibility: "listed", CoverUrls: []string{}}
		dropped := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Dropped", Slug: "dropped", OwnerId: unlistedOwner, Visibility: "listed", CoverUrls: []string{}}

		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
			byID := map[uuid.UUID]userapi.ProfileCard{
				listedOwner:   {UserId: listedOwner, Handle: "keeper", ProfileVisibility: "listed"},
				unlistedOwner: {UserId: unlistedOwner, Handle: "hidden", ProfileVisibility: "unlisted"},
			}
			out := make([]userapi.ProfileCard, 0, len(ids))
			for _, id := range ids {
				if c, ok := byID[id]; ok {
					out = append(out, c)
				}
			}
			return out, nil
		}}
		var calls int
		h.collection = &stubCollection{listSharedShelves: func(context.Context, string, []uuid.UUID, int, int) ([]collectionapi.SharedShelfSummary, int, error) {
			calls++
			return []collectionapi.SharedShelfSummary{dropped, kept}, 2, nil
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent&limit=2")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if calls != 1 {
			t.Fatalf("collection.ListSharedShelves called %d times, want exactly 1 (total exhausted after round 1)", calls)
		}
		var got struct {
			Shelves []struct {
				Name string `json:"name"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Shelves) != 1 || got.Shelves[0].Name != "Kept" {
			t.Fatalf("shelves = %+v, want exactly [Kept] (the unlisted-owner shelf must be dropped)", got.Shelves)
		}
	})

	// TestUnitExplore_RecentAndTop fill loop: mirrors
	// TestUnitFeed_FillLoopAndGating - round 1 comes back entirely
	// dropped (owners gone unlisted), so the loop must re-page collection
	// at the ADVANCED offset for round 2, not repeat round 1's rows.
	t.Run("recent: fill loop refills after an all-dropped round (round 2 fetch proven)", func(t *testing.T) {
		unlistedOwner1, unlistedOwner2, listedOwner := uuid.New(), uuid.New(), uuid.New()
		dropped1 := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Dropped1", Slug: "dropped1", OwnerId: unlistedOwner1, Visibility: "listed", CoverUrls: []string{}}
		dropped2 := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Dropped2", Slug: "dropped2", OwnerId: unlistedOwner2, Visibility: "listed", CoverUrls: []string{}}
		survivor := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Survivor", Slug: "survivor", OwnerId: listedOwner, Visibility: "listed", CoverUrls: []string{}}

		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
			byID := map[uuid.UUID]userapi.ProfileCard{
				unlistedOwner1: {UserId: unlistedOwner1, Handle: "hidden1", ProfileVisibility: "unlisted"},
				unlistedOwner2: {UserId: unlistedOwner2, Handle: "hidden2", ProfileVisibility: "unlisted"},
				listedOwner:    {UserId: listedOwner, Handle: "keeper", ProfileVisibility: "listed"},
			}
			out := make([]userapi.ProfileCard, 0, len(ids))
			for _, id := range ids {
				if c, ok := byID[id]; ok {
					out = append(out, c)
				}
			}
			return out, nil
		}}
		var calls int
		var gotOffsets []int
		h.collection = &stubCollection{listSharedShelves: func(_ context.Context, _ string, _ []uuid.UUID, limit, offset int) ([]collectionapi.SharedShelfSummary, int, error) {
			calls++
			gotOffsets = append(gotOffsets, offset)
			switch calls {
			case 1:
				return []collectionapi.SharedShelfSummary{dropped1, dropped2}, 3, nil
			case 2:
				return []collectionapi.SharedShelfSummary{survivor}, 3, nil
			default:
				t.Fatalf("collection.ListSharedShelves called a 3rd time; it should have stopped exhausted after round 2")
				return nil, 0, nil
			}
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent&limit=2")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if calls != 2 {
			t.Fatalf("collection.ListSharedShelves called %d times, want exactly 2 (round 1 all-dropped, round 2 fills)", calls)
		}
		if len(gotOffsets) != 2 || gotOffsets[0] != 0 || gotOffsets[1] != 2 {
			t.Fatalf("offsets = %v, want [0 2] (round 2 resumes after round 1's 2 raw rows)", gotOffsets)
		}
		var got struct {
			Shelves []struct {
				Name string `json:"name"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Shelves) != 1 || got.Shelves[0].Name != "Survivor" {
			t.Fatalf("shelves = %+v, want exactly [Survivor] (round 2's owner-listed shelf)", got.Shelves)
		}
	})

	// TestUnitExplore_RecentAndTop next_offset arithmetic: the response
	// is limit-satisfied after round 1 (1 survivor fills limit=1), but
	// collection actually handed back 2 raw rows to get there and more
	// remain beyond them - next_offset must reflect the 2 raw rows
	// consumed, not the 1 gated survivor nor any other count.
	t.Run("recent: next_offset counts raw consumed rows, not gated survivors", func(t *testing.T) {
		unlistedOwner, listedOwner := uuid.New(), uuid.New()
		dropped := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Dropped", Slug: "dropped", OwnerId: unlistedOwner, Visibility: "listed", CoverUrls: []string{}}
		survivor := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Survivor", Slug: "survivor", OwnerId: listedOwner, Visibility: "listed", CoverUrls: []string{}}

		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
			byID := map[uuid.UUID]userapi.ProfileCard{
				unlistedOwner: {UserId: unlistedOwner, Handle: "hidden", ProfileVisibility: "unlisted"},
				listedOwner:   {UserId: listedOwner, Handle: "keeper", ProfileVisibility: "listed"},
			}
			out := make([]userapi.ProfileCard, 0, len(ids))
			for _, id := range ids {
				if c, ok := byID[id]; ok {
					out = append(out, c)
				}
			}
			return out, nil
		}}
		var calls int
		h.collection = &stubCollection{listSharedShelves: func(context.Context, string, []uuid.UUID, int, int) ([]collectionapi.SharedShelfSummary, int, error) {
			calls++
			// 2 raw rows, only 1 survives the owner gate; total=5 means 3
			// more rows exist past this page.
			return []collectionapi.SharedShelfSummary{dropped, survivor}, 5, nil
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent&limit=1")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if calls != 1 {
			t.Fatalf("collection.ListSharedShelves called %d times, want exactly 1 (limit filled on round 1)", calls)
		}
		var got struct {
			NextOffset *int `json:"next_offset"`
			Shelves    []struct {
				Name string `json:"name"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Shelves) != 1 || got.Shelves[0].Name != "Survivor" {
			t.Fatalf("shelves = %+v, want exactly [Survivor]", got.Shelves)
		}
		if got.NextOffset == nil || *got.NextOffset != 2 {
			t.Fatalf("next_offset = %v, want 2 (the 2 RAW rows collection returned, not the 1 gated survivor)", got.NextOffset)
		}
	})

	// TestUnitExplore_RecentAndTop mid-page-break regression (review
	// finding): round 1 examines its whole page without filling (A
	// survives, B's owner is unlisted), so it advances by the full
	// page as usual - offset 2 for round 2. Round 2 then fills the
	// limit at C, the FIRST of its two rows, leaving D - also listed -
	// unexamined in that same page's tail. The old code advanced pos
	// by round 2's WHOLE consumed page (to 4) before ever gating,
	// regardless of where the fill happened, so next_offset pointed
	// past D and it was skipped forever. next_offset must instead
	// resume just past C (offset 3, at D), the last row actually
	// examined-and-included.
	t.Run("recent: mid-page fill resumes past the last INCLUDED row, not the whole page (regression)", func(t *testing.T) {
		ownerA, ownerB, ownerC, ownerD := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		shelfA := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "A", Slug: "a", OwnerId: ownerA, Visibility: "listed", CoverUrls: []string{}}
		shelfB := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "B", Slug: "b", OwnerId: ownerB, Visibility: "listed", CoverUrls: []string{}}
		shelfC := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "C", Slug: "c", OwnerId: ownerC, Visibility: "listed", CoverUrls: []string{}}
		shelfD := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "D", Slug: "d", OwnerId: ownerD, Visibility: "listed", CoverUrls: []string{}}

		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
			byID := map[uuid.UUID]userapi.ProfileCard{
				ownerA: {UserId: ownerA, Handle: "a", ProfileVisibility: "listed"},
				ownerB: {UserId: ownerB, Handle: "b", ProfileVisibility: "unlisted"},
				ownerC: {UserId: ownerC, Handle: "c", ProfileVisibility: "listed"},
				ownerD: {UserId: ownerD, Handle: "d", ProfileVisibility: "listed"},
			}
			out := make([]userapi.ProfileCard, 0, len(ids))
			for _, id := range ids {
				if c, ok := byID[id]; ok {
					out = append(out, c)
				}
			}
			return out, nil
		}}
		var calls int
		var gotOffsets []int
		h.collection = &stubCollection{listSharedShelves: func(_ context.Context, _ string, _ []uuid.UUID, _ int, offset int) ([]collectionapi.SharedShelfSummary, int, error) {
			calls++
			gotOffsets = append(gotOffsets, offset)
			switch calls {
			case 1:
				// total=5: one more row exists past D, so the buggy
				// arithmetic's wrong resume point (4, past D) and the
				// fixed one (3, at D) are both "more data left" - the
				// bug's symptom is a present-but-wrong next_offset
				// that skips D, not a falsely-absent one.
				return []collectionapi.SharedShelfSummary{shelfA, shelfB}, 5, nil
			case 2:
				return []collectionapi.SharedShelfSummary{shelfC, shelfD}, 5, nil
			default:
				t.Fatalf("collection.ListSharedShelves called a 3rd time; round 2 already fills the limit at C")
				return nil, 0, nil
			}
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent&limit=2")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if calls != 2 {
			t.Fatalf("collection.ListSharedShelves called %d times, want exactly 2", calls)
		}
		if len(gotOffsets) != 2 || gotOffsets[0] != 0 || gotOffsets[1] != 2 {
			t.Fatalf("offsets = %v, want [0 2] (round 2 resumes after round 1's 2 raw rows)", gotOffsets)
		}
		var got struct {
			NextOffset *int `json:"next_offset"`
			Shelves    []struct {
				Name string `json:"name"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Shelves) != 2 || got.Shelves[0].Name != "A" || got.Shelves[1].Name != "C" {
			t.Fatalf("shelves = %+v, want exactly [A C]", got.Shelves)
		}
		if got.NextOffset == nil || *got.NextOffset != 3 {
			t.Fatalf("next_offset = %v, want 3 (just past C, the last INCLUDED row - not 4, which would skip D)", got.NextOffset)
		}
	})

	// TestUnitExplore_RecentAndTop mid-page-break continuation: a
	// caller following the previous test's next_offset=3 must land
	// exactly on D, the shelf the old arithmetic skipped, and see no
	// further next_offset once collection confirms D was the last row.
	t.Run("recent: continuation request at the resumed offset serves the previously-skipped shelf", func(t *testing.T) {
		ownerD := uuid.New()
		shelfD := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "D", Slug: "d", OwnerId: ownerD, Visibility: "listed", CoverUrls: []string{}}

		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{{UserId: ownerD, Handle: "d", ProfileVisibility: "listed"}}, nil
		}}
		var calls int
		var gotOffset int
		h.collection = &stubCollection{listSharedShelves: func(_ context.Context, _ string, _ []uuid.UUID, _ int, offset int) ([]collectionapi.SharedShelfSummary, int, error) {
			calls++
			gotOffset = offset
			// D is the last raw row (total=4, offset=3): nothing left after it.
			return []collectionapi.SharedShelfSummary{shelfD}, 4, nil
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent&limit=2&offset=3")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if calls != 1 {
			t.Fatalf("collection.ListSharedShelves called %d times, want exactly 1 (exhausted after round 1)", calls)
		}
		if gotOffset != 3 {
			t.Fatalf("offset = %d, want 3 (the resumed position from the prior page)", gotOffset)
		}
		var got struct {
			NextOffset *int `json:"next_offset"`
			Shelves    []struct {
				Name string `json:"name"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Shelves) != 1 || got.Shelves[0].Name != "D" {
			t.Fatalf("shelves = %+v, want exactly [D]", got.Shelves)
		}
		if got.NextOffset != nil {
			t.Fatalf("next_offset = %v, want absent (D was the last raw row)", got.NextOffset)
		}
	})

	// TestUnitExplore_RecentAndTop exhaustion: collection handing back
	// fewer rows than asked means there is nothing left to page to, so
	// next_offset must be entirely absent from the response, the same
	// "absent, not null" contract next_cursor uses on the feed.
	t.Run("recent: exhaustion (collection returns fewer than asked) leaves next_offset absent", func(t *testing.T) {
		owner := uuid.New()
		shelf := collectionapi.SharedShelfSummary{Id: uuid.New(), Name: "Only", Slug: "only", OwnerId: owner, Visibility: "listed", CoverUrls: []string{}}

		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{{UserId: owner, Handle: "keeper", ProfileVisibility: "listed"}}, nil
		}}
		h.collection = &stubCollection{listSharedShelves: func(context.Context, string, []uuid.UUID, int, int) ([]collectionapi.SharedShelfSummary, int, error) {
			// The default limit is 20; handing back 1 row (fewer than
			// asked) means the listed-shelf stream is exhausted.
			return []collectionapi.SharedShelfSummary{shelf}, 1, nil
		}}
		h.social = &stubSocialFull{shelvesSummary: func(context.Context, string, []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			return nil, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "next_offset") {
			t.Fatalf("next_offset must be entirely absent on exhaustion: %s", rec.Body)
		}
	})

	t.Run("top: leaderboard order preserved, drops an unlisted shelf and a non-listed owner", func(t *testing.T) {
		shelfA, shelfB, shelfC, shelfD := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		ownerA, ownerB, ownerC, ownerD := uuid.New(), uuid.New(), uuid.New(), uuid.New()

		var gotTopLimit int
		var gotSummaryIDs []uuid.UUID
		soc := &stubSocialFull{
			topShelves: func(_ context.Context, _ string, limit int) ([]uuid.UUID, error) {
				gotTopLimit = limit
				return []uuid.UUID{shelfA, shelfB, shelfC, shelfD}, nil
			},
		}
		soc.shelvesSummary = func(_ context.Context, _ string, ids []uuid.UUID) ([]socialapi.ShelfSocialSummary, error) {
			gotSummaryIDs = ids
			return []socialapi.ShelfSocialSummary{{ShelfId: shelfA, LikeCount: 9}, {ShelfId: shelfD, LikeCount: 4}}, nil
		}
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.social = soc
		h.collection = &stubCollection{sharedShelvesByIDs: func(context.Context, string, []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
			// Deliberately returned out of leaderboard order, to prove
			// the handler walks the leaderboard's own order rather than
			// trusting this response's order.
			return []collectionapi.SharedShelfSummary{
				{Id: shelfD, Name: "D", Slug: "d", OwnerId: ownerD, Visibility: "listed", CoverUrls: []string{}},
				{Id: shelfC, Name: "C", Slug: "c", OwnerId: ownerC, Visibility: "listed", CoverUrls: []string{}},
				{Id: shelfB, Name: "B", Slug: "b", OwnerId: ownerB, Visibility: "unlisted", CoverUrls: []string{}},
				{Id: shelfA, Name: "A", Slug: "a", OwnerId: ownerA, Visibility: "listed", CoverUrls: []string{}},
			}, nil
		}}
		h.users = &stubUsersFull{sharedCardsByIDs: func(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
			return []userapi.ProfileCard{
				{UserId: ownerA, Handle: "ownerA", ProfileVisibility: "listed"},
				{UserId: ownerB, Handle: "ownerB", ProfileVisibility: "listed"},
				{UserId: ownerC, Handle: "ownerC", ProfileVisibility: "unlisted"},
				{UserId: ownerD, Handle: "ownerD", ProfileVisibility: "listed"},
			}, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=top")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if gotTopLimit != topShelvesLimit {
			t.Fatalf("social.TopShelves limit = %d, want %d", gotTopLimit, topShelvesLimit)
		}
		if len(gotSummaryIDs) != 2 || gotSummaryIDs[0] != shelfA || gotSummaryIDs[1] != shelfD {
			t.Fatalf("social.ShelvesSummary ids = %v, want exactly the 2 survivors [A D]", gotSummaryIDs)
		}
		var got struct {
			Shelves []struct {
				Name string `json:"name"`
			} `json:"shelves"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Shelves) != 2 || got.Shelves[0].Name != "A" || got.Shelves[1].Name != "D" {
			t.Fatalf("shelves = %+v, want [A D] in leaderboard order", got.Shelves)
		}
	})
}

// TestUnitExplore_ValidatesLimitAndOffsetMinimum pins that
// GetExplore's sort=recent branch rejects a sub-minimum limit or a
// negative offset with 400 invalid_param before any upstream call -
// api/bff.yaml declares minimum: 1 on limit and minimum: 0 on offset,
// but the handler used to clamp only the maximum, so limit=0 or
// offset=-1 passed straight through. limit=1 (the minimum) must still
// be accepted and reach the upstream collection call.
func TestUnitExplore_ValidatesLimitAndOffsetMinimum(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	assertInvalidParam := func(t *testing.T, path string) {
		t.Helper()
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
		}
		var p struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_param" {
			t.Fatalf("problem code = %+v, err = %v, want invalid_param", p, err)
		}
	}

	t.Run("limit=0 -> 400", func(t *testing.T) {
		h.users = &stubUsersFull{}
		assertInvalidParam(t, "/api/explore?sort=recent&limit=0")
	})

	t.Run("offset=-1 -> 400", func(t *testing.T) {
		h.users = &stubUsersFull{}
		assertInvalidParam(t, "/api/explore?sort=recent&offset=-1")
	})

	t.Run("limit=1 (the minimum) is accepted and reaches the upstream call", func(t *testing.T) {
		var called bool
		h.collection = &stubCollection{listSharedShelves: func(context.Context, string, []uuid.UUID, int, int) ([]collectionapi.SharedShelfSummary, int, error) {
			called = true
			return nil, 0, nil
		}}
		rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=recent&limit=1")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body)
		}
		if !called {
			t.Fatal("want the upstream collection call to happen at limit=1")
		}
	})
}
