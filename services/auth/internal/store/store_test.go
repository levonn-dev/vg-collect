package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/services/auth/internal/store"
	"github.com/levonn-dev/vgkeep/services/auth/migrations"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return pgtest.FreshPool(t, migrations.FS, ".")
}

func newTestStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	pool := newTestPool(t)
	return store.New(pool), pool
}

func TestSigningKeys(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i)
	}
	if err := s.RegisterSigningKey(ctx, "kid-1", pub); err != nil {
		t.Fatal(err)
	}
	// Same kid again: idempotent (every replica registers at boot).
	if err := s.RegisterSigningKey(ctx, "kid-1", pub); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	keys, err := s.ActiveSigningKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Kid != "kid-1" {
		t.Fatalf("keys = %+v", keys)
	}
	// The stored encoding IS the JWKS x field: it must round-trip as
	// base64url back to the exact key bytes (std base64 would not).
	decoded, err := base64.RawURLEncoding.DecodeString(keys[0].PublicKeyB64)
	if err != nil || !bytes.Equal(decoded, pub) {
		t.Fatalf("public_key not base64url of the registered key: %q (%v)", keys[0].PublicKeyB64, err)
	}

	// Retired keys drop out of the JWKS.
	if _, err := pool.Exec(ctx,
		`UPDATE signing_keys SET retired_at = now() WHERE kid = 'kid-1'`); err != nil {
		t.Fatal(err)
	}
	keys, err = s.ActiveSigningKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("retired key still served: %+v", keys)
	}
}

