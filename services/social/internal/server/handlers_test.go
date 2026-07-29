package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/social/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/social/internal/server"
	"github.com/levonn-dev/vgkeep/services/social/internal/store"
	"github.com/levonn-dev/vgkeep/services/social/internal/userclient"
)

// ---- stub doubles (function fields; a nil field panics loudly) ----

// stubStore implements server.Store via function fields.
type stubStore struct {
	follow         func(ctx context.Context, follower, followee uuid.UUID, cap int) (bool, error)
	unfollow       func(ctx context.Context, follower, followee uuid.UUID) error
	profileSummary func(ctx context.Context, userID, viewer uuid.UUID) (int, int, bool, error)
	followeeIDs    func(ctx context.Context, follower uuid.UUID) ([]uuid.UUID, error)
	like           func(ctx context.Context, user, shelf, owner uuid.UUID, cap int) (bool, error)
	unlike         func(ctx context.Context, user, shelf uuid.UUID) error
	shelfSummaries func(ctx context.Context, ids []uuid.UUID, viewer uuid.UUID) ([]store.ShelfSummary, error)
	topShelves     func(ctx context.Context, limit int) ([]uuid.UUID, error)
	createComment  func(ctx context.Context, shelf, owner, author uuid.UUID, body string, cap int) (store.Comment, error)
	listComments   func(ctx context.Context, shelf uuid.UUID, cursor *store.Cursor, limit int) ([]store.Comment, error)
	commentsByIDs  func(ctx context.Context, ids []uuid.UUID) ([]store.Comment, error)
	deleteComment  func(ctx context.Context, id, caller uuid.UUID) (string, error)
	feed           func(ctx context.Context, viewer uuid.UUID, tab string, cursor *store.Cursor, limit int) ([]store.Event, error)
	recordPublish  func(ctx context.Context, actor, shelf uuid.UUID, throttle time.Duration) (string, error)
	purgeUser      func(ctx context.Context, userID uuid.UUID) error
}

var _ server.Store = (*stubStore)(nil)

func (s *stubStore) Follow(ctx context.Context, follower, followee uuid.UUID, cap int) (bool, error) {
	if s.follow == nil {
		panic("unexpected Follow")
	}
	return s.follow(ctx, follower, followee, cap)
}
func (s *stubStore) Unfollow(ctx context.Context, follower, followee uuid.UUID) error {
	if s.unfollow == nil {
		panic("unexpected Unfollow")
	}
	return s.unfollow(ctx, follower, followee)
}
func (s *stubStore) ProfileSummary(ctx context.Context, userID, viewer uuid.UUID) (int, int, bool, error) {
	if s.profileSummary == nil {
		panic("unexpected ProfileSummary")
	}
	return s.profileSummary(ctx, userID, viewer)
}
func (s *stubStore) FolloweeIDs(ctx context.Context, follower uuid.UUID) ([]uuid.UUID, error) {
	if s.followeeIDs == nil {
		panic("unexpected FolloweeIDs")
	}
	return s.followeeIDs(ctx, follower)
}
func (s *stubStore) Like(ctx context.Context, user, shelf, shelfOwner uuid.UUID, cap int) (bool, error) {
	if s.like == nil {
		panic("unexpected Like")
	}
	return s.like(ctx, user, shelf, shelfOwner, cap)
}
func (s *stubStore) Unlike(ctx context.Context, user, shelf uuid.UUID) error {
	if s.unlike == nil {
		panic("unexpected Unlike")
	}
	return s.unlike(ctx, user, shelf)
}
func (s *stubStore) ShelfSummaries(ctx context.Context, ids []uuid.UUID, viewer uuid.UUID) ([]store.ShelfSummary, error) {
	if s.shelfSummaries == nil {
		panic("unexpected ShelfSummaries")
	}
	return s.shelfSummaries(ctx, ids, viewer)
}
func (s *stubStore) TopShelves(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if s.topShelves == nil {
		panic("unexpected TopShelves")
	}
	return s.topShelves(ctx, limit)
}
func (s *stubStore) CreateComment(ctx context.Context, shelf, shelfOwner, author uuid.UUID, body string, cap int) (store.Comment, error) {
	if s.createComment == nil {
		panic("unexpected CreateComment")
	}
	return s.createComment(ctx, shelf, shelfOwner, author, body, cap)
}
func (s *stubStore) ListLiveComments(ctx context.Context, shelf uuid.UUID, cursor *store.Cursor, limit int) ([]store.Comment, error) {
	if s.listComments == nil {
		panic("unexpected ListLiveComments")
	}
	return s.listComments(ctx, shelf, cursor, limit)
}
func (s *stubStore) LiveCommentsByIDs(ctx context.Context, ids []uuid.UUID) ([]store.Comment, error) {
	if s.commentsByIDs == nil {
		panic("unexpected LiveCommentsByIDs")
	}
	return s.commentsByIDs(ctx, ids)
}
func (s *stubStore) DeleteComment(ctx context.Context, id, caller uuid.UUID) (string, error) {
	if s.deleteComment == nil {
		panic("unexpected DeleteComment")
	}
	return s.deleteComment(ctx, id, caller)
}
func (s *stubStore) Feed(ctx context.Context, viewer uuid.UUID, tab string, cursor *store.Cursor, limit int) ([]store.Event, error) {
	if s.feed == nil {
		panic("unexpected Feed")
	}
	return s.feed(ctx, viewer, tab, cursor, limit)
}
func (s *stubStore) RecordPublish(ctx context.Context, actor, shelf uuid.UUID, throttle time.Duration) (string, error) {
	if s.recordPublish == nil {
		panic("unexpected RecordPublish")
	}
	return s.recordPublish(ctx, actor, shelf, throttle)
}
func (s *stubStore) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if s.purgeUser == nil {
		panic("unexpected PurgeUser")
	}
	return s.purgeUser(ctx, userID)
}

