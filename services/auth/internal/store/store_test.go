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
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/auth/internal/store"
	"github.com/levonn-dev/vg-collect/services/auth/migrations"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("auth"), tcpostgres.WithUsername("a"), tcpostgres.WithPassword("p"),
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
	return pool
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

func TestBindIdentity_InsertAndRebind(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	u1, u2 := uuid.New(), uuid.New()
	if err := s.BindIdentity(ctx, "google", "sub-1", u1); err != nil {
		t.Fatal(err)
	}
	// The provider account follows its current verified email: a later
	// login that upserts to a different user rebinds the identity.
	if err := s.BindIdentity(ctx, "google", "sub-1", u2); err != nil {
		t.Fatal(err)
	}
	var got uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM identities WHERE provider = 'google' AND provider_subject = 'sub-1'`).
		Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != u2 {
		t.Fatalf("user_id = %s, want %s", got, u2)
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
