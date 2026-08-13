// Tag CRUD and the per-entry tag set.

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TagRef is a tag as carried on an entry.
type TagRef struct {
	ID   uuid.UUID
	Name string
}

// Tag is a tag with its usage count (the tag-management surface).
type Tag struct {
	ID         uuid.UUID
	Name       string
	EntryCount int
}

// TagCap is the per-user distinct-tag ceiling; CreateTag's only
// uncapped user-writable growth path before this, closed here.
// Exported so the handler's cap-exceeded detail can name the same
// number without duplicating the literal.
const TagCap = 200

// CreateTag creates a user-scoped tag; names are unique per user
// case-insensitively (citext). Enforces TagCap distinct tags per
// user, count-then-insert inside one transaction: not airtight
// against a genuine race between two concurrent creates from the same
// user (no explicit lock), the same best-effort shape as the social
// service's own edge caps - the abuse case this closes (a user far
// past a modest static ceiling) has no meaningful path through it.
func (s *Store) CreateTag(ctx context.Context, userID uuid.UUID, name string) (Tag, error) {
	var t Tag
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM tags WHERE user_id = $1`, userID).Scan(&n); err != nil {
			return fmt.Errorf("store: count tags: %w", err)
		}
		if n >= TagCap {
			return ErrUserTagCapExceeded
		}
		return tx.QueryRow(ctx, `
			INSERT INTO tags (user_id, name) VALUES ($1, $2)
			RETURNING id, name`, userID, name).Scan(&t.ID, &t.Name)
	})
	if isUniqueViolation(err) {
		return Tag{}, ErrNameTaken
	}
	if errors.Is(err, ErrUserTagCapExceeded) {
		return Tag{}, ErrUserTagCapExceeded
	}
	if err != nil {
		return Tag{}, fmt.Errorf("store: create tag: %w", err)
	}
	return t, nil
}

// ListTags returns the user's tags with usage counts, ordered by name.
func (s *Store) ListTags(ctx context.Context, userID uuid.UUID) ([]Tag, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.name, count(et.entry_id)
		FROM tags t LEFT JOIN entry_tags et ON et.tag_id = t.id
		WHERE t.user_id = $1
		GROUP BY t.id, t.name ORDER BY t.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list tags: %w", err)
	}
	return scanAll(rows, []Tag{}, "", func(r pgx.Rows) (Tag, error) {
		var t Tag
		if err := r.Scan(&t.ID, &t.Name, &t.EntryCount); err != nil {
			return Tag{}, fmt.Errorf("store: scan tag: %w", err)
		}
		return t, nil
	})
}

// RenameTag renames one of the user's tags.
func (s *Store) RenameTag(ctx context.Context, userID, id uuid.UUID, name string) (Tag, error) {
	var t Tag
	err := s.pool.QueryRow(ctx, `
		UPDATE tags SET name = $3 WHERE id = $1 AND user_id = $2
		RETURNING id, name`, id, userID, name).Scan(&t.ID, &t.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if isUniqueViolation(err) {
		return Tag{}, ErrNameTaken
	}
	if err != nil {
		return Tag{}, fmt.Errorf("store: rename tag: %w", err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM entry_tags WHERE tag_id = $1`, id).Scan(&t.EntryCount); err != nil {
		return Tag{}, fmt.Errorf("store: tag count: %w", err)
	}
	return t, nil
}

// DeleteTag removes one of the user's tags; entry links cascade.
func (s *Store) DeleteTag(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tags WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("store: delete tag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
