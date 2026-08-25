package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
	"github.com/levonn-dev/vgkeep/services/user/migrations"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	return store.New(pgtest.FreshPool(t, migrations.FS, "."))
}

func TestUpsert_FillsOnCreateOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u1, created, err := s.Upsert(ctx, "a@example.com", "Alice", nil, "USD")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !created {
		t.Fatalf("first upsert must report created")
	}
	if u1.Email != "a@example.com" || u1.Handle != "Alice" {
		t.Fatalf("created = %+v", u1)
	}
	if len(u1.Roles) != 1 || u1.Roles[0] != "user" {
		t.Fatalf("default role missing: %v", u1.Roles)
	}

	// A later login must not clobber the profile: same id, same fields.
	avatar := "https://img.example/a.png"
	u2, created, err := s.Upsert(ctx, "a@example.com", "Alice II", &avatar, "USD")
	if err != nil {
		t.Fatalf("Upsert existing: %v", err)
	}
	if created {
		t.Fatalf("existing-account upsert must not report created")
	}
	if u2.ID != u1.ID {
		t.Fatalf("upsert created a duplicate: %s vs %s", u1.ID, u2.ID)
	}
	if u2.Handle != "Alice" || u2.AvatarURL != nil {
		t.Fatalf("login overwrote the profile: %+v", u2)
	}
	if !u2.UpdatedAt.Equal(u1.UpdatedAt) {
		t.Fatalf("login touched updated_at: %v vs %v", u2.UpdatedAt, u1.UpdatedAt)
	}

	// citext: case-insensitive email still resolves the same account.
	u3, created, err := s.Upsert(ctx, "A@EXAMPLE.COM", "Alice III", nil, "USD")
	if err != nil {
		t.Fatalf("Upsert citext: %v", err)
	}
	if created {
		t.Fatalf("citext-matched upsert must not report created")
	}
	if u3.ID != u1.ID {
		t.Fatalf("citext email uniqueness failed: %s vs %s", u3.ID, u1.ID)
	}
}

func TestUpdate_FieldSemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, _, err := s.Upsert(ctx, "c@example.com", "Carol", nil, "USD")
	if err != nil {
		t.Fatal(err)
	}

	handle := "Carol_Prime"
	avatar := "https://img.example/c.png"
	u, err := s.Update(ctx, created.ID, &handle, &avatar, nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if u.Handle != "Carol_Prime" || u.AvatarURL == nil || *u.AvatarURL != avatar {
		t.Fatalf("update not applied: %+v", u)
	}
	if !u.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at not bumped")
	}
	if len(u.Roles) != 1 || u.Roles[0] != "user" {
		t.Fatalf("roles lost: %v", u.Roles)
	}

	// nil keeps, empty string clears the avatar.
	empty := ""
	u, err = s.Update(ctx, created.ID, nil, &empty, nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if u.Handle != "Carol_Prime" || u.AvatarURL != nil {
		t.Fatalf("clear semantics wrong: %+v", u)
	}

	_, err = s.Update(ctx, uuid.New(), &handle, nil, nil, nil, nil, time.Hour)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDelete_IdempotentAndCascades(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, _, err := s.Upsert(ctx, "d@example.com", "Dave", nil, "USD")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := s.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Fatalf("delete of a live row must report deleted")
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("user survived: %v", err)
	}
	// user_roles cascaded: re-creating by email gets a fresh id + role.
	again, _, err := s.Upsert(ctx, "d@example.com", "Dave", nil, "USD")
	if err != nil || again.ID == created.ID || len(again.Roles) != 1 {
		t.Fatalf("recreate = %+v %v", again, err)
	}
	deleted, err = s.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if deleted {
		t.Fatalf("delete of an already-gone row must report a noop")
	}
}

func TestStorePreferredCurrencyLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// First login with a hint-derived currency: the insert seeds it.
	u, _, err := st.Upsert(ctx, "cur@example.com", "Cur", nil, "EUR")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if u.PreferredCurrency != "EUR" {
		t.Fatalf("seeded currency: %q", u.PreferredCurrency)
	}

	// A later login with a different hint never overwrites.
	u, _, err = st.Upsert(ctx, "cur@example.com", "Cur", nil, "JPY")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if u.PreferredCurrency != "EUR" {
		t.Fatalf("existing row must keep its currency, got %q", u.PreferredCurrency)
	}

	// The profile update changes it; nil leaves it alone.
	gbp := "GBP"
	u, err = st.Update(ctx, u.ID, nil, nil, &gbp, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if u.PreferredCurrency != "GBP" {
		t.Fatalf("updated currency: %q", u.PreferredCurrency)
	}
	u, err = st.Update(ctx, u.ID, nil, nil, nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("nil update: %v", err)
	}
	if u.PreferredCurrency != "GBP" {
		t.Fatalf("nil must keep the currency, got %q", u.PreferredCurrency)
	}
}

