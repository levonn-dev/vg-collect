// Tests for catalog submissions: filing, the review queue, and
// verdicts.

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

func TestSubmissions_LifecycleCapsAndQueue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()

	custom, err := s.CreateEntry(ctx, customEntry(userID), nil)
	if err != nil {
		t.Fatal(err)
	}

	sub, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != "pending" || sub.EntryID != custom.ID || sub.UserID != userID {
		t.Fatalf("submission wrong: %+v", sub)
	}

	// One pending per entry: the partial unique index answers.
	if _, err := s.CreateSubmission(ctx, userID, custom.ID); !errors.Is(err, store.ErrSubmissionPending) {
		t.Fatalf("double submit = %v, want ErrSubmissionPending", err)
	}

	// Cancel is a status flip; the row persists and a fresh
	// submission is allowed again.
	if err := s.CancelSubmission(ctx, userID, custom.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelSubmission(ctx, userID, custom.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second cancel = %v, want ErrNotFound", err)
	}
	latest, err := s.LatestSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "cancelled" {
		t.Fatalf("latest after cancel = %q", latest.Status)
	}
	sub2, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatalf("resubmit after cancel: %v", err)
	}

	// The caps' counting queries: one pending, two created in-window
	// (cancelled rows count - cancel/recreate must not reset the
	// window).
	if n, err := s.CountPendingSubmissions(ctx, userID); err != nil || n != 1 {
		t.Fatalf("pending count = %d (%v), want 1", n, err)
	}
	if n, err := s.CountSubmissionsSince(ctx, userID, time.Now().UTC().Add(-time.Hour)); err != nil || n != 2 {
		t.Fatalf("window count = %d (%v), want 2 incl. cancelled", n, err)
	}
	if n, err := s.CountSubmissionsSince(ctx, userID, time.Now().UTC().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("future window = %d (%v), want 0", n, err)
	}

	// The admin queue joins the LIVE entry (edits flow through).
	rows, total, err := s.ListPendingSubmissions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("queue = %d/%d, want 1/1", len(rows), total)
	}
	row := rows[0]
	if row.ID != sub2.ID || row.DisplayName != custom.DisplayName || row.ItemType != custom.ItemType || row.Region != custom.Region {
		t.Fatalf("proposal join wrong: %+v", row)
	}

	// Reject resolves the row; a second verdict finds it resolved.
	rej, err := s.RejectSubmission(ctx, sub2.ID, "not a shared item")
	if err != nil {
		t.Fatal(err)
	}
	if rej.Status != "rejected" || rej.RejectReason == nil || *rej.RejectReason != "not a shared item" || rej.ReviewedAt == nil {
		t.Fatalf("reject wrong: %+v", rej)
	}
	if _, err := s.RejectSubmission(ctx, sub2.ID, "again"); !errors.Is(err, store.ErrSubmissionResolved) {
		t.Fatalf("re-reject = %v, want ErrSubmissionResolved", err)
	}

	// Approve adopts: the entry flips product-backed and the
	// submission resolves, atomically.
	sub3, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	productID := uuid.New()
	platName := "SNES"
	rd := time.Date(1995, 10, 9, 0, 0, 0, 0, time.UTC)
	if err := s.RecordSubmissionProduct(ctx, sub3.ID, productID); err != nil {
		t.Fatal(err)
	}
	appr, err := s.ApproveSubmission(ctx, sub3.ID, store.CatalogSnapshot{
		ProductID: productID, ItemType: "game", DisplayName: "Curated Name",
		PlatformName: &platName, FirstReleaseDate: &rd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if appr.Status != "approved" || appr.ProductID == nil || *appr.ProductID != productID {
		t.Fatalf("approve wrong: %+v", appr)
	}
	adopted, err := s.GetEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.ProductID == nil || *adopted.ProductID != productID ||
		adopted.DisplayName != "Curated Name" ||
		adopted.PlatformName == nil || *adopted.PlatformName != "SNES" ||
		adopted.FirstReleaseDate == nil || !adopted.FirstReleaseDate.Equal(rd) {
		t.Fatalf("adoption snapshot wrong: %+v", adopted)
	}
	if _, err := s.ApproveSubmission(ctx, sub3.ID, store.CatalogSnapshot{ProductID: productID}); !errors.Is(err, store.ErrSubmissionResolved) {
		t.Fatalf("re-approve = %v, want ErrSubmissionResolved", err)
	}

	// Entry deletion cascades the history away.
	if err := s.DeleteEntry(ctx, userID, custom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestSubmissionForEntry(ctx, userID, custom.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after entry delete = %v, want ErrNotFound", err)
	}
}

// TestCountAllPendingSubmissions pins the review-queue gauge query:
// pending rows count across users, resolved rows do not.
func TestCountAllPendingSubmissions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	if n, err := s.CountAllPendingSubmissions(ctx); err != nil || n != 0 {
		t.Fatalf("empty = %d (%v), want 0", n, err)
	}
	entryA := mustCreate(t, s, customEntry(userA), nil)
	entryB := mustCreate(t, s, customEntry(userB), nil)
	if _, err := s.CreateSubmission(ctx, userA, entryA.ID); err != nil {
		t.Fatal(err)
	}
	subB, err := s.CreateSubmission(ctx, userB, entryB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountAllPendingSubmissions(ctx); err != nil || n != 2 {
		t.Fatalf("two users pending = %d (%v), want 2", n, err)
	}
	if _, err := s.RejectSubmission(ctx, subB.ID, "not a shared item"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountAllPendingSubmissions(ctx); err != nil || n != 1 {
		t.Fatalf("after reject = %d (%v), want 1", n, err)
	}
}

// TestApproveSubmission_PreservesUserOwnedFields guards ApproveSubmission's
// documented contract - "the entry keeps every user-owned field
// (acquisition, tags, rank, pricing)" - against a regression that widens
// the adoption UPDATE onto a column it must leave alone. The fixture sets
// one value per category (acquisition, condition, pricing, rank) BEFORE
// approval; every one of them must read back unchanged afterward, even
// though the catalog fields did change. It also pins the opposite rule for
// region_mismatch_ack_at: adoption changes product_id like any other
// re-match, so the ack must clear, not survive alongside the other
// user-owned fields.
func TestApproveSubmission_PreservesUserOwnedFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()

	e := customEntry(userID)
	// Acquisition.
	e.PricePaidCents = new(int64(3599))
	e.Currency = "GBP"
	e.PurchasedAt = new(time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC))
	e.PurchasedFrom = new("Local Game Shop")
	// Condition.
	e.Packaging = "sealed"
	e.HasBox = true
	e.HasManual = true
	e.BoxCondition = new("good")
	e.ManualCondition = new("very_good")
	e.ItemCondition = new("acceptable")
	// Pricing.
	e.PricingMode = "custom"
	e.CustomValueCents = new(int64(4200))
	e.CustomValueEnteredCents = new(int64(5000))
	e.CustomValueEnteredCurrency = new("EUR")
	created := mustCreate(t, s, e, nil)
	origRank := rankOf(t, created) // Rank.

	if err := s.AckRegionMismatch(ctx, userID, created.ID); err != nil {
		t.Fatal(err)
	}

	sub, err := s.CreateSubmission(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	productID := uuid.New()
	platName := "SNES"
	rd := time.Date(1995, 10, 9, 0, 0, 0, 0, time.UTC)
	appr, err := s.ApproveSubmission(ctx, sub.ID, store.CatalogSnapshot{
		ProductID: productID, ItemType: "game", DisplayName: "Curated Name",
		PlatformName: &platName, FirstReleaseDate: &rd,
		LocalizedName: new("聖剣伝説3"), LocalizedNameTranslit: new("Seiken Densetsu 3"),
		LocalizedCoverURL: new("https://images.igdb.example/jp.jpg"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if appr.Status != "approved" {
		t.Fatalf("approve status = %q", appr.Status)
	}

	adopted, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The catalog side actually changed - the preservation check below
	// is meaningless if adoption silently no-oped.
	if adopted.ProductID == nil || *adopted.ProductID != productID ||
		adopted.DisplayName != "Curated Name" ||
		adopted.PlatformName == nil || *adopted.PlatformName != "SNES" ||
		adopted.FirstReleaseDate == nil || !adopted.FirstReleaseDate.Equal(rd) ||
		adopted.LocalizedName == nil || *adopted.LocalizedName != "聖剣伝説3" ||
		adopted.LocalizedNameTranslit == nil || *adopted.LocalizedNameTranslit != "Seiken Densetsu 3" ||
		adopted.LocalizedCoverURL == nil || *adopted.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
		t.Fatalf("adoption must write the catalog snapshot: %+v", adopted)
	}
	if adopted.RegionMismatchAckAt != nil {
		t.Fatal("adoption changes product_id, so it must clear region_mismatch_ack_at like any other product change")
	}

	// Acquisition.
	if adopted.PricePaidCents == nil || *adopted.PricePaidCents != 3599 {
		t.Fatalf("price_paid_cents must survive, got %v", adopted.PricePaidCents)
	}
	if adopted.Currency != "GBP" {
		t.Fatalf("currency must survive, got %q", adopted.Currency)
	}
	if adopted.PurchasedAt == nil || !adopted.PurchasedAt.Equal(*e.PurchasedAt) {
		t.Fatalf("purchased_at must survive, got %v", adopted.PurchasedAt)
	}
	if adopted.PurchasedFrom == nil || *adopted.PurchasedFrom != "Local Game Shop" {
		t.Fatalf("purchased_from must survive, got %v", adopted.PurchasedFrom)
	}
	// Condition.
	if adopted.Packaging != "sealed" {
		t.Fatalf("packaging must survive, got %q", adopted.Packaging)
	}
	if !adopted.HasBox || !adopted.HasManual {
		t.Fatalf("has_box/has_manual must survive, got %v/%v", adopted.HasBox, adopted.HasManual)
	}
	if adopted.BoxCondition == nil || *adopted.BoxCondition != "good" {
		t.Fatalf("box_condition must survive, got %v", adopted.BoxCondition)
	}
	if adopted.ManualCondition == nil || *adopted.ManualCondition != "very_good" {
		t.Fatalf("manual_condition must survive, got %v", adopted.ManualCondition)
	}
	if adopted.ItemCondition == nil || *adopted.ItemCondition != "acceptable" {
		t.Fatalf("item_condition must survive, got %v", adopted.ItemCondition)
	}
	// Pricing.
	if adopted.PricingMode != "custom" {
		t.Fatalf("pricing_mode must survive, got %q", adopted.PricingMode)
	}
	if adopted.CustomValueCents == nil || *adopted.CustomValueCents != 4200 {
		t.Fatalf("custom_value_cents must survive, got %v", adopted.CustomValueCents)
	}
	if adopted.CustomValueEnteredCents == nil || *adopted.CustomValueEnteredCents != 5000 ||
		adopted.CustomValueEnteredCurrency == nil || *adopted.CustomValueEnteredCurrency != "EUR" {
		t.Fatalf("entered custom pair must survive, got %v %v",
			adopted.CustomValueEnteredCents, adopted.CustomValueEnteredCurrency)
	}
	// Rank.
	if rankOf(t, adopted) != origRank {
		t.Fatalf("backlog_rank must survive, got %q want %q", rankOf(t, adopted), origRank)
	}
}

// TestGetSubmission is GetSubmission's direct exercise: a hit returns
// the row keyed on id alone (no user scoping - the admin queue reads
// across users), a miss answers the documented sentinel. GetSubmission
// returns a plain Submission, not a SubmissionProposal, so there are no
// proposal fields to assert here; the live-join proposal fields are
// TestListPendingSubmissions_ReflectsLiveEntryEdits's job below.
func TestGetSubmission(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	entry := mustCreate(t, s, customEntry(userID), nil)

	created, err := s.CreateSubmission(ctx, userID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSubmission(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.EntryID != entry.ID || got.UserID != userID || got.Status != "pending" {
		t.Fatalf("get submission = %+v, want id=%s entry=%s user=%s status=pending",
			got, created.ID, entry.ID, userID)
	}

	if _, err := s.GetSubmission(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown id = %v, want ErrNotFound", err)
	}
}

// TestRecordSubmissionProduct_ResolvedGuard: recording on a resolved
// (here, rejected) row must not silently succeed or move product_id -
// the approve_new retry path depends on this guard to detect that a
// concurrent verdict already resolved the row.
func TestRecordSubmissionProduct_ResolvedGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	entry := mustCreate(t, s, customEntry(userID), nil)

	sub, err := s.CreateSubmission(ctx, userID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RejectSubmission(ctx, sub.ID, "not a shared item"); err != nil {
		t.Fatal(err)
	}

	if err := s.RecordSubmissionProduct(ctx, sub.ID, uuid.New()); !errors.Is(err, store.ErrSubmissionResolved) {
		t.Fatalf("record on a resolved row = %v, want ErrSubmissionResolved", err)
	}

	got, err := s.GetSubmission(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProductID != nil {
		t.Fatalf("product_id must stay nil after a guarded record, got %v", *got.ProductID)
	}
}

// TestListPendingSubmissions_ReflectsLiveEntryEdits proves the queue
// joins the entry LIVE: every mutable proposal column, edited AFTER the
// submission was filed, must show the edit - not a submit-time
// snapshot. item_type is excluded: it is immutable at the store layer
// (UpdateEntry's SET list never touches it), so it cannot be exercised
// this way.
func TestListPendingSubmissions_ReflectsLiveEntryEdits(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	entry := mustCreate(t, s, customEntry(userID), nil)

	if _, err := s.CreateSubmission(ctx, userID, entry.ID); err != nil {
		t.Fatal(err)
	}

	edited := entry
	edited.DisplayName = "Retitled Cart"
	edited.PlatformName = new("SNES (PAL)")
	edited.Region = "pal"
	edited.Edition = new("Player's Choice")
	rd := time.Date(1996, 3, 1, 0, 0, 0, 0, time.UTC)
	edited.FirstReleaseDate = &rd
	if _, err := s.UpdateEntry(ctx, edited, nil); err != nil {
		t.Fatal(err)
	}

	rows, total, err := s.ListPendingSubmissions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("queue = %d/%d, want 1/1", len(rows), total)
	}
	row := rows[0]
	if row.DisplayName != "Retitled Cart" ||
		row.PlatformName == nil || *row.PlatformName != "SNES (PAL)" ||
		row.Region != "pal" ||
		row.Edition == nil || *row.Edition != "Player's Choice" ||
		row.FirstReleaseDate == nil || !row.FirstReleaseDate.Equal(rd) {
		t.Fatalf("queue must reflect the LIVE entry, got %+v", row)
	}
}

func TestSubmissionAck_StampOnceIdempotentAndApprovedOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	custom, err := s.CreateEntry(ctx, customEntry(userID), nil)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}

	// No approved submission yet: the approved-only read is ErrNotFound.
	if _, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pending-only = %v, want ErrNotFound", err)
	}

	// Approve it (adoption path), then the approved read finds it unstamped.
	if err := s.RecordSubmissionProduct(ctx, sub.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveSubmission(ctx, sub.ID, store.CatalogSnapshot{ProductID: uuid.New(), ItemType: "game", DisplayName: "X"}); err != nil {
		t.Fatal(err)
	}
	appr, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if appr.ResolutionAckAt != nil {
		t.Fatalf("fresh approved submission must be unacked: %+v", appr.ResolutionAckAt)
	}

	// Stamp once, then re-stamp is a no-op that does not move the time.
	if err := s.AckSubmissionResolution(ctx, appr.ID); err != nil {
		t.Fatal(err)
	}
	stamped, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stamped.ResolutionAckAt == nil {
		t.Fatal("ack did not stamp")
	}
	first := *stamped.ResolutionAckAt
	if err := s.AckSubmissionResolution(ctx, appr.ID); err != nil {
		t.Fatalf("repeat ack must be a no-op, got %v", err)
	}
	again, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ResolutionAckAt == nil || !again.ResolutionAckAt.Equal(first) {
		t.Fatalf("repeat ack moved the stamp: %v -> %v", first, again.ResolutionAckAt)
	}
}

func TestListPendingSubmissions_JoinsCover(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	e := customEntry(userID)
	cover := "https://img.example/prop.jpg"
	e.CoverURL = &cover
	created, err := s.CreateEntry(ctx, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubmission(ctx, userID, created.ID); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.ListPendingSubmissions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CoverURL == nil || *rows[0].CoverURL != cover {
		t.Fatalf("proposal cover not joined: %+v", rows)
	}
}
