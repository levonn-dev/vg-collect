// Tests for tag CRUD and the per-entry tag set.

package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

func TestTagsCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()

	rpg, err := s.CreateTag(ctx, user, "RPG")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(ctx, user, "rpg"); !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("case-insensitive duplicate must be ErrNameTaken, got %v", err)
	}
	if _, err := s.CreateTag(ctx, stranger, "rpg"); err != nil {
		t.Fatalf("the same name under another user is fine: %v", err)
	}

	mustCreate(t, s, baseEntry(user), []uuid.UUID{rpg.ID})
	tags, err := s.ListTags(ctx, user)
	if err != nil || len(tags) != 1 || tags[0].Name != "RPG" || tags[0].EntryCount != 1 {
		t.Fatalf("list: %+v %v", tags, err)
	}

	if _, err := s.CreateTag(ctx, user, "later"); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.RenameTag(ctx, user, rpg.ID, "role-playing")
	if err != nil || renamed.Name != "role-playing" || renamed.EntryCount != 1 {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	if _, err := s.RenameTag(ctx, user, rpg.ID, "LATER"); !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("rename onto a taken name must be ErrNameTaken, got %v", err)
	}
	if _, err := s.RenameTag(ctx, stranger, rpg.ID, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign rename must be ErrNotFound, got %v", err)
	}

	// Verify shared-tag and zero-tag entry_count via ListTags.
	second := baseEntry(user)
	second.DisplayName = "Final Fantasy"
	mustCreate(t, s, second, []uuid.UUID{rpg.ID})
	tags, err = s.ListTags(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("after second entry: expected 2 tags, got %d", len(tags))
	}
	var rolePlayingTag, laterTag *store.Tag
	for i := range tags {
		if tags[i].Name == "role-playing" {
			rolePlayingTag = &tags[i]
		}
		if tags[i].Name == "later" {
			laterTag = &tags[i]
		}
	}
	if rolePlayingTag == nil {
		t.Fatalf("role-playing tag not found in %+v", tags)
	}
	if rolePlayingTag.EntryCount != 2 {
		t.Fatalf("role-playing entry_count: got %d, want 2", rolePlayingTag.EntryCount)
	}
	if laterTag == nil {
		t.Fatalf("later tag not found in %+v", tags)
	}
	if laterTag.EntryCount != 0 {
		t.Fatalf("later entry_count: got %d, want 0", laterTag.EntryCount)
	}

	if err := s.DeleteTag(ctx, user, rpg.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ListEntries(ctx, user, store.Filters{})
	if err != nil || len(entries) != 2 {
		t.Fatalf("after tag delete, expected 2 entries: %+v %v", entries, err)
	}
	// Both entries must have tags detached.
	for _, e := range entries {
		if len(e.Tags) != 0 {
			t.Fatalf("tag delete must detach from all entries: %+v", e.Tags)
		}
	}
	if err := s.DeleteTag(ctx, user, rpg.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double delete must be ErrNotFound, got %v", err)
	}
}

// TestCreateTag_PerUserCap pins the per-user distinct-tag ceiling: the
// 200th tag succeeds, the 201st answers ErrUserTagCapExceeded, and
// the count stops climbing (no half-committed row from the rejected
// attempt). A different user's own cap is untouched by this one.
func TestCreateTag_PerUserCap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	for i := range store.TagCap {
		if _, err := s.CreateTag(ctx, user, fmt.Sprintf("tag-%03d", i)); err != nil {
			t.Fatalf("tag %d: %v", i, err)
		}
	}
	tags, err := s.ListTags(ctx, user)
	if err != nil || len(tags) != store.TagCap {
		t.Fatalf("expected exactly %d tags at the cap, got %d (%v)", store.TagCap, len(tags), err)
	}

	if _, err := s.CreateTag(ctx, user, "one-too-many"); !errors.Is(err, store.ErrUserTagCapExceeded) {
		t.Fatalf("the tag past the cap must be ErrUserTagCapExceeded, got %v", err)
	}
	tags, err = s.ListTags(ctx, user)
	if err != nil || len(tags) != store.TagCap {
		t.Fatalf("a rejected create must not land a row: got %d tags (%v)", len(tags), err)
	}

	if _, err := s.CreateTag(ctx, uuid.New(), "fresh user, fresh cap"); err != nil {
		t.Fatalf("a different user must not see this user's cap: %v", err)
	}
}