type stubCollection struct {
	sharedShelf func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Shelf, error)
}

func (s *stubCollection) SharedShelf(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Shelf, error) {
	if s.sharedShelf == nil {
		panic("unexpected SharedShelf")
	}
	return s.sharedShelf(ctx, bearer, id)
}

var _ server.Collection = (*stubCollection)(nil)

type stubUsers struct {
	cardsByIDs func(ctx context.Context, bearer string, ids []uuid.UUID) ([]userclient.Card, error)
}

func (s *stubUsers) CardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]userclient.Card, error) {
	if s.cardsByIDs == nil {
		panic("unexpected CardsByIDs")
	}
	return s.cardsByIDs(ctx, bearer, ids)
}

var _ server.Users = (*stubUsers)(nil)

// ---- follows ----

func TestUnitFollow(t *testing.T) {
	me := uuid.New()
	other := uuid.New()

	t.Run("valid followee follows and 204s", func(t *testing.T) {
		st := &stubStore{follow: func(_ context.Context, follower, followee uuid.UUID, cap int) (bool, error) {
			if follower != me || followee != other || cap != 100 {
				t.Fatalf("follow(%s, %s, %d)", follower, followee, cap)
			}
			return true, nil
		}}
		users := &stubUsers{cardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userclient.Card, error) {
			return []userclient.Card{{UserID: other, Handle: "Other", Visibility: "listed"}}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, users)
		resp := do(t, http.MethodPut, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("self-follow is 400", func(t *testing.T) {
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodPut, srv.URL+"/follows/"+me.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "self_follow")
	})

	t.Run("private or missing followee is 404", func(t *testing.T) {
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return []userclient.Card{{UserID: other, Visibility: "private"}}, nil
		}}
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, users)
		resp := do(t, http.MethodPut, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusNotFound, "profile_not_found")

		empty := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return nil, nil
		}}
		srv2, a2 := newUnitServer(t, &stubStore{}, &stubCollection{}, empty)
		resp2 := do(t, http.MethodPut, srv2.URL+"/follows/"+other.String(), a2.token(t, me.String()), nil)
		wantProblem(t, resp2, http.StatusNotFound, "profile_not_found")
	})

	t.Run("cap maps to 429", func(t *testing.T) {
		st := &stubStore{follow: func(context.Context, uuid.UUID, uuid.UUID, int) (bool, error) {
			return false, store.ErrCapExceeded
		}}
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return []userclient.Card{{UserID: other, Visibility: "listed"}}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, users)
		resp := do(t, http.MethodPut, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusTooManyRequests, "cap_exceeded")
	})

	t.Run("user service outage is 502", func(t *testing.T) {
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return nil, errors.New("boom")
		}}
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, users)
		resp := do(t, http.MethodPut, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadGateway, "upstream_error")
	})

	t.Run("store error is 500", func(t *testing.T) {
		st := &stubStore{follow: func(context.Context, uuid.UUID, uuid.UUID, int) (bool, error) {
			return false, errors.New("boom")
		}}
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return []userclient.Card{{UserID: other, Visibility: "listed"}}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, users)
		resp := do(t, http.MethodPut, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

func TestUnitUnfollow(t *testing.T) {
	me, other := uuid.New(), uuid.New()

	t.Run("unfollow succeeds and 204s", func(t *testing.T) {
		var unfollowed struct{ follower, followee uuid.UUID }
		st := &stubStore{unfollow: func(_ context.Context, follower, followee uuid.UUID) error {
			unfollowed.follower, unfollowed.followee = follower, followee
			return nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if unfollowed.follower != me || unfollowed.followee != other {
			t.Fatalf("unfollow(%s, %s)", unfollowed.follower, unfollowed.followee)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		st := &stubStore{unfollow: func(context.Context, uuid.UUID, uuid.UUID) error { return errors.New("boom") }}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

// ---- likes ----

// problemDetail decodes a problem+json response's code and detail so
// a test can compare two responses byte-for-byte instead of just
// checking that both happen to be a 404.
func problemDetail(t *testing.T, resp *http.Response) (code, detail string) {
	t.Helper()
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p.Code, p.Detail
}

func TestUnitLike(t *testing.T) {
	me, owner := uuid.New(), uuid.New()
	shelf := uuid.New()

	t.Run("resolves shelf, denormalizes owner, 204s", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(_ context.Context, bearer string, id uuid.UUID) (collectionclient.Shelf, error) {
			if id != shelf || bearer == "" {
				t.Fatalf("resolve(%s, bearer=%q)", id, bearer)
			}
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return []userclient.Card{{UserID: owner, Visibility: "listed"}}, nil
		}}
		st := &stubStore{like: func(_ context.Context, user, sh, ow uuid.UUID, cap int) (bool, error) {
			if user != me || sh != shelf || ow != owner || cap != 200 {
				t.Fatalf("like(%s, %s, %s, %d)", user, sh, ow, cap)
			}
			return true, nil
		}}
		srv, a := newUnitServer(t, st, col, users)
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("collection 404 relays as shelf_not_found", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrShelfNotFound
		}}
		srv, a := newUnitServer(t, &stubStore{}, col, &stubUsers{})
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusNotFound, "shelf_not_found")
	})

	t.Run("collection outage is 502 - never accept unvalidated writes", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrUpstream
		}}
		srv, a := newUnitServer(t, &stubStore{}, col, &stubUsers{})
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadGateway, "upstream_error")
	})

	// Effective visibility is the stricter of the shelf's own
	// visibility and its owner's profile visibility: a shelf that
	// is itself listed must still 404 when its owner is private, and
	// that 404 must be indistinguishable from a genuinely missing
	// shelf - otherwise PUT /likes/{shelfId} is an existence oracle
	// for private profiles.
	t.Run("owner-private profile 404s as shelf_not_found and never touches the store", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		users := &stubUsers{cardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userclient.Card, error) {
			if len(ids) != 1 || ids[0] != owner {
				t.Fatalf("ids = %v", ids)
			}
			return []userclient.Card{{UserID: owner, Visibility: "private"}}, nil
		}}
		srv, a := newUnitServer(t, &stubStore{}, col, users)
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		gotCode, gotDetail := problemDetail(t, resp)
		if resp.StatusCode != http.StatusNotFound || gotCode != "shelf_not_found" {
			t.Fatalf("status = %d, code = %q", resp.StatusCode, gotCode)
		}

		missing := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrShelfNotFound
		}}
		srv2, a2 := newUnitServer(t, &stubStore{}, missing, &stubUsers{})
		resp2 := do(t, http.MethodPut, srv2.URL+"/likes/"+shelf.String(), a2.token(t, me.String()), nil)
		wantCode, wantDetail := problemDetail(t, resp2)
		if gotCode != wantCode || gotDetail != wantDetail {
			t.Fatalf("owner-private body = (%q, %q), want it to match the shelf-missing body (%q, %q)",
				gotCode, gotDetail, wantCode, wantDetail)
		}
	})

	t.Run("owner-unlisted and owner-listed profiles allow the like", func(t *testing.T) {
		for _, vis := range []string{"unlisted", "listed"} {
			t.Run(vis, func(t *testing.T) {
				col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
					return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
				}}
				users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
					return []userclient.Card{{UserID: owner, Visibility: vis}}, nil
				}}
				var liked bool
				st := &stubStore{like: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) (bool, error) {
					liked = true
					return true, nil
				}}
				srv, a := newUnitServer(t, st, col, users)
				resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
				if resp.StatusCode != http.StatusNoContent || !liked {
					t.Fatalf("status = %d, liked = %v", resp.StatusCode, liked)
				}
			})
		}
	})

	t.Run("owner profile lookup outage is 502", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return nil, errors.New("boom")
		}}
		srv, a := newUnitServer(t, &stubStore{}, col, users)
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadGateway, "upstream_error")
	})

	t.Run("owner profile missing (empty card slice) 404s as shelf_not_found", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return nil, nil
		}}
		srv, a := newUnitServer(t, &stubStore{}, col, users)
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusNotFound, "shelf_not_found")
	})

	t.Run("cap maps to 429", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return []userclient.Card{{UserID: owner, Visibility: "listed"}}, nil
		}}
		st := &stubStore{like: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) (bool, error) {
			return false, store.ErrCapExceeded
		}}
		srv, a := newUnitServer(t, st, col, users)
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusTooManyRequests, "cap_exceeded")
	})

	t.Run("store error is 500", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
			return []userclient.Card{{UserID: owner, Visibility: "listed"}}, nil
		}}
		st := &stubStore{like: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) (bool, error) {
			return false, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, col, users)
		resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

