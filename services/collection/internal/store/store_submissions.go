// Catalog submissions: filing, the review queue, and verdicts.

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

// Submission is one catalog-submission row; rows persist as history
// (rejected/cancelled included) so the rolling creation cap counts every attempt.
type Submission struct {
	ID              uuid.UUID
	EntryID         uuid.UUID
	UserID          uuid.UUID
	Status          string
	RejectReason    *string
	ProductID       *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ReviewedAt      *time.Time
	ResolutionAckAt *time.Time
}

// SubmissionProposal is one admin-queue row: the submission plus the entry's
// CURRENT proposal fields (a live reference; edits flow through until the verdict).
type SubmissionProposal struct {
	Submission
	DisplayName      string
	ItemType         string
	PlatformName     *string
	Region           string
	Edition          *string
	FirstReleaseDate *time.Time
	CoverURL         *string
	Developers       []string
	Publishers       []string
}

// CatalogSnapshot is the product-derived snapshot adoption writes -
// the same fields product-backed creation snapshots.
type CatalogSnapshot struct {
	ProductID             uuid.UUID
	ItemType              string
	DisplayName           string
	PlatformIGDBID        *int64
	PlatformName          *string
	FirstReleaseDate      *time.Time
	IGDBGameID            *int64
	CoverURL              *string
	LocalizedName         *string
	LocalizedNameTranslit *string
	LocalizedCoverURL     *string
	Developers            []string
	Publishers            []string
}

const submissionCols = `id, entry_id, user_id, status, reject_reason, product_id,
	created_at, updated_at, reviewed_at, resolution_ack_at`

func scanSubmission(row pgx.Row) (Submission, error) {
	var s Submission
	if err := row.Scan(&s.ID, &s.EntryID, &s.UserID, &s.Status, &s.RejectReason, &s.ProductID,
		&s.CreatedAt, &s.UpdatedAt, &s.ReviewedAt, &s.ResolutionAckAt); err != nil {
		return Submission{}, err
	}
	return s, nil
}

// CreateSubmission files a pending submission; the partial unique
// index enforces one open submission per entry.
func (s *Store) CreateSubmission(ctx context.Context, userID, entryID uuid.UUID) (Submission, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO catalog_submissions (entry_id, user_id)
		VALUES ($1, $2)
		RETURNING `+submissionCols, entryID, userID)
	sub, err := scanSubmission(row)
	if isUniqueViolation(err) {
		return Submission{}, ErrSubmissionPending
	}
	if err != nil {
		return Submission{}, fmt.Errorf("store: create submission: %w", err)
	}
	return sub, nil
}

// LatestSubmissionForEntry is the entry page's read: the newest row
// for the caller's entry, any status.
func (s *Store) LatestSubmissionForEntry(ctx context.Context, userID, entryID uuid.UUID) (Submission, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+submissionCols+` FROM catalog_submissions
		WHERE entry_id = $1 AND user_id = $2
		ORDER BY created_at DESC, id DESC LIMIT 1`, entryID, userID)
	sub, err := scanSubmission(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, fmt.Errorf("store: latest submission: %w", err)
	}
	return sub, nil
}

// LatestApprovedSubmissionForEntry is the approval banner's read: the newest
// APPROVED row (at most one, since approval makes the entry product-backed).
func (s *Store) LatestApprovedSubmissionForEntry(ctx context.Context, userID, entryID uuid.UUID) (Submission, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+submissionCols+` FROM catalog_submissions
		WHERE entry_id = $1 AND user_id = $2 AND status = 'approved'
		ORDER BY created_at DESC, id DESC LIMIT 1`, entryID, userID)
	sub, err := scanSubmission(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, fmt.Errorf("store: latest approved submission: %w", err)
	}
	return sub, nil
}

// AckSubmissionResolution stamps the ack time once; the guard
// (resolution_ack_at IS NULL) makes a repeat a harmless no-op.
func (s *Store) AckSubmissionResolution(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE catalog_submissions
		SET resolution_ack_at = now(), updated_at = now()
		WHERE id = $1 AND resolution_ack_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("store: ack submission: %w", err)
	}
	return nil
}

// CancelSubmission flips the caller's pending submission to cancelled (a
// status change, not a delete): the row keeps counting toward the rolling cap.
func (s *Store) CancelSubmission(ctx context.Context, userID, entryID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE catalog_submissions
		SET status = 'cancelled', updated_at = now()
		WHERE entry_id = $1 AND user_id = $2 AND status = 'pending'`, entryID, userID)
	if err != nil {
		return fmt.Errorf("store: cancel submission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSubmission loads one row by id (the verdict handler's read).
func (s *Store) GetSubmission(ctx context.Context, id uuid.UUID) (Submission, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+submissionCols+` FROM catalog_submissions WHERE id = $1`, id)
	sub, err := scanSubmission(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, fmt.Errorf("store: get submission: %w", err)
	}
	return sub, nil
}

// CountPendingSubmissions serves the pending cap.
func (s *Store) CountPendingSubmissions(ctx context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM catalog_submissions
		WHERE user_id = $1 AND status = 'pending'`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count pending submissions: %w", err)
	}
	return n, nil
}

// CountAllPendingSubmissions serves the review-queue gauge: pending rows
// across every user (the (status, created_at) index keeps it an index scan).
func (s *Store) CountAllPendingSubmissions(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM catalog_submissions WHERE status = 'pending'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count all pending submissions: %w", err)
	}
	return n, nil
}

// CountSubmissionsSince serves the rolling creation cap; every row
// counts regardless of current status.
func (s *Store) CountSubmissionsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM catalog_submissions
		WHERE user_id = $1 AND created_at >= $2`, userID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count submissions since: %w", err)
	}
	return n, nil
}