func TestAuthStates_SingleUse(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	st := store.AuthState{
		State: "st-1", PKCEVerifier: "ver-1", Nonce: "n-1",
		Provider: "google", ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	if err := s.CreateState(ctx, st); err != nil {
		t.Fatal(err)
	}
	got, err := s.ConsumeState(ctx, "st-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PKCEVerifier != "ver-1" || got.Nonce != "n-1" || got.Provider != "google" {
		t.Fatalf("got = %+v", got)
	}
	// Second consume: the row is gone (single-use).
	if _, err := s.ConsumeState(ctx, "st-1"); !errors.Is(err, store.ErrStateNotFound) {
		t.Fatalf("want ErrStateNotFound, got %v", err)
	}
	if _, err := s.ConsumeState(ctx, "never-existed"); !errors.Is(err, store.ErrStateNotFound) {
		t.Fatalf("want ErrStateNotFound, got %v", err)
	}
}

func TestAuthStates_ExpiredUnusableAndCleanedOnInsert(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateState(ctx, store.AuthState{
		State: "old", PKCEVerifier: "v", Nonce: "n",
		Provider: "google", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE auth_states SET expires_at = now() - interval '1 minute' WHERE state = 'old'`); err != nil {
		t.Fatal(err)
	}
	// Expired state cannot be consumed.
	if _, err := s.ConsumeState(ctx, "old"); !errors.Is(err, store.ErrStateNotFound) {
		t.Fatalf("want ErrStateNotFound for expired, got %v", err)
	}
	// The next insert sweeps expired rows.
	if err := s.CreateState(ctx, store.AuthState{
		State: "new", PKCEVerifier: "v", Nonce: "n",
		Provider: "twitch", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM auth_states WHERE state = 'old'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("expired state not swept on insert")
	}
}

// --- refresh sessions ---

const window = 6 * time.Minute // access TTL + leeway margin, as the handler passes it

func mustCreateSession(t *testing.T, s *store.Store, hash string, user uuid.UUID, jti string) {
	t.Helper()
	if err := s.CreateSession(context.Background(), hash, user, jti,
		time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLifecycle_PeekRotate(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	mustCreateSession(t, s, "h0", user, "jti-0")

	sess, err := s.PeekSession(ctx, "h0")
	if err != nil || sess.UserID != user {
		t.Fatalf("peek = %+v, %v", sess, err)
	}
	if _, err := s.PeekSession(ctx, "missing"); !errors.Is(err, store.ErrRefreshNotFound) {
		t.Fatalf("want ErrRefreshNotFound, got %v", err)
	}

	res, err := s.Rotate(ctx, "h0", "h1", "jti-1", window)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if res.UserID != user || time.Until(res.ExpiresAt) <= 29*24*time.Hour {
		t.Fatalf("res = %+v", res)
	}
	// The child is live and carries the family forward.
	if _, err := s.Rotate(ctx, "h1", "h2", "jti-2", window); err != nil {
		t.Fatalf("rotate child: %v", err)
	}
}

func TestRotate_UnknownAndExpired(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Rotate(ctx, "nope", "x", "j", window); !errors.Is(err, store.ErrRefreshNotFound) {
		t.Fatalf("want ErrRefreshNotFound, got %v", err)
	}

	mustCreateSession(t, s, "h0", uuid.New(), "jti-0")
	if _, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET expires_at = now() - interval '1 second' WHERE token_hash = 'h0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(ctx, "h0", "h1", "jti-1", window); !errors.Is(err, store.ErrRefreshExpired) {
		t.Fatalf("want ErrRefreshExpired, got %v", err)
	}
}

func TestRotate_ReuseRevokesWholeFamilyAndReportsRecentJTIs(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	// Build a chain h0 -> h1 -> h2 (h2 is the live tip).
	mustCreateSession(t, s, "h0", user, "jti-0")
	if _, err := s.Rotate(ctx, "h0", "h1", "jti-1", window); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(ctx, "h1", "h2", "jti-2", window); err != nil {
		t.Fatal(err)
	}

	// Replaying the consumed h0 is reuse: the whole family dies and the
	// recently minted jtis come back for denylisting.
	_, err := s.Rotate(ctx, "h0", "hx", "jti-x", window)
	var reuse *store.ReuseError
	if !errors.As(err, &reuse) {
		t.Fatalf("want ReuseError, got %v", err)
	}
	got := map[string]bool{}
	for _, j := range reuse.RevokedJTIs {
		got[j] = true
	}
	if !got["jti-0"] || !got["jti-1"] || !got["jti-2"] {
		t.Fatalf("RevokedJTIs = %v, want all three", reuse.RevokedJTIs)
	}

	// The live tip died with its family.
	if _, err := s.Rotate(ctx, "h2", "h3", "jti-3", window); !errors.As(err, &reuse) {
		t.Fatalf("tip survived family revocation: %v", err)
	}

	// And no row in the family is left unrevoked.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rows still unrevoked", n)
	}
}

func TestRotate_ReuseReportsOnlyRecentJTIs(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	mustCreateSession(t, s, "h0", uuid.New(), "jti-0")
	if _, err := s.Rotate(ctx, "h0", "h1", "jti-1", window); err != nil {
		t.Fatal(err)
	}
	// Age h0 past the window: its access token is long dead, so its jti
	// is not worth denylisting.
	if _, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET created_at = now() - interval '1 hour' WHERE token_hash = 'h0'`); err != nil {
		t.Fatal(err)
	}
	_, err := s.Rotate(ctx, "h0", "hx", "jti-x", window)
	var reuse *store.ReuseError
	if !errors.As(err, &reuse) {
		t.Fatalf("want ReuseError, got %v", err)
	}
	if len(reuse.RevokedJTIs) != 1 || reuse.RevokedJTIs[0] != "jti-1" {
		t.Fatalf("RevokedJTIs = %v, want [jti-1]", reuse.RevokedJTIs)
	}
}

func TestRevokeFamilyByToken(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	mustCreateSession(t, s, "h0", uuid.New(), "jti-0")
	if _, err := s.Rotate(ctx, "h0", "h1", "jti-1", window); err != nil {
		t.Fatal(err)
	}
	// Logout presents the live token; any family member works.
	if err := s.RevokeFamilyByToken(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	var reuse *store.ReuseError
	if _, err := s.Rotate(ctx, "h1", "h2", "jti-2", window); !errors.As(err, &reuse) {
		t.Fatalf("refresh after logout should hit the revoked path, got %v", err)
	}
	// Unknown token: idempotent no-op (logout never fails).
	if err := s.RevokeFamilyByToken(ctx, "unknown"); err != nil {
		t.Fatal(err)
	}
}

func TestSessions_AreIndependentFamilies(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	mustCreateSession(t, s, "ha", user, "jti-a") // e.g. browser login
	mustCreateSession(t, s, "hb", user, "jti-b") // e.g. second device

	if err := s.RevokeFamilyByToken(ctx, "ha"); err != nil {
		t.Fatal(err)
	}
	// The other session is untouched.
	if _, err := s.Rotate(ctx, "hb", "hb1", "jti-b1", window); err != nil {
		t.Fatalf("independent family was harmed: %v", err)
	}
}

func TestRotate_ConcurrentSameToken_SingleUse(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	mustCreateSession(t, s, "h0", uuid.New(), "jti-0")

	start := make(chan struct{})
	type outcome struct {
		newJTI string
		err    error
	}
	results := make(chan outcome, 2)
	for _, attempt := range []struct{ hash, jti string }{{"hA", "jA"}, {"hB", "jB"}} {
		go func(hash, jti string) {
			<-start
			_, err := s.Rotate(ctx, "h0", hash, jti, window)
			results <- outcome{newJTI: jti, err: err}
		}(attempt.hash, attempt.jti)
	}
	close(start)
	a, b := <-results, <-results

	var winner outcome
	var reuse *store.ReuseError
	switch {
	case a.err == nil && errors.As(b.err, &reuse):
		winner = a
	case b.err == nil && errors.As(a.err, &reuse):
		winner = b
	default:
		t.Fatalf("want exactly one success and one ReuseError, got %v / %v", a.err, b.err)
	}

	// Concurrent presentation of one token IS reuse: the loser's
	// detection must have revoked everything, including the winner's
	// fresh child, and reported its jti for denylisting.
	got := map[string]bool{}
	for _, j := range reuse.RevokedJTIs {
		got[j] = true
	}
	if !got["jti-0"] || !got[winner.newJTI] {
		t.Fatalf("RevokedJTIs = %v, want jti-0 and %s", reuse.RevokedJTIs, winner.newJTI)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rows survived concurrent-reuse detection", n)
	}
}

// rawRotate mirrors Rotate's statements on a caller-held transaction,
// simulating another replica mid-rotation (lock taken, child inserted,
// commit deferred to the caller).
func rawRotate(t *testing.T, ctx context.Context, tx pgx.Tx, parentHash, childHash, childJTI string) {
	t.Helper()
	var familyID uuid.UUID
	var userID uuid.UUID
	var expiresAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT family_id, user_id, expires_at
		FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, parentHash,
	).Scan(&familyID, &userID, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET used_at = now() WHERE token_hash = $1`, parentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens
			(token_hash, parent_hash, family_id, user_id, last_access_jti, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		childHash, parentHash, familyID, userID, childJTI, expiresAt); err != nil {
		t.Fatal(err)
	}
}

func TestRotate_ReuseRacingLiveRotation_NoSurvivors(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	mustCreateSession(t, s, "h0", uuid.New(), "jti-0")
	if _, err := s.Rotate(ctx, "h0", "h1", "jti-1", window); err != nil {
		t.Fatal(err)
	}

	// Another replica is mid-rotation of the live tip h1: child h2
	// inserted but not yet committed.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rawRotate(t, ctx, tx, "h1", "h2", "jti-2")

	// Replaying the consumed h0 now must block on the family lock,
	// then catch h2 once the in-flight rotation commits.
	done := make(chan error, 1)
	go func() {
		_, err := s.Rotate(ctx, "h0", "hx", "jti-x", window)
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var reuse *store.ReuseError
	if err := <-done; !errors.As(err, &reuse) {
		t.Fatalf("want ReuseError, got %v", err)
	}
	got := map[string]bool{}
	for _, j := range reuse.RevokedJTIs {
		got[j] = true
	}
	if !got["jti-2"] {
		t.Fatalf("RevokedJTIs = %v, must include the racing child's jti-2", reuse.RevokedJTIs)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rows survived reuse detection (phantom child)", n)
	}
	if _, err := s.Rotate(ctx, "h2", "h3", "jti-3", window); !errors.As(err, &reuse) {
		t.Fatalf("phantom child h2 still rotatable: %v", err)
	}
}

func TestRevokeFamily_RacingLiveRotation_NoSurvivors(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	mustCreateSession(t, s, "h0", uuid.New(), "jti-0")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rawRotate(t, ctx, tx, "h0", "h1", "jti-1")

	done := make(chan error, 1)
	go func() { done <- s.RevokeFamilyByToken(ctx, "h0") }()
	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rows survived logout (phantom child)", n)
	}
}

func TestCreateSession_SweepsDeadRowsPastRetention(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	mustCreateSession(t, s, "dead-old", uuid.New(), "jti-dead-old")
	if err := s.RevokeFamilyByToken(ctx, "dead-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET expires_at = now() - interval '31 days' WHERE token_hash = 'dead-old'`); err != nil {
		t.Fatal(err)
	}

	// Any login insert runs the sweep first.
	mustCreateSession(t, s, "trigger", uuid.New(), "jti-trigger")

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE token_hash = 'dead-old'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("dead row past retention survived the sweep")
	}
}

func TestCreateSession_SweepPreservesLiveTipAndRecentDeadRows(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	// Chain h0 -> h1 -> h2; h2 is the live tip, h0 and h1 are dead
	// (used) by rotation, all three sharing one far-future family
	// expiry until the direct UPDATE below ages h0 alone.
	mustCreateSession(t, s, "h0", user, "jti-0")
	if _, err := s.Rotate(ctx, "h0", "h1", "jti-1", window); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(ctx, "h1", "h2", "jti-2", window); err != nil {
		t.Fatal(err)
	}

	// Only h0 ages past the retention window; h1 and h2 keep the
	// family's real (far-future) expiry.
	if _, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET expires_at = now() - interval '31 days' WHERE token_hash = 'h0'`); err != nil {
		t.Fatal(err)
	}

	mustCreateSession(t, s, "trigger", uuid.New(), "jti-trigger")

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE token_hash = 'h0'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("stale dead ancestor past retention survived the sweep")
	}

	// h1 is dead but recent (not past retention): it survives as both
	// a live descendant's parent and the swept row's child, and its
	// parent_hash link to h0 nulls out via the FK instead of the
	// sweep's DELETE failing.
	var parentHash *string
	if err := pool.QueryRow(ctx,
		`SELECT parent_hash FROM refresh_tokens WHERE token_hash = 'h1'`).Scan(&parentHash); err != nil {
		t.Fatalf("h1 (recent dead parent) did not survive: %v", err)
	}
	if parentHash != nil {
		t.Fatalf("h1.parent_hash = %v, want NULL after its parent was swept", *parentHash)
	}

	// The live tip survives untouched by the sweep.
	if _, err := s.Rotate(ctx, "h2", "h3", "jti-3", window); err != nil {
		t.Fatalf("live tip did not survive the sweep: %v", err)
	}
}

func TestCreateSession_SweepPreservesRecentlyDeadRows(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	mustCreateSession(t, s, "dead-recent", uuid.New(), "jti-dead-recent")
	if err := s.RevokeFamilyByToken(ctx, "dead-recent"); err != nil {
		t.Fatal(err)
	}
	// Expired, but well within the 30-day retention window.
	if _, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET expires_at = now() - interval '1 day' WHERE token_hash = 'dead-recent'`); err != nil {
		t.Fatal(err)
	}

	mustCreateSession(t, s, "trigger", uuid.New(), "jti-trigger")

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE token_hash = 'dead-recent'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("recently dead row inside the retention window was swept")
	}
}