func TestUnitUnlikeShelf(t *testing.T) {
	me := uuid.New()
	shelf := uuid.New()

	t.Run("unlike succeeds and 204s", func(t *testing.T) {
		st := &stubStore{unlike: func(_ context.Context, user, sh uuid.UUID) error {
			if user != me || sh != shelf {
				t.Fatalf("unlike(%s, %s)", user, sh)
			}
			return nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		st := &stubStore{unlike: func(context.Context, uuid.UUID, uuid.UUID) error { return errors.New("boom") }}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

func TestUnitSummaries(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	shelf := uuid.New()

	t.Run("profile summary", func(t *testing.T) {
		st := &stubStore{profileSummary: func(_ context.Context, userID, viewer uuid.UUID) (int, int, bool, error) {
			if userID != other || viewer != me {
				t.Fatalf("summary(%s, %s)", userID, viewer)
			}
			return 3, 5, true, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/profiles/"+other.String()+"/summary", a.token(t, me.String()), nil)
		var got struct {
			FollowerCount  int  `json:"follower_count"`
			FollowingCount int  `json:"following_count"`
			ViewerFollows  bool `json:"viewer_follows"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.FollowerCount != 3 || got.FollowingCount != 5 || !got.ViewerFollows {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("shelf summaries batch", func(t *testing.T) {
		st := &stubStore{shelfSummaries: func(_ context.Context, ids []uuid.UUID, viewer uuid.UUID) ([]store.ShelfSummary, error) {
			return []store.ShelfSummary{{ShelfID: shelf, LikeCount: 2, CommentCount: 1, ViewerLikes: true}}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/summary?ids="+shelf.String(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("profile summary store error is 500", func(t *testing.T) {
		st := &stubStore{profileSummary: func(context.Context, uuid.UUID, uuid.UUID) (int, int, bool, error) {
			return 0, 0, false, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/profiles/"+other.String()+"/summary", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("shelf summaries store error is 500", func(t *testing.T) {
		st := &stubStore{shelfSummaries: func(context.Context, []uuid.UUID, uuid.UUID) ([]store.ShelfSummary, error) {
			return nil, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/summary?ids="+shelf.String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("too many ids is a 400 before the store is touched", func(t *testing.T) {
		// api/social.yaml declares maxItems: 100 on ids; the generated
		// param binder does not enforce it, so the handler must reject
		// 101+ entries itself (the empty stubStore proves it).
		q := url.Values{}
		for i := 0; i < 101; i++ {
			q.Add("ids", uuid.New().String())
		}
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/summary?"+q.Encode(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "too_many_ids")
	})

	t.Run("exactly the max (100 ids) is accepted", func(t *testing.T) {
		q := url.Values{}
		for i := 0; i < 100; i++ {
			q.Add("ids", uuid.New().String())
		}
		st := &stubStore{shelfSummaries: func(_ context.Context, ids []uuid.UUID, _ uuid.UUID) ([]store.ShelfSummary, error) {
			if len(ids) != 100 {
				t.Fatalf("ids = %d, want 100", len(ids))
			}
			return nil, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/summary?"+q.Encode(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("100 ids (the yaml max) must be accepted: %d", resp.StatusCode)
		}
	})
}

func TestUnitComments(t *testing.T) {
	me, owner, author := uuid.New(), uuid.New(), uuid.New()
	shelf := uuid.New()
	body := func(s string) map[string]string { return map[string]string{"body": s} }

	t.Run("create validates shelf and returns 201", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "unlisted"}, nil
		}}
		st := &stubStore{createComment: func(_ context.Context, sh, ow, au uuid.UUID, b string, cap int) (store.Comment, error) {
			if sh != shelf || ow != owner || au != me || b != "nice CIB run" || cap != 50 {
				t.Fatalf("create(%s, %s, %s, %q, %d)", sh, ow, au, b, cap)
			}
			id := uuid.New()
			return store.Comment{ID: id, ShelfID: sh, ShelfOwnerID: ow, AuthorID: &au, Body: &b, CreatedAt: time.Now()}, nil
		}}
		srv, a := newUnitServer(t, st, col, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/shelves/"+shelf.String()+"/comments",
			a.token(t, me.String()), body("nice CIB run"))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("cap 429s and empty body 400s", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		st := &stubStore{createComment: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, int) (store.Comment, error) {
			return store.Comment{}, store.ErrCapExceeded
		}}
		srv, a := newUnitServer(t, st, col, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/shelves/"+shelf.String()+"/comments", a.token(t, me.String()), body("x"))
		wantProblem(t, resp, http.StatusTooManyRequests, "cap_exceeded")

		srv2, a2 := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp2 := do(t, http.MethodPost, srv2.URL+"/shelves/"+shelf.String()+"/comments", a2.token(t, me.String()), body("   "))
		wantProblem(t, resp2, http.StatusBadRequest, "invalid_body")

		// Sanctioned addition: the 2000-char DB CHECK boundary must 400
		// before the store call (an opaque 500 otherwise), same as the
		// empty-body case above - reuse srv2/a2 to prove the store and
		// collection client are never touched for either invalid shape.
		resp3 := do(t, http.MethodPost, srv2.URL+"/shelves/"+shelf.String()+"/comments",
			a2.token(t, me.String()), body(strings.Repeat("x", 2001)))
		wantProblem(t, resp3, http.StatusBadRequest, "invalid_body")
	})

	t.Run("delete maps sentinels (403, 404)", func(t *testing.T) {
		cid := uuid.New()
		cases := []struct {
			name string
			err  error
			code int
			slug string
		}{
			{"forbidden", store.ErrForbidden, http.StatusForbidden, "forbidden"},
			{"missing", store.ErrNotFound, http.StatusNotFound, "comment_not_found"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				st := &stubStore{deleteComment: func(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "", c.err }}
				srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
				resp := do(t, http.MethodDelete, srv.URL+"/comments/"+cid.String(), a.token(t, me.String()), nil)
				wantProblem(t, resp, c.code, c.slug)
			})
		}
	})

	t.Run("list pages with cursor and rejects garbage cursors", func(t *testing.T) {
		b := "hello"
		au := author
		st := &stubStore{listComments: func(_ context.Context, sh uuid.UUID, cur *store.Cursor, limit int) ([]store.Comment, error) {
			if cur != nil {
				t.Fatalf("first page must have nil cursor")
			}
			return []store.Comment{{ID: uuid.New(), ShelfID: sh, ShelfOwnerID: owner, AuthorID: &au, Body: &b, CreatedAt: time.Now()}}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/"+shelf.String()+"/comments", a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		bad := do(t, http.MethodGet, srv.URL+"/shelves/"+shelf.String()+"/comments?cursor=garbage", a.token(t, me.String()), nil)
		wantProblem(t, bad, http.StatusBadRequest, "invalid_param")
	})

	t.Run("list respects an explicit limit and pages at the boundary", func(t *testing.T) {
		b, au := "hi", author
		one := store.Comment{ID: uuid.New(), ShelfID: shelf, ShelfOwnerID: owner, AuthorID: &au, Body: &b, CreatedAt: time.Now()}
		st := &stubStore{listComments: func(_ context.Context, _ uuid.UUID, _ *store.Cursor, limit int) ([]store.Comment, error) {
			if limit != 1 {
				t.Fatalf("limit = %d, want 1", limit)
			}
			return []store.Comment{one}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/"+shelf.String()+"/comments?limit=1", a.token(t, me.String()), nil)
		var got struct {
			NextCursor *string `json:"next_cursor"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		want := (store.Cursor{CreatedAt: one.CreatedAt, ID: one.ID}).String()
		if got.NextCursor == nil || *got.NextCursor != want {
			t.Fatalf("next_cursor = %+v, want %q", got.NextCursor, want)
		}
	})

	t.Run("list limit is clamped by the api bounds", func(t *testing.T) {
		// api/social.yaml declares minimum:1 maximum:50 on limit; the
		// generated param binder is type-only, so the handler must
		// enforce the bound itself before the store is touched (the
		// empty stubStore proves it for the out-of-range cases).
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		for _, limit := range []string{"0", "51"} {
			resp := do(t, http.MethodGet, srv.URL+"/shelves/"+shelf.String()+"/comments?limit="+limit,
				a.token(t, me.String()), nil)
			wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
		}

		b, au := "hi", author
		st := &stubStore{listComments: func(_ context.Context, _ uuid.UUID, _ *store.Cursor, limit int) ([]store.Comment, error) {
			if limit != 50 {
				t.Fatalf("limit = %d, want 50", limit)
			}
			return []store.Comment{{ID: uuid.New(), ShelfID: shelf, ShelfOwnerID: owner, AuthorID: &au, Body: &b, CreatedAt: time.Now()}}, nil
		}}
		srv2, a2 := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv2.URL+"/shelves/"+shelf.String()+"/comments?limit=50", a2.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("limit=50 (the yaml max) must be accepted: %d", resp.StatusCode)
		}
	})

	t.Run("list forwards a real cursor", func(t *testing.T) {
		want := store.Cursor{CreatedAt: time.Now(), ID: uuid.New()}
		st := &stubStore{listComments: func(_ context.Context, _ uuid.UUID, cur *store.Cursor, _ int) ([]store.Comment, error) {
			if cur == nil || cur.ID != want.ID {
				t.Fatalf("cursor = %+v, want %+v", cur, want)
			}
			return []store.Comment{}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/"+shelf.String()+"/comments?cursor="+want.String(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("list store error is 500", func(t *testing.T) {
		st := &stubStore{listComments: func(context.Context, uuid.UUID, *store.Cursor, int) ([]store.Comment, error) {
			return nil, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/shelves/"+shelf.String()+"/comments", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("create maps collection errors", func(t *testing.T) {
		notFound := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrShelfNotFound
		}}
		srv, a := newUnitServer(t, &stubStore{}, notFound, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/shelves/"+shelf.String()+"/comments", a.token(t, me.String()), body("hi"))
		wantProblem(t, resp, http.StatusNotFound, "shelf_not_found")

		down := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrUpstream
		}}
		srv2, a2 := newUnitServer(t, &stubStore{}, down, &stubUsers{})
		resp2 := do(t, http.MethodPost, srv2.URL+"/shelves/"+shelf.String()+"/comments", a2.token(t, me.String()), body("hi"))
		wantProblem(t, resp2, http.StatusBadGateway, "upstream_error")
	})

	t.Run("create store error is 500", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		st := &stubStore{createComment: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, int) (store.Comment, error) {
			return store.Comment{}, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, col, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/shelves/"+shelf.String()+"/comments", a.token(t, me.String()), body("hi"))
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("delete succeeds and 204s", func(t *testing.T) {
		cid := uuid.New()
		st := &stubStore{deleteComment: func(_ context.Context, id, caller uuid.UUID) (string, error) {
			if id != cid || caller != me {
				t.Fatalf("delete(%s, %s)", id, caller)
			}
			return "self_delete", nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/comments/"+cid.String(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	// The counter itself is nil-guarded OTel (not asserted here); this
	// pins that whatever outcome the store reports - self_delete or
	// owner_delete - flows through h.count without the handler
	// panicking or otherwise mishandling the string.
	t.Run("delete outcome flows through for both self_delete and owner_delete", func(t *testing.T) {
		for _, outcome := range []string{"self_delete", "owner_delete"} {
			t.Run(outcome, func(t *testing.T) {
				st := &stubStore{deleteComment: func(context.Context, uuid.UUID, uuid.UUID) (string, error) {
					return outcome, nil
				}}
				srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
				resp := do(t, http.MethodDelete, srv.URL+"/comments/"+uuid.New().String(), a.token(t, me.String()), nil)
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("status = %d", resp.StatusCode)
				}
			})
		}
	})

	t.Run("delete store error is 500", func(t *testing.T) {
		st := &stubStore{deleteComment: func(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "", errors.New("boom") }}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/comments/"+uuid.New().String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

func TestUnitCommentsByIds(t *testing.T) {
	me, owner, author := uuid.New(), uuid.New(), uuid.New()
	shelf := uuid.New()

	t.Run("batch hydration", func(t *testing.T) {
		id := uuid.New()
		b := "hi"
		st := &stubStore{commentsByIDs: func(_ context.Context, ids []uuid.UUID) ([]store.Comment, error) {
			if len(ids) != 1 || ids[0] != id {
				t.Fatalf("ids = %v", ids)
			}
			return []store.Comment{{ID: id, ShelfID: shelf, ShelfOwnerID: owner, AuthorID: &author, Body: &b, CreatedAt: time.Now()}}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/comments/by-ids?ids="+id.String(), a.token(t, me.String()), nil)
		var got struct {
			Comments []map[string]any `json:"comments"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || len(got.Comments) != 1 {
			t.Fatalf("status = %d, comments = %+v", resp.StatusCode, got.Comments)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		st := &stubStore{commentsByIDs: func(context.Context, []uuid.UUID) ([]store.Comment, error) {
			return nil, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/comments/by-ids?ids="+uuid.New().String(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("too many ids is a 400 before the store is touched", func(t *testing.T) {
		// api/social.yaml declares maxItems: 100 on ids; the generated
		// param binder does not enforce it, so the handler must reject
		// 101+ entries itself (the empty stubStore proves it).
		q := url.Values{}
		for i := 0; i < 101; i++ {
			q.Add("ids", uuid.New().String())
		}
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/comments/by-ids?"+q.Encode(), a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "too_many_ids")
	})

	t.Run("exactly the max (100 ids) is accepted", func(t *testing.T) {
		q := url.Values{}
		for i := 0; i < 100; i++ {
			q.Add("ids", uuid.New().String())
		}
		st := &stubStore{commentsByIDs: func(_ context.Context, ids []uuid.UUID) ([]store.Comment, error) {
			if len(ids) != 100 {
				t.Fatalf("ids = %d, want 100", len(ids))
			}
			return nil, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/comments/by-ids?"+q.Encode(), a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("100 ids (the yaml max) must be accepted: %d", resp.StatusCode)
		}
	})
}

func TestUnitFeedAndPublish(t *testing.T) {
	me := uuid.New()
	shelf := uuid.New()

	t.Run("feed returns events + next_cursor from the last raw row", func(t *testing.T) {
		e1 := store.Event{ID: uuid.New(), ActorID: uuid.New(), Verb: "liked_shelf", TargetUserID: me, CreatedAt: time.Now()}
		e2 := store.Event{ID: uuid.New(), ActorID: uuid.New(), Verb: "followed_user", TargetUserID: me, CreatedAt: time.Now().Add(-time.Minute)}
		st := &stubStore{feed: func(_ context.Context, viewer uuid.UUID, tab string, cur *store.Cursor, limit int) ([]store.Event, error) {
			if viewer != me || tab != "you" || limit != 20 {
				t.Fatalf("feed(%s, %s, %d)", viewer, tab, limit)
			}
			return []store.Event{e1, e2}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/feed?tab=you", a.token(t, me.String()), nil)
		var got struct {
			Events     []map[string]any `json:"events"`
			NextCursor *string          `json:"next_cursor"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if len(got.Events) != 2 || got.NextCursor == nil {
			t.Fatalf("got = %+v", got)
		}
		want := (store.Cursor{CreatedAt: e2.CreatedAt, ID: e2.ID}).String()
		if *got.NextCursor != want {
			t.Fatalf("next_cursor = %q, want %q", *got.NextCursor, want)
		}
	})

	t.Run("short page omits next_cursor", func(t *testing.T) {
		st := &stubStore{feed: func(context.Context, uuid.UUID, string, *store.Cursor, int) ([]store.Event, error) {
			return []store.Event{}, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/feed?tab=following", a.token(t, me.String()), nil)
		var got struct {
			NextCursor *string `json:"next_cursor"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.NextCursor != nil {
			t.Fatalf("empty page must omit next_cursor")
		}
	})

	t.Run("publish validates and records", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: me, Visibility: "listed"}, nil
		}}
		st := &stubStore{recordPublish: func(_ context.Context, actor, sh uuid.UUID, throttle time.Duration) (string, error) {
			if actor != me || sh != shelf || throttle != time.Hour {
				t.Fatalf("publish(%s, %s, %v)", actor, sh, throttle)
			}
			return "created", nil
		}}
		srv, a := newUnitServer(t, st, col, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/events/shelf-published", a.token(t, me.String()),
			map[string]string{"shelf_id": shelf.String()})
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	// Defense in depth: the bff only ever reaches this endpoint with
	// the shelf owner's own bearer, but the handler itself must not
	// trust that - a caller recording a publish for a shelf they do
	// not own gets the IDENTICAL shelf_not_found 404 a genuinely
	// missing shelf would (never a new oracle for shelf existence),
	// same posture as Follow/Like's owner-visibility gate. &stubStore{}
	// has no recordPublish field, so either case reaching the store
	// would panic loudly.
	t.Run("publish by a non-owner is 404 shelf_not_found (byte-identical to a missing shelf), store untouched", func(t *testing.T) {
		owner := uuid.New()
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
		}}
		srv, a := newUnitServer(t, &stubStore{}, col, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/events/shelf-published", a.token(t, me.String()),
			map[string]string{"shelf_id": shelf.String()})
		gotCode, gotDetail := problemDetail(t, resp)
		if resp.StatusCode != http.StatusNotFound || gotCode != "shelf_not_found" {
			t.Fatalf("status = %d, code = %q", resp.StatusCode, gotCode)
		}

		missing := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrShelfNotFound
		}}
		srv2, a2 := newUnitServer(t, &stubStore{}, missing, &stubUsers{})
		resp2 := do(t, http.MethodPost, srv2.URL+"/events/shelf-published", a2.token(t, me.String()),
			map[string]string{"shelf_id": shelf.String()})
		wantCode, wantDetail := problemDetail(t, resp2)
		if gotCode != wantCode || gotDetail != wantDetail {
			t.Fatalf("non-owner body = (%q, %q), want it to match the shelf-missing body (%q, %q)",
				gotCode, gotDetail, wantCode, wantDetail)
		}
	})

	t.Run("feed rejects a garbage cursor", func(t *testing.T) {
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/feed?tab=you&cursor=garbage", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	})

	t.Run("feed rejects a tab outside the enum", func(t *testing.T) {
		// api/social.yaml enums tab to [following, you]; the generated
		// param binder is a bare string, so the handler must enforce
		// the enum itself before the store is touched.
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/feed?tab=everything", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	})

	t.Run("feed rejects a wrong-case tab", func(t *testing.T) {
		// "Following" is the correct word with the wrong case: the
		// enum check (params.Tab != api.Following && ...) is a plain Go
		// string comparison, not case-folded, so this must be rejected
		// exactly like a nonsense tab value is - a case-insensitive
		// match would silently accept it.
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/feed?tab=Following", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	})

	t.Run("feed limit is clamped by the api bounds", func(t *testing.T) {
		// api/social.yaml declares minimum:1 maximum:50 on limit.
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		for _, limit := range []string{"0", "51"} {
			resp := do(t, http.MethodGet, srv.URL+"/feed?tab=you&limit="+limit, a.token(t, me.String()), nil)
			wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
		}

		st := &stubStore{feed: func(_ context.Context, _ uuid.UUID, _ string, _ *store.Cursor, limit int) ([]store.Event, error) {
			if limit != 50 {
				t.Fatalf("limit = %d, want 50", limit)
			}
			return []store.Event{}, nil
		}}
		srv2, a2 := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv2.URL+"/feed?tab=you&limit=50", a2.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("limit=50 (the yaml max) must be accepted: %d", resp.StatusCode)
		}
	})

	t.Run("feed store error is 500", func(t *testing.T) {
		st := &stubStore{feed: func(context.Context, uuid.UUID, string, *store.Cursor, int) ([]store.Event, error) {
			return nil, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/feed?tab=you", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("publish rejects a malformed or nil shelf id", func(t *testing.T) {
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/events/shelf-published", a.token(t, me.String()),
			map[string]string{"shelf_id": "not-a-uuid"})
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")

		resp2 := do(t, http.MethodPost, srv.URL+"/events/shelf-published", a.token(t, me.String()),
			map[string]string{"shelf_id": uuid.Nil.String()})
		wantProblem(t, resp2, http.StatusBadRequest, "invalid_body")
	})

	t.Run("publish maps collection errors", func(t *testing.T) {
		notFound := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrShelfNotFound
		}}
		srv, a := newUnitServer(t, &stubStore{}, notFound, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/events/shelf-published", a.token(t, me.String()),
			map[string]string{"shelf_id": shelf.String()})
		wantProblem(t, resp, http.StatusNotFound, "shelf_not_found")

		down := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{}, collectionclient.ErrUpstream
		}}
		srv2, a2 := newUnitServer(t, &stubStore{}, down, &stubUsers{})
		resp2 := do(t, http.MethodPost, srv2.URL+"/events/shelf-published", a2.token(t, me.String()),
			map[string]string{"shelf_id": shelf.String()})
		wantProblem(t, resp2, http.StatusBadGateway, "upstream_error")
	})

	t.Run("publish store error is 500", func(t *testing.T) {
		col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
			return collectionclient.Shelf{ID: shelf, OwnerID: me, Visibility: "listed"}, nil
		}}
		st := &stubStore{recordPublish: func(context.Context, uuid.UUID, uuid.UUID, time.Duration) (string, error) {
			return "", errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, col, &stubUsers{})
		resp := do(t, http.MethodPost, srv.URL+"/events/shelf-published", a.token(t, me.String()),
			map[string]string{"shelf_id": shelf.String()})
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

func TestUnitTopAndPurge(t *testing.T) {
	me := uuid.New()

	t.Run("top shelves", func(t *testing.T) {
		ids := []uuid.UUID{uuid.New(), uuid.New()}
		st := &stubStore{topShelves: func(_ context.Context, limit int) ([]uuid.UUID, error) {
			if limit != 50 {
				t.Fatalf("limit = %d", limit)
			}
			return ids, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/explore/top-shelves", a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("purge is self-scoped", func(t *testing.T) {
		var purged uuid.UUID
		st := &stubStore{purgeUser: func(_ context.Context, id uuid.UUID) error { purged = id; return nil }}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/user-data", a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusNoContent || purged != me {
			t.Fatalf("status = %d purged = %s", resp.StatusCode, purged)
		}
	})

	t.Run("top shelves respects an explicit limit", func(t *testing.T) {
		st := &stubStore{topShelves: func(_ context.Context, limit int) ([]uuid.UUID, error) {
			if limit != 5 {
				t.Fatalf("limit = %d, want 5", limit)
			}
			return nil, nil
		}}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/explore/top-shelves?limit=5", a.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("top shelves store error is 500", func(t *testing.T) {
		st := &stubStore{topShelves: func(context.Context, int) ([]uuid.UUID, error) { return nil, errors.New("boom") }}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv.URL+"/explore/top-shelves", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("top shelves limit is clamped by the api bounds", func(t *testing.T) {
		// api/social.yaml declares minimum:1 maximum:50 on limit (the
		// default is 50, unlike the other two limited endpoints' 20).
		srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
		for _, limit := range []string{"0", "51"} {
			resp := do(t, http.MethodGet, srv.URL+"/explore/top-shelves?limit="+limit, a.token(t, me.String()), nil)
			wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
		}

		st := &stubStore{topShelves: func(_ context.Context, limit int) ([]uuid.UUID, error) {
			if limit != 50 {
				t.Fatalf("limit = %d, want 50", limit)
			}
			return nil, nil
		}}
		srv2, a2 := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodGet, srv2.URL+"/explore/top-shelves?limit=50", a2.token(t, me.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("limit=50 (the yaml max) must be accepted: %d", resp.StatusCode)
		}
	})

	t.Run("purge store error is 500", func(t *testing.T) {
		st := &stubStore{purgeUser: func(context.Context, uuid.UUID) error { return errors.New("boom") }}
		srv, a := newUnitServer(t, st, &stubCollection{}, &stubUsers{})
		resp := do(t, http.MethodDelete, srv.URL+"/user-data", a.token(t, me.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

// TestUnitCallerRejectsMalformedSubject covers caller()'s own defensive
// branch: a token can be validly signed (the JWT middleware only checks
// signature/exp/iss/aud) yet carry a subject that is not a user uuid.
// That must 500 cleanly, not panic, and must never reach the store.
func TestUnitCallerRejectsMalformedSubject(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodGet, srv.URL+"/explore/top-shelves", a.token(t, "not-a-uuid"), nil)
	wantProblem(t, resp, http.StatusInternalServerError, "internal")
}