// ListPendingSubmissions pages the admin queue oldest-first with the live
// entry proposal joined on (the cascade guarantees the entry exists).
func (s *Store) ListPendingSubmissions(ctx context.Context, limit, offset int) ([]SubmissionProposal, int64, error) {
	var total int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM catalog_submissions WHERE status = 'pending'`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count queue: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.entry_id, s.user_id, s.status, s.reject_reason, s.product_id,
		       s.created_at, s.updated_at, s.reviewed_at,
		       e.display_name, e.item_type, e.platform_name, e.region, e.edition, e.first_release_date, e.cover_url,
		       e.developers, e.publishers
		FROM catalog_submissions s
		JOIN entries e ON e.id = s.entry_id
		WHERE s.status = 'pending'
		ORDER BY s.created_at, s.id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list queue: %w", err)
	}
	out, err := pgkit.ScanAll(rows, nil, func(r pgx.Rows) (SubmissionProposal, error) {
		var p SubmissionProposal
		if err := r.Scan(&p.ID, &p.EntryID, &p.UserID, &p.Status, &p.RejectReason, &p.ProductID,
			&p.CreatedAt, &p.UpdatedAt, &p.ReviewedAt,
			&p.DisplayName, &p.ItemType, &p.PlatformName, &p.Region, &p.Edition, &p.FirstReleaseDate, &p.CoverURL,
			&p.Developers, &p.Publishers); err != nil {
			return SubmissionProposal{}, fmt.Errorf("store: scan queue row: %w", err)
		}
		return p, nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("store: list queue: %w", err)
	}
	return out, total, nil
}

// RejectSubmission resolves a pending row with the admin's reason.
func (s *Store) RejectSubmission(ctx context.Context, id uuid.UUID, reason string) (Submission, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE catalog_submissions
		SET status = 'rejected', reject_reason = $2, reviewed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING `+submissionCols, id, reason)
	sub, err := scanSubmission(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Submission{}, ErrSubmissionResolved
	}
	if err != nil {
		return Submission{}, fmt.Errorf("store: reject submission: %w", err)
	}
	return sub, nil
}

// RecordSubmissionProduct stores approve_new's minted product id while
// pending, so a retry after a mid-way failure adopts without re-minting.
func (s *Store) RecordSubmissionProduct(ctx context.Context, id, productID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE catalog_submissions
		SET product_id = $2, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, id, productID)
	if err != nil {
		return fmt.Errorf("store: record submission product: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSubmissionResolved
	}
	return nil
}

// ApproveSubmission adopts the product onto the submitter's entry and
// resolves the submission in one transaction; only the catalog snapshot and
// product_id change (user-owned fields survive).
func (s *Store) ApproveSubmission(ctx context.Context, id uuid.UUID, snap CatalogSnapshot) (Submission, error) {
	var out Submission
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE catalog_submissions
			SET status = 'approved', product_id = $2, reviewed_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'pending'
			RETURNING `+submissionCols, id, snap.ProductID)
		sub, err := scanSubmission(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSubmissionResolved
		}
		if err != nil {
			return fmt.Errorf("store: approve submission: %w", err)
		}
		// Always a product change, so the region-mismatch ack unconditionally
		// clears too, the same rule as RepointEntry.
		if _, err := tx.Exec(ctx, `
			UPDATE entries
			SET product_id = $2, item_type = $3, display_name = $4,
			    platform_igdb_id = $5, platform_name = $6,
			    first_release_date = $7, igdb_game_id = $8, cover_url = $9,
			    localized_name = $10, localized_name_translit = $11,
			    localized_cover_url = $12,
			    developers = $13, publishers = $14,
			    region_mismatch_ack_at = NULL,
			    updated_at = now()
			WHERE id = $1`,
			sub.EntryID, snap.ProductID, snap.ItemType, snap.DisplayName,
			snap.PlatformIGDBID, snap.PlatformName,
			snap.FirstReleaseDate, snap.IGDBGameID, snap.CoverURL,
			snap.LocalizedName, snap.LocalizedNameTranslit, snap.LocalizedCoverURL,
			snap.Developers, snap.Publishers); err != nil {
			return fmt.Errorf("store: adopt entry: %w", err)
		}
		out = sub
		return nil
	})
	if err != nil {
		return Submission{}, err
	}
	return out, nil
}
