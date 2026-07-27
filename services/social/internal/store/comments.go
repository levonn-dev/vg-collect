package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Comment is a row in any lifecycle state; live-read paths only ever
// see deleted_at IS NULL rows, so their Body/AuthorID are non-nil.
type Comment struct {
	ID           uuid.UUID
	ShelfID      uuid.UUID
	ShelfOwnerID uuid.UUID
	AuthorID     *uuid.UUID
	Body         *string
	CreatedAt    time.Time
	DeletedAt    *time.Time
	DeletedBy    *uuid.UUID
}

const commentCols = `id, shelf_id, shelf_owner_id, author_id, body, created_at, deleted_at, deleted_by`

func scanComment(row pgx.Row) (Comment, error) {
	var c Comment
	err := row.Scan(&c.ID, &c.ShelfID, &c.ShelfOwnerID, &c.AuthorID, &c.Body,
		&c.CreatedAt, &c.DeletedAt, &c.DeletedBy)
	return c, err
}

// CreateComment writes the comment + its event. The cap counts every
// authored row incl. tombstones (delete-repost spam still burns it).
func (s *Store) CreateComment(ctx context.Context, shelf, shelfOwner, author uuid.UUID, body string, cap int) (Comment, error) {
	var c Comment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		n, err := capCount(ctx, tx, "comments", "author_id", author)
		if err != nil {
			return err
		}
		if n >= cap {
			return ErrCapExceeded
		}
		c, err = scanComment(tx.QueryRow(ctx, `
			INSERT INTO comments (shelf_id, shelf_owner_id, author_id, body)
			VALUES ($1, $2, $3, $4) RETURNING `+commentCols,
			shelf, shelfOwner, author, body))
		if err != nil {
			return fmt.Errorf("store: create comment: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity (actor_id, verb, object_shelf_id, object_comment_id, target_user_id)
			VALUES ($1, 'commented_shelf', $2, $3, $4)`,
			author, shelf, c.ID, shelfOwner); err != nil {
			return fmt.Errorf("store: comment event: %w", err)
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	return c, nil
}

// ListLiveComments pages live rows newest-first from the cursor.
func (s *Store) ListLiveComments(ctx context.Context, shelf uuid.UUID, cursor *Cursor, limit int) ([]Comment, error) {
	after := time.Now().Add(time.Hour) // sentinel: newer than anything
	afterID := uuid.Max
	if cursor != nil {
		after, afterID = cursor.CreatedAt, cursor.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+commentCols+` FROM comments
		WHERE shelf_id = $1 AND deleted_at IS NULL AND (created_at, id) < ($2, $3)
		ORDER BY created_at DESC, id DESC LIMIT $4`,
		shelf, after, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list comments: %w", err)
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan comment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LiveCommentsByIDs batch-loads live rows (feed excerpts). By
// construction feed events only reference live comments (deletion
// removes the event), so misses here mean a race, and the row just
// drops from the hydration.
func (s *Store) LiveCommentsByIDs(ctx context.Context, ids []uuid.UUID) ([]Comment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+commentCols+` FROM comments
		WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: comments by ids: %w", err)
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan comment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteComment tombstones per the lifecycle: the author's own delete
// erases the body (self-erasure, irreversible) and reports the outcome
// "self_delete"; the shelf owner's removal retains the body (undelete
// arrives with moderation tooling) and reports "owner_delete". The
// author check runs first, so an owner deleting their own comment
// still matches the author branch and reports self_delete. Either way
// the comment's event is removed. Tombstones cannot be deleted again
// (ErrNotFound); strangers get ErrForbidden. outcome is "" whenever
// err is non-nil.
func (s *Store) DeleteComment(ctx context.Context, id, caller uuid.UUID) (string, error) {
	outcome := ""
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := scanComment(tx.QueryRow(ctx,
			`SELECT `+commentCols+` FROM comments WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("store: read comment: %w", err)
		}
		if c.DeletedAt != nil {
			return ErrNotFound
		}
		isAuthor := c.AuthorID != nil && *c.AuthorID == caller
		isOwner := c.ShelfOwnerID == caller
		if !isAuthor && !isOwner {
			return ErrForbidden
		}
		if isAuthor {
			outcome = "self_delete"
			_, err = tx.Exec(ctx, `
				UPDATE comments SET deleted_at = now(), deleted_by = $2, body = NULL
				WHERE id = $1`, id, caller)
		} else {
			outcome = "owner_delete"
			_, err = tx.Exec(ctx, `
				UPDATE comments SET deleted_at = now(), deleted_by = $2
				WHERE id = $1`, id, caller)
		}
		if err != nil {
			return fmt.Errorf("store: tombstone comment: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM activity WHERE object_comment_id = $1`, id); err != nil {
			return fmt.Errorf("store: comment event delete: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}
