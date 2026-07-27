package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/social/internal/store"
	"github.com/levonn-dev/vg-collect/services/social/migrations"
)

// newTestStore duplicates the fixture in migrations/migrations_test.go
// (Go test packages can't share helpers across packages).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("social"), tcpostgres.WithUsername("s"), tcpostgres.WithPassword("p"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool)
}

// poolOf exposes the pool for the raw-SQL assertions below.
func poolOf(t *testing.T, s *store.Store) *pgxpool.Pool {
	t.Helper()
	return s.Pool()
}

func TestFollow_IdempotentCapAndEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()

	ins, err := s.Follow(ctx, a, b, 100)
	if err != nil || !ins {
		t.Fatalf("first follow: ins=%v err=%v", ins, err)
	}
	// Idempotent retry: no second edge, and the caller knows not to
	// re-emit the event.
	ins, err = s.Follow(ctx, a, b, 100)
	if err != nil || ins {
		t.Fatalf("retry follow: ins=%v err=%v", ins, err)
	}
	followers, following, viewerFollows, err := s.ProfileSummary(ctx, b, a)
	if err != nil || followers != 1 || following != 0 || !viewerFollows {
		t.Fatalf("summary = %d/%d/%v/%v", followers, following, viewerFollows, err)
	}
	// Cap: a fresh follower with cap 1 hits the wall on the second.
	c := uuid.New()
	if _, err := s.Follow(ctx, c, a, 1); err != nil {
		t.Fatalf("c follows a: %v", err)
	}
	if _, err := s.Follow(ctx, c, b, 1); !errors.Is(err, store.ErrCapExceeded) {
		t.Fatalf("cap err = %v", err)
	}
	if err := s.Unfollow(ctx, a, b); err != nil {
		t.Fatalf("unfollow: %v", err)
	}
	if _, _, viewerFollows, _ = s.ProfileSummary(ctx, b, a); viewerFollows {
		t.Fatal("unfollow did not stick")
	}
}

// TestFollow_CapBoundaryIdempotentRetry is the regression case for the
// cap-before-conflict ordering bug: retrying an edge already held must
// never be charged against the cap, even when the cap is fully spent.
func TestFollow_CapBoundaryIdempotentRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, b1, b2, b3 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	const followCap = 2

	// Drive a to exactly the cap with two genuinely new edges.
	if ins, err := s.Follow(ctx, a, b1, followCap); err != nil || !ins {
		t.Fatalf("follow b1: ins=%v err=%v", ins, err)
	}
	if ins, err := s.Follow(ctx, a, b2, followCap); err != nil || !ins {
		t.Fatalf("follow b2: ins=%v err=%v", ins, err)
	}

	// Retrying an edge already held must succeed as a no-op even
	// though the cap is fully spent: a retry is never charged.
	ins, err := s.Follow(ctx, a, b1, followCap)
	if err != nil || ins {
		t.Fatalf("retry at cap: ins=%v err=%v", ins, err)
	}
	var events int
	if err := poolOf(t, s).QueryRow(ctx,
		`SELECT count(*) FROM activity WHERE actor_id = $1 AND verb = 'followed_user' AND target_user_id = $2`,
		a, b1).Scan(&events); err != nil || events != 1 {
		t.Fatalf("b1 event count = %d, %v (want 1)", events, err)
	}

	// A genuinely new edge at the same moment still hits the cap.
	if _, err := s.Follow(ctx, a, b3, followCap); !errors.Is(err, store.ErrCapExceeded) {
		t.Fatalf("new edge at cap err = %v", err)
	}
}