// --- identity linking ---

func TestResolveIdentity_FoundRefreshesEmailAndNotFound(t *testing.T) {
	s := store.New(newTestPool(t))
	ctx := context.Background()
	userID := uuid.New()

	if _, err := s.ResolveIdentity(ctx, "google", "sub-1", "a@example.com"); !errors.Is(err, store.ErrIdentityNotFound) {
		t.Fatalf("want ErrIdentityNotFound, got %v", err)
	}

	if err := s.BindIdentity(ctx, "google", "sub-1", "a@example.com", userID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	got, err := s.ResolveIdentity(ctx, "google", "sub-1", "a2@example.com")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.UserID != userID || got.Email == nil || *got.Email != "a2@example.com" {
		t.Fatalf("resolve = %+v", got)
	}
	if got.ID == uuid.Nil || got.Provider != "google" || got.Subject != "sub-1" {
		t.Fatalf("identity fields = %+v", got)
	}
}

func TestBindIdentity_InsertOnlySemantics(t *testing.T) {
	s := store.New(newTestPool(t))
	ctx := context.Background()
	owner, other := uuid.New(), uuid.New()

	if err := s.BindIdentity(ctx, "dev", "dev-x", "x@example.com", owner); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	// Same user again: idempotent, refreshes email.
	if err := s.BindIdentity(ctx, "dev", "dev-x", "x2@example.com", owner); err != nil {
		t.Fatalf("rebind same user: %v", err)
	}
	got, err := s.ResolveIdentity(ctx, "dev", "dev-x", "x2@example.com")
	if err != nil || got.UserID != owner {
		t.Fatalf("resolve after idempotent bind: %+v %v", got, err)
	}
	// Different user: rejected, mapping unchanged.
	if err := s.BindIdentity(ctx, "dev", "dev-x", "x@example.com", other); !errors.Is(err, store.ErrIdentityTaken) {
		t.Fatalf("want ErrIdentityTaken, got %v", err)
	}
}

func TestRebindIdentity_MovesOwnershipAndInsertsWhenAbsent(t *testing.T) {
	s := store.New(newTestPool(t))
	ctx := context.Background()
	dead, next := uuid.New(), uuid.New()

	if err := s.RebindIdentity(ctx, "google", "sub-r", "r@example.com", dead); err != nil {
		t.Fatalf("rebind-as-insert: %v", err)
	}
	if err := s.RebindIdentity(ctx, "google", "sub-r", "r@example.com", next); err != nil {
		t.Fatalf("rebind-as-move: %v", err)
	}
	got, err := s.ResolveIdentity(ctx, "google", "sub-r", "r@example.com")
	if err != nil || got.UserID != next {
		t.Fatalf("after rebind: %+v %v", got, err)
	}
}

func TestListIdentities_ScopedAndOrdered(t *testing.T) {
	s := store.New(newTestPool(t))
	ctx := context.Background()
	u1, u2 := uuid.New(), uuid.New()
	if err := s.BindIdentity(ctx, "dev", "dev-a", "a@example.com", u1); err != nil {
		t.Fatal(err)
	}
	if err := s.BindIdentity(ctx, "google", "g-a", "a@gmail.example", u1); err != nil {
		t.Fatal(err)
	}
	if err := s.BindIdentity(ctx, "dev", "dev-b", "b@example.com", u2); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ListIdentities(ctx, u1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 2 || ids[0].Provider != "dev" || ids[1].Provider != "google" {
		t.Fatalf("list = %+v", ids)
	}
	empty, err := s.ListIdentities(ctx, uuid.New())
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown user list = %+v %v", empty, err)
	}
}

func TestDeleteIdentity_GuardsLastAndOwnership(t *testing.T) {
	s := store.New(newTestPool(t))
	ctx := context.Background()
	u1, u2 := uuid.New(), uuid.New()
	if err := s.BindIdentity(ctx, "dev", "dev-1", "1@example.com", u1); err != nil {
		t.Fatal(err)
	}
	if err := s.BindIdentity(ctx, "google", "g-1", "1@gmail.example", u1); err != nil {
		t.Fatal(err)
	}
	if err := s.BindIdentity(ctx, "dev", "dev-2", "2@example.com", u2); err != nil {
		t.Fatal(err)
	}
	mine, _ := s.ListIdentities(ctx, u1)
	theirs, _ := s.ListIdentities(ctx, u2)

	// Someone else's identity id: not found (no oracle about existence).
	if err := s.DeleteIdentity(ctx, u1, theirs[0].ID); !errors.Is(err, store.ErrIdentityNotFound) {
		t.Fatalf("cross-user delete: want ErrIdentityNotFound, got %v", err)
	}
	if err := s.DeleteIdentity(ctx, u1, mine[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	left, _ := s.ListIdentities(ctx, u1)
	if len(left) != 1 {
		t.Fatalf("left = %+v", left)
	}
	if err := s.DeleteIdentity(ctx, u1, left[0].ID); !errors.Is(err, store.ErrLastIdentity) {
		t.Fatalf("want ErrLastIdentity, got %v", err)
	}
}

func TestDeleteUserAuth_WipesIdentitiesAndRevokesFamilies(t *testing.T) {
	s := store.New(newTestPool(t))
	ctx := context.Background()
	u := uuid.New()
	if err := s.BindIdentity(ctx, "dev", "dev-w", "w@example.com", u); err != nil {
		t.Fatal(err)
	}
	if err := s.BindIdentity(ctx, "google", "g-w", "w@gmail.example", u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "hash-w1", u, "jti-w1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUserAuth(ctx, u); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	ids, _ := s.ListIdentities(ctx, u)
	if len(ids) != 0 {
		t.Fatalf("identities survived: %+v", ids)
	}
	if _, err := s.PeekSession(ctx, "hash-w1"); !errors.Is(err, store.ErrRefreshRevoked) {
		t.Fatalf("want ErrRefreshRevoked, got %v", err)
	}
	// Idempotent.
	if err := s.DeleteUserAuth(ctx, u); err != nil {
		t.Fatalf("second wipe: %v", err)
	}
}

func TestState_CarriesLinkUserID(t *testing.T) {
	s := store.New(newTestPool(t))
	ctx := context.Background()
	link := uuid.New()

	if err := s.CreateState(ctx, store.AuthState{
		State: "st-link", PKCEVerifier: "v", Nonce: "n", Provider: "google",
		ExpiresAt: time.Now().Add(time.Minute), LinkUserID: &link,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ConsumeState(ctx, "st-link")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.LinkUserID == nil || *got.LinkUserID != link {
		t.Fatalf("LinkUserID = %v", got.LinkUserID)
	}

	if err := s.CreateState(ctx, store.AuthState{
		State: "st-login", PKCEVerifier: "v", Nonce: "n", Provider: "google",
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.ConsumeState(ctx, "st-login")
	if err != nil {
		t.Fatal(err)
	}
	if got.LinkUserID != nil {
		t.Fatalf("login state LinkUserID = %v", got.LinkUserID)
	}
}