// Pins the DB default (every account, incl. pre-migration rows, reads
// "feed") and Update's COALESCE semantics: a value changes it, nil doesn't.
func TestUpdate_LandingPage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, _, err := s.Upsert(ctx, "landing@example.com", "Landing", nil, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if u.LandingPage != "feed" {
		t.Fatalf("landing_page default = %q, want feed", u.LandingPage)
	}

	collection := "collection"
	u, err = s.Update(ctx, u.ID, nil, nil, nil, nil, &collection, time.Hour)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if u.LandingPage != "collection" {
		t.Fatalf("landing_page = %q, want collection", u.LandingPage)
	}

	u, err = s.Update(ctx, u.ID, nil, nil, nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("nil update: %v", err)
	}
	if u.LandingPage != "collection" {
		t.Fatalf("nil must keep landing_page, got %q", u.LandingPage)
	}
}

func TestGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, _, err := s.Upsert(ctx, "b@example.com", "Bob", nil, "USD")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Email != "b@example.com" || len(got.Roles) != 1 {
		t.Fatalf("got = %+v", got)
	}

	_, err = s.Get(ctx, uuid.New())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestUpsert_MintsAndDedupesHandles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u1, _, err := s.Upsert(ctx, "a@example.com", "Alice Prime", nil, "USD")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if u1.Handle != "Alice_Prime" {
		t.Fatalf("handle = %q, want Alice_Prime", u1.Handle)
	}
	if u1.ProfileVisibility != "private" {
		t.Fatalf("profile_visibility = %q, want private", u1.ProfileVisibility)
	}
	if u1.LandingPage != "feed" {
		t.Fatalf("landing_page = %q, want feed", u1.LandingPage)
	}

	// Same folded key from a different account: suffix dedupe.
	u2, _, err := s.Upsert(ctx, "b@example.com", "alice prime", nil, "USD")
	if err != nil {
		t.Fatalf("Upsert twin: %v", err)
	}
	if u2.Handle != "alice_prime2" {
		t.Fatalf("deduped handle = %q, want alice_prime2", u2.Handle)
	}

	// Empty seed falls back to the email local part.
	u3, _, err := s.Upsert(ctx, "carol.q@example.com", "", nil, "USD")
	if err != nil {
		t.Fatalf("Upsert fallback: %v", err)
	}
	if u3.Handle != "carol_q" {
		t.Fatalf("fallback handle = %q, want carol_q", u3.Handle)
	}
}

// Pins Upsert's own base+"1" reserved-avoidance branch (distinct from
// migration 000003's backfill for pre-existing rows). A display_name
// deriving straight to "Search" must never mint the bare reserved handle,
// which would shadow /shared/profiles/search.
func TestUpsert_MintsHandleAwayFromReservedWord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, _, err := s.Upsert(ctx, "s@example.com", "Search", nil, "USD")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if u.Handle != "Search1" {
		t.Fatalf("handle = %q, want Search1 (reserved fold must not be assigned bare)", u.Handle)
	}
	if key := store.NormalizeHandle(u.Handle); key != "search1" {
		t.Fatalf("handle_key = %q, want search1", key)
	}
}

func TestUpdate_HandleCooldownAndTaken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u1, _, _ := s.Upsert(ctx, "a@example.com", "Alice", nil, "USD")
	u2, _, _ := s.Upsert(ctx, "b@example.com", "Bob", nil, "USD")

	// First change succeeds and stamps handle_changed_at.
	h := "Alice_Prime"
	got, err := s.Update(ctx, u1.ID, &h, nil, nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("first change: %v", err)
	}
	if got.Handle != "Alice_Prime" || got.HandleChangedAt == nil {
		t.Fatalf("changed = %+v", got)
	}

	// Second change inside the window: cooldown.
	h2 := "Alice_Two"
	if _, err := s.Update(ctx, u1.ID, &h2, nil, nil, nil, nil, time.Hour); !errors.Is(err, store.ErrHandleCooldown) {
		t.Fatalf("cooldown err = %v", err)
	}

	// Same typed value is a no-op: no cooldown, no stamp change.
	same := "Alice_Prime"
	again, err := s.Update(ctx, u1.ID, &same, nil, nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("no-op change: %v", err)
	}
	if !again.HandleChangedAt.Equal(*got.HandleChangedAt) {
		t.Fatalf("no-op stamped handle_changed_at")
	}

	// Bob claiming a decoration of Alice's handle: taken.
	steal := "alice_prime"
	if _, err := s.Update(ctx, u2.ID, &steal, nil, nil, nil, nil, time.Hour); !errors.Is(err, store.ErrHandleTaken) {
		t.Fatalf("taken err = %v", err)
	}

	// Zero cooldown allows immediate re-change (the e2e dev posture).
	h3 := "Alice_Three"
	if _, err := s.Update(ctx, u1.ID, &h3, nil, nil, nil, nil, 0); err != nil {
		t.Fatalf("zero-cooldown change: %v", err)
	}
}