func TestFollow_FolloweeIDsMembership(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, b, c, x, y := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	if _, err := s.Follow(ctx, a, b, 100); err != nil {
		t.Fatalf("a follows b: %v", err)
	}
	if _, err := s.Follow(ctx, a, c, 100); err != nil {
		t.Fatalf("a follows c: %v", err)
	}
	// Unrelated edge must not leak into a's followee set.
	if _, err := s.Follow(ctx, x, y, 100); err != nil {
		t.Fatalf("x follows y: %v", err)
	}

	ids, err := s.FolloweeIDs(ctx, a)
	if err != nil || len(ids) != 2 {
		t.Fatalf("followees = %v, %v", ids, err)
	}
	byID := map[uuid.UUID]bool{}
	for _, id := range ids {
		byID[id] = true
	}
	if !byID[b] || !byID[c] {
		t.Fatalf("followees missing members: %v", ids)
	}

	none, err := s.FolloweeIDs(ctx, uuid.New())
	if err != nil || len(none) != 0 {
		t.Fatalf("empty followees = %v, %v", none, err)
	}
}

func TestLike_SummariesAndTop(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner, fan1, fan2 := uuid.New(), uuid.New(), uuid.New()
	shelf1, shelf2 := uuid.New(), uuid.New()

	mustLike := func(u, sh uuid.UUID) {
		t.Helper()
		if _, err := s.Like(ctx, u, sh, owner, 200); err != nil {
			t.Fatalf("like: %v", err)
		}
	}
	mustLike(fan1, shelf1)
	mustLike(fan2, shelf1)
	mustLike(fan1, shelf2)

	sums, err := s.ShelfSummaries(ctx, []uuid.UUID{shelf1, shelf2, uuid.New()}, fan1)
	if err != nil || len(sums) != 3 {
		t.Fatalf("summaries = %+v, %v", sums, err)
	}
	byID := map[uuid.UUID]store.ShelfSummary{}
	for _, x := range sums {
		byID[x.ShelfID] = x
	}
	if byID[shelf1].LikeCount != 2 || !byID[shelf1].ViewerLikes {
		t.Fatalf("shelf1 = %+v", byID[shelf1])
	}
	if byID[shelf2].LikeCount != 1 {
		t.Fatalf("shelf2 = %+v", byID[shelf2])
	}
	// Unknown shelf answers zeroed, not absent.
	top, err := s.TopShelves(ctx, 50)
	if err != nil || len(top) != 2 || top[0] != shelf1 {
		t.Fatalf("top = %v, %v", top, err)
	}
	if err := s.Unlike(ctx, fan2, shelf1); err != nil {
		t.Fatalf("unlike: %v", err)
	}
}

// TestLike_CapBoundaryIdempotentRetry mirrors
// TestFollow_CapBoundaryIdempotentRetry for the like edge.
func TestLike_CapBoundaryIdempotentRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner, user := uuid.New(), uuid.New()
	shelf1, shelf2, shelf3 := uuid.New(), uuid.New(), uuid.New()
	const likeCap = 2

	if ins, err := s.Like(ctx, user, shelf1, owner, likeCap); err != nil || !ins {
		t.Fatalf("like shelf1: ins=%v err=%v", ins, err)
	}
	if ins, err := s.Like(ctx, user, shelf2, owner, likeCap); err != nil || !ins {
		t.Fatalf("like shelf2: ins=%v err=%v", ins, err)
	}

	// Retrying an already-held like at a spent cap must be a no-op.
	ins, err := s.Like(ctx, user, shelf1, owner, likeCap)
	if err != nil || ins {
		t.Fatalf("retry at cap: ins=%v err=%v", ins, err)
	}
	var events int
	if err := poolOf(t, s).QueryRow(ctx,
		`SELECT count(*) FROM activity WHERE actor_id = $1 AND verb = 'liked_shelf' AND object_shelf_id = $2`,
		user, shelf1).Scan(&events); err != nil || events != 1 {
		t.Fatalf("shelf1 event count = %d, %v (want 1)", events, err)
	}

	// A genuinely new like at the same moment still hits the cap.
	if _, err := s.Like(ctx, user, shelf3, owner, likeCap); !errors.Is(err, store.ErrCapExceeded) {
		t.Fatalf("new like at cap err = %v", err)
	}
}