// Update gates on a plain string comparison against the current TYPED
// handle, not the folded key, so a decoration-only rename ("alice_prime"
// vs "Alice_Prime", same fold) still counts as a change and is
// cooldown-gated; there is no decoration-only exemption.
func TestUpdate_DecorationOnlyRenameIsGatedByCooldown(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, _, _ := s.Upsert(ctx, "e@example.com", "Bob", nil, "USD")

	// Mint the chosen handle: a genuine typed change from the derived
	// "Bob", so it stamps handle_changed_at and starts the cooldown.
	minted := "Alice_Prime"
	got, err := s.Update(ctx, u.ID, &minted, nil, nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if got.Handle != "Alice_Prime" || got.HandleChangedAt == nil {
		t.Fatalf("minted = %+v", got)
	}

	// A decoration-only rename of the current handle, inside the cooldown
	// window: same folded identity, different typed string -> still gated.
	decorationOnly := "alice_prime"
	if _, err := s.Update(ctx, u.ID, &decorationOnly, nil, nil, nil, nil, time.Hour); !errors.Is(err, store.ErrHandleCooldown) {
		t.Fatalf("decoration-only rename inside cooldown: err = %v, want ErrHandleCooldown", err)
	}
}

func TestSharedReads_VisibilityAndFold(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u1, _, _ := s.Upsert(ctx, "a@example.com", "Alice Prime", nil, "USD")
	u2, _, _ := s.Upsert(ctx, "b@example.com", "Bob Fixture", nil, "USD")
	u3, _, _ := s.Upsert(ctx, "c@example.com", "Carol Hidden", nil, "USD")
	listed := "listed"
	if _, err := s.Update(ctx, u1.ID, nil, nil, nil, &listed, nil, time.Hour); err != nil {
		t.Fatalf("list alice: %v", err)
	}
	unlisted := "unlisted"
	if _, err := s.Update(ctx, u3.ID, nil, nil, nil, &unlisted, nil, time.Hour); err != nil {
		t.Fatalf("unlist carol: %v", err)
	}

	// GetByHandle folds: decorated lookups resolve.
	got, err := s.GetByHandle(ctx, store.NormalizeHandle("ALICE__PRIME"))
	if err != nil || got.ID != u1.ID {
		t.Fatalf("GetByHandle = %+v, %v", got, err)
	}
	if _, err := s.GetByHandle(ctx, store.NormalizeHandle("nobody")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing handle err = %v", err)
	}

	// SearchListed matches on the fold and excludes private bob.
	found, err := s.SearchListed(ctx, "liceprim", 20)
	if err != nil || len(found) != 1 || found[0].ID != u1.ID {
		t.Fatalf("SearchListed = %v, %v", found, err)
	}
	if found2, _ := s.SearchListed(ctx, "bobfixture", 20); len(found2) != 0 {
		t.Fatalf("private profile leaked into search: %v", found2)
	}
	// unlisted is link-only (reachable by exact handle, not search - see
	// TestUnitSharedProfile_VisibilityGate); the e2e suite pins this too.
	if found3, _ := s.SearchListed(ctx, "carolhidden", 20); len(found3) != 0 {
		t.Fatalf("unlisted profile leaked into search: %v", found3)
	}

	// GetByIDs returns both regardless of visibility (attribution rule).
	both, err := s.GetByIDs(ctx, []uuid.UUID{u1.ID, u2.ID})
	if err != nil || len(both) != 2 {
		t.Fatalf("GetByIDs = %v, %v", both, err)
	}
}

// Pins that a literal % or \ can't act as SQL LIKE syntax: unescaped,
// "ali%prime" would wildcard-match "aliceprime"; escaped, it's a literal
// substring alice's handle lacks, so it must find nothing. Ordinary
// substring search still works.
func TestSearchListed_EscapesLikeMetacharacters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	alice, _, err := s.Upsert(ctx, "alice@example.com", "Alice Prime", nil, "USD")
	if err != nil {
		t.Fatal(err)
	}
	listed := "listed"
	if _, err := s.Update(ctx, alice.ID, nil, nil, nil, &listed, nil, time.Hour); err != nil {
		t.Fatalf("list alice: %v", err)
	}

	found, err := s.SearchListed(ctx, "ali%prime", 20)
	if err != nil {
		t.Fatalf("SearchListed with %%: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("literal %% must not wildcard-match, got %v", found)
	}

	found, err = s.SearchListed(ctx, `ali\prime`, 20)
	if err != nil {
		t.Fatalf("SearchListed with backslash: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("literal backslash must not error or wildcard-match, got %v", found)
	}

	found, err = s.SearchListed(ctx, "liceprim", 20)
	if err != nil || len(found) != 1 || found[0].ID != alice.ID {
		t.Fatalf("ordinary substring search = %v, %v", found, err)
	}
}