func TestComments_LifecycleAndCaps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner, author, stranger := uuid.New(), uuid.New(), uuid.New()
	shelf := uuid.New()

	c1, err := s.CreateComment(ctx, shelf, owner, author, "first!", 50)
	if err != nil || c1.Body == nil || *c1.Body != "first!" {
		t.Fatalf("create = %+v, %v", c1, err)
	}
	c2, _ := s.CreateComment(ctx, shelf, owner, author, "second", 50)

	// Stranger cannot delete; outcome is empty whenever err is non-nil.
	if outcome, err := s.DeleteComment(ctx, c1.ID, stranger); !errors.Is(err, store.ErrForbidden) || outcome != "" {
		t.Fatalf("stranger delete outcome=%q err=%v", outcome, err)
	}

	// Author self-delete: tombstone, body NULLed, outcome self_delete.
	outcome, err := s.DeleteComment(ctx, c1.ID, author)
	if err != nil || outcome != "self_delete" {
		t.Fatalf("self delete: outcome=%q err=%v", outcome, err)
	}
	// Owner removal: tombstone, body RETAINED, outcome owner_delete.
	outcome, err = s.DeleteComment(ctx, c2.ID, owner)
	if err != nil || outcome != "owner_delete" {
		t.Fatalf("owner delete: outcome=%q err=%v", outcome, err)
	}
	// Both are gone from live reads.
	live, err := s.ListLiveComments(ctx, shelf, nil, 20)
	if err != nil || len(live) != 0 {
		t.Fatalf("live = %+v, %v", live, err)
	}
	// Deleting a tombstone: not found, outcome empty.
	if outcome, err := s.DeleteComment(ctx, c1.ID, author); !errors.Is(err, store.ErrNotFound) || outcome != "" {
		t.Fatalf("double delete outcome=%q err=%v", outcome, err)
	}
	// The cap counts tombstones: cap 2 is already burned.
	if _, err := s.CreateComment(ctx, shelf, owner, author, "third", 2); !errors.Is(err, store.ErrCapExceeded) {
		t.Fatalf("cap err = %v", err)
	}
	// Verify the tombstone body rules directly.
	var selfBody, ownerBody *string
	pool := poolOf(t, s)
	if err := pool.QueryRow(ctx, `SELECT body FROM comments WHERE id = $1`, c1.ID).Scan(&selfBody); err != nil || selfBody != nil {
		t.Fatalf("self-deleted body = %v, %v (want NULL)", selfBody, err)
	}
	if err := pool.QueryRow(ctx, `SELECT body FROM comments WHERE id = $1`, c2.ID).Scan(&ownerBody); err != nil || ownerBody == nil || *ownerBody != "second" {
		t.Fatalf("removed body = %v, %v (want retained)", ownerBody, err)
	}
}

func TestComments_CursorPaging(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner, author, shelf := uuid.New(), uuid.New(), uuid.New()
	for i := 0; i < 5; i++ {
		if _, err := s.CreateComment(ctx, shelf, owner, author, fmt.Sprintf("c%d", i), 50); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	page1, err := s.ListLiveComments(ctx, shelf, nil, 2)
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1 = %d, %v", len(page1), err)
	}
	cur := &store.Cursor{CreatedAt: page1[1].CreatedAt, ID: page1[1].ID}
	page2, err := s.ListLiveComments(ctx, shelf, cur, 2)
	if err != nil || len(page2) != 2 {
		t.Fatalf("page2 = %d, %v", len(page2), err)
	}
	if page2[0].ID == page1[1].ID {
		t.Fatal("cursor did not advance")
	}
	// Round-trip the encoding.
	parsed, err := store.ParseCursor(cur.String())
	if err != nil || !parsed.CreatedAt.Equal(cur.CreatedAt) || parsed.ID != cur.ID {
		t.Fatalf("cursor round trip: %+v, %v", parsed, err)
	}
	if _, err := store.ParseCursor("garbage"); err == nil {
		t.Fatal("garbage cursor must error")
	}
}

func TestComments_LiveByIDsFiltersDeleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner, author, other := uuid.New(), uuid.New(), uuid.New()
	shelf := uuid.New()

	live, err := s.CreateComment(ctx, shelf, owner, author, "still here", 50)
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	selfDeleted, err := s.CreateComment(ctx, shelf, owner, author, "self gone", 50)
	if err != nil {
		t.Fatalf("create self-deleted: %v", err)
	}
	ownerRemoved, err := s.CreateComment(ctx, shelf, owner, other, "owner gone", 50)
	if err != nil {
		t.Fatalf("create owner-removed: %v", err)
	}
	if _, err := s.DeleteComment(ctx, selfDeleted.ID, author); err != nil {
		t.Fatalf("self delete: %v", err)
	}
	if _, err := s.DeleteComment(ctx, ownerRemoved.ID, owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}

	got, err := s.LiveCommentsByIDs(ctx, []uuid.UUID{live.ID, selfDeleted.ID, ownerRemoved.ID})
	if err != nil || len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("live by ids = %+v, %v", got, err)
	}
}

func TestActivity_FeedTabsAndUndo(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	alice, bob, carol := uuid.New(), uuid.New(), uuid.New()
	shelf := uuid.New()

	// carol follows alice; alice likes bob's shelf; alice follows bob.
	if _, err := s.Follow(ctx, carol, alice, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Like(ctx, alice, shelf, bob, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Follow(ctx, alice, bob, 100); err != nil {
		t.Fatal(err)
	}

	// carol's following feed sees alice's two events, newest first.
	feed, err := s.Feed(ctx, carol, "following", nil, 20)
	if err != nil || len(feed) != 2 {
		t.Fatalf("following = %+v, %v", feed, err)
	}
	if feed[0].Verb != "followed_user" || feed[1].Verb != "liked_shelf" {
		t.Fatalf("order = %s, %s", feed[0].Verb, feed[1].Verb)
	}

	// bob's you-tab sees both events targeting him.
	you, err := s.Feed(ctx, bob, "you", nil, 20)
	if err != nil || len(you) != 2 {
		t.Fatalf("you = %+v, %v", you, err)
	}

	// Undo: unlike removes the like event.
	if err := s.Unlike(ctx, alice, shelf); err != nil {
		t.Fatal(err)
	}
	feed, _ = s.Feed(ctx, carol, "following", nil, 20)
	if len(feed) != 1 || feed[0].Verb != "followed_user" {
		t.Fatalf("post-unlike feed = %+v", feed)
	}
}

func TestRecordPublish_UpsertAndThrottle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner, shelf := uuid.New(), uuid.New()

	out, err := s.RecordPublish(ctx, owner, shelf, time.Hour)
	if err != nil || out != "created" {
		t.Fatalf("first = %q, %v", out, err)
	}
	// Within the throttle window: no refresh.
	out, err = s.RecordPublish(ctx, owner, shelf, time.Hour)
	if err != nil || out != "throttled" {
		t.Fatalf("second = %q, %v", out, err)
	}
	// Zero throttle: refresh (feed bump) and still exactly one row.
	out, err = s.RecordPublish(ctx, owner, shelf, 0)
	if err != nil || out != "refreshed" {
		t.Fatalf("third = %q, %v", out, err)
	}
	var n int
	if err := poolOf(t, s).QueryRow(ctx,
		`SELECT count(*) FROM activity WHERE verb = 'published_shelf' AND object_shelf_id = $1`, shelf).Scan(&n); err != nil || n != 1 {
		t.Fatalf("publish rows = %d, %v", n, err)
	}
}

func TestPurgeUser_AnonymizeAndDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	leaver, other := uuid.New(), uuid.New()
	leaverShelf, otherShelf := uuid.New(), uuid.New()

	// leaver's graph: follows both ways, likes both ways, comments in
	// both directions, activity everywhere.
	_, _ = s.Follow(ctx, leaver, other, 100)
	_, _ = s.Follow(ctx, other, leaver, 100)
	_, _ = s.Like(ctx, leaver, otherShelf, other, 200)
	_, _ = s.Like(ctx, other, leaverShelf, leaver, 200)
	authored, _ := s.CreateComment(ctx, otherShelf, other, leaver, "leaver on other", 50)
	received, _ := s.CreateComment(ctx, leaverShelf, leaver, other, "other on leaver", 50)
	// A removed comment by other on other's shelf, removed BY leaver?
	// Not possible (leaver is not that owner) - instead: other's
	// comment on leaver's shelf removed by leaver, to exercise the
	// deleted_by NULLing on rows that get hard-deleted anyway, plus a
	// third-party row: other authored on other's own... keep it to the
	// two shelves; the deleted_by case is covered by removing
	// 'received' below.
	_, _ = s.DeleteComment(ctx, received.ID, leaver) // owner-removal: deleted_by = leaver

	if err := s.PurgeUser(ctx, leaver); err != nil {
		t.Fatalf("purge: %v", err)
	}

	pool := poolOf(t, s)
	assertCount := func(q string, want int, args ...any) {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil || n != want {
			t.Fatalf("%s = %d (err %v), want %d", q, n, err, want)
		}
	}
	assertCount(`SELECT count(*) FROM follows WHERE follower_id = $1 OR followee_id = $1`, 0, leaver)
	assertCount(`SELECT count(*) FROM likes WHERE user_id = $1 OR shelf_owner_id = $1`, 0, leaver)
	assertCount(`SELECT count(*) FROM activity WHERE actor_id = $1 OR target_user_id = $1`, 0, leaver)
	// Comments on leaver's own shelves: hard-deleted (incl. the tombstone).
	assertCount(`SELECT count(*) FROM comments WHERE shelf_owner_id = $1`, 0, leaver)
	// Authored on a surviving shelf: anonymized in place.
	var authorID, body *string
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT author_id::text, body, deleted_at FROM comments WHERE id = $1`, authored.ID).
		Scan(&authorID, &body, &deletedAt); err != nil {
		t.Fatalf("read anonymized: %v", err)
	}
	if authorID != nil || body != nil || deletedAt == nil {
		t.Fatalf("anonymized = author %v body %v deleted %v", authorID, body, deletedAt)
	}
	// Idempotent re-run.
	if err := s.PurgeUser(ctx, leaver); err != nil {
		t.Fatalf("re-purge: %v", err)
	}
}

// TestPurgeUser_PreservesOtherModeratorAttribution pins the
// authored-elsewhere anonymize statement's deleted_by handling: a
// comment the purge target authored on someone else's shelf, which
// that shelf owner had already REMOVED before the purge, must keep
// the owner's id in deleted_by afterward - only author_id and body
// are erased. Nulling deleted_by unconditionally there would erase
// the owner's own moderation action; the owner is not who is being
// purged.
func TestPurgeUser_PreservesOtherModeratorAttribution(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	leaver, owner := uuid.New(), uuid.New()
	ownerShelf := uuid.New()

	authored, err := s.CreateComment(ctx, ownerShelf, owner, leaver, "leaver on owner's shelf", 50)
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	// The shelf owner (not the author) removes it before the purge:
	// deleted_by = owner, body retained per the lifecycle.
	if _, err := s.DeleteComment(ctx, authored.ID, owner); err != nil {
		t.Fatalf("owner removal: %v", err)
	}

	if err := s.PurgeUser(ctx, leaver); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var authorID, body, deletedBy *string
	var deletedAt *time.Time
	if err := poolOf(t, s).QueryRow(ctx,
		`SELECT author_id::text, body, deleted_at, deleted_by::text FROM comments WHERE id = $1`, authored.ID).
		Scan(&authorID, &body, &deletedAt, &deletedBy); err != nil {
		t.Fatalf("read purged row: %v", err)
	}
	if authorID != nil || body != nil {
		t.Fatalf("purge must erase author_id and body: author=%v body=%v", authorID, body)
	}
	if deletedAt == nil {
		t.Fatal("deleted_at must stay set")
	}
	if deletedBy == nil || *deletedBy != owner.String() {
		t.Fatalf("deleted_by = %v, want the owner's id %s (moderation attribution must survive purge)", deletedBy, owner)
	}
}
