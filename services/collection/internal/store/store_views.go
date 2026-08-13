// Saved views and the shared-shelf surface.

package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// View is a saved list configuration; Params is the frontend's opaque
// JSON document, stored and returned verbatim. Slug/Visibility/
// PublishedAt are the sharing layer: a view whose effective
// visibility (min of owner profile and view) is non-private is a
// "shelf" on the social surface.
type View struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	Slug        string
	Visibility  string
	Params      []byte
	PublishedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const viewCols = `id, user_id, name, slug, visibility, params, published_at, created_at, updated_at`

func scanView(row pgx.Row) (View, error) {
	var v View
	err := row.Scan(&v.ID, &v.UserID, &v.Name, &v.Slug, &v.Visibility, &v.Params,
		&v.PublishedAt, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

// ListViews returns the user's saved views ordered by name.
func (s *Store) ListViews(ctx context.Context, userID uuid.UUID) ([]View, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+viewCols+` FROM saved_views WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list views: %w", err)
	}
	return scanAll(rows, []View{}, "", func(r pgx.Rows) (View, error) {
		v, err := scanView(r)
		if err != nil {
			return View{}, fmt.Errorf("store: scan view: %w", err)
		}
		return v, nil
	})
}

// slugConstraint is the per-user folded-slug unique index; a
// violation there dedupes with a suffix, while a name violation is
// the user-facing ErrNameTaken.
const slugConstraint = "saved_views_user_slug_key_idx"

func slugViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation &&
		pgErr.ConstraintName == slugConstraint
}

// CreateView saves a view; names are unique per user
// case-insensitively, slugs unique per user on the folded key.
func (s *Store) CreateView(ctx context.Context, userID uuid.UUID, name string, params []byte, visibility string) (View, error) {
	base := DeriveSlug(name)
	slug := base
	for attempt := 2; ; attempt++ {
		v, err := scanView(s.pool.QueryRow(ctx, `
			INSERT INTO saved_views (user_id, name, params, slug, visibility, published_at)
			VALUES ($1, $2, $3, $4, $5, CASE WHEN $5 = 'listed' THEN now() END)
			RETURNING `+viewCols, userID, name, params, slug, visibility))
		if slugViolation(err) {
			suffix := strconv.Itoa(attempt)
			slug = base
			if len(slug)+len(suffix) > 30 {
				slug = strings.TrimRight(slug[:30-len(suffix)], "_")
			}
			slug += suffix
			continue
		}
		if isUniqueViolation(err) {
			return View{}, ErrNameTaken
		}
		if err != nil {
			return View{}, fmt.Errorf("store: create view: %w", err)
		}
		return v, nil
	}
}

// UpdateView replaces a view's name, params, and visibility. A name
// change re-derives the slug (old links break; documented trade); an
// unchanged name keeps the stored slug verbatim, so a params- or
// visibility-only save can never silently move the row onto a
// different suffix (e.g. one a sibling's deletion just freed) and
// break a shared link that the name change never touched. A
// transition into listed stamps published_at.
func (s *Store) UpdateView(ctx context.Context, userID, id uuid.UUID, name string, params []byte, visibility string) (View, error) {
	var currentName, currentSlug string
	err := s.pool.QueryRow(ctx,
		`SELECT name, slug FROM saved_views WHERE id = $1 AND user_id = $2`,
		id, userID).Scan(&currentName, &currentSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("store: update view: load current: %w", err)
	}

	rename := name != currentName
	base, slug := currentSlug, currentSlug
	if rename {
		base = DeriveSlug(name)
		slug = base
	}

	for attempt := 2; ; attempt++ {
		v, err := scanView(s.pool.QueryRow(ctx, `
			UPDATE saved_views SET
				name = $3, params = $4, slug = $5,
				published_at = CASE WHEN $6 = 'listed' AND visibility <> 'listed' THEN now() ELSE published_at END,
				visibility = $6,
				updated_at = now()
			WHERE id = $1 AND user_id = $2
			RETURNING `+viewCols, id, userID, name, params, slug, visibility))
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrNotFound
		}
		if rename && slugViolation(err) {
			suffix := strconv.Itoa(attempt)
			slug = base
			if len(slug)+len(suffix) > 30 {
				slug = strings.TrimRight(slug[:30-len(suffix)], "_")
			}
			slug += suffix
			continue
		}
		if isUniqueViolation(err) {
			return View{}, ErrNameTaken
		}
		if err != nil {
			return View{}, fmt.Errorf("store: update view: %w", err)
		}
		return v, nil
	}
}

// DeleteView removes one of the user's saved views.
func (s *Store) DeleteView(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM saved_views WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("store: delete view: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SeedDefaultViews gives a zero-view user the two starter shelves.
// ON CONFLICT DO NOTHING makes it safe to race and safe to re-run;
// the caller triggers it only when ListViews found nothing, so
// deleting every view brings the defaults back (factory-reset
// semantics, documented).
func (s *Store) SeedDefaultViews(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO saved_views (user_id, name, params, slug)
		VALUES
			($1, 'Full collection', '{"v":1}', 'Full_Collection'),
			($1, 'Backlog', '{"v":1,"status":["backlog"],"sort":"backlog_rank","order":"asc"}', 'Backlog')
		ON CONFLICT DO NOTHING`, userID)
	if err != nil {
		return fmt.Errorf("store: seed default views: %w", err)
	}
	return nil
}

// GetSharedShelf loads a view by id, any owner, any visibility; the
// handler applies the gate (unknown == private == 404).
func (s *Store) GetSharedShelf(ctx context.Context, id uuid.UUID) (View, error) {
	v, err := scanView(s.pool.QueryRow(ctx,
		`SELECT `+viewCols+` FROM saved_views WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("store: get shared shelf: %w", err)
	}
	return v, nil
}

// GetSharedShelfBySlug resolves (owner, folded slug) - the URL path.
func (s *Store) GetSharedShelfBySlug(ctx context.Context, ownerID uuid.UUID, foldedSlug string) (View, error) {
	v, err := scanView(s.pool.QueryRow(ctx,
		`SELECT `+viewCols+` FROM saved_views WHERE user_id = $1 AND slug_key = $2`,
		ownerID, foldedSlug))
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("store: get shelf by slug: %w", err)
	}
	return v, nil
}

// ListListedShelves pages listed views, newest publish first - the
// Explore-recent (unfiltered) and profile-page (owner-scoped) reads
// share this one method on a nil-slice contract: a nil or empty
// ownerIDs lists across every listed owner; a non-empty one scopes
// the page to just those owners (the caller passes only owners whose
// profile is listed, when scoping).
func (s *Store) ListListedShelves(ctx context.Context, ownerIDs []uuid.UUID, limit, offset int) ([]View, int, error) {
	where := "visibility = 'listed'"
	args := []any{}
	if len(ownerIDs) > 0 {
		args = append(args, ownerIDs)
		where += " AND user_id = ANY($1)"
	}

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM saved_views WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count listed shelves: %w", err)
	}

	limitArg := fmt.Sprintf("$%d", len(args)+1)
	offsetArg := fmt.Sprintf("$%d", len(args)+2)
	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, `
		SELECT `+viewCols+` FROM saved_views
		WHERE `+where+`
		ORDER BY published_at DESC NULLS LAST, id
		LIMIT `+limitArg+` OFFSET `+offsetArg, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list listed shelves: %w", err)
	}
	// The original's trailing rows.Err() branch returned (out, total,
	// err) - the real total and whatever shelves had already been
	// scanned - while its own two earlier error branches above return
	// (nil, 0, err). seed []View{} is non-nil, so scanAll only ever
	// returns a nil slice here via its own scan-closure short-circuit;
	// a nil out is therefore an unambiguous signal that this was a
	// scan error (which the original also reported as (nil, 0, err)),
	// not a trailing one.
	out, err := scanAll(rows, []View{}, "", func(r pgx.Rows) (View, error) {
		v, err := scanView(r)
		if err != nil {
			return View{}, fmt.Errorf("store: scan shelf: %w", err)
		}
		return v, nil
	})
	if out == nil && err != nil {
		return nil, 0, err
	}
	return out, total, err
}

// SharedShelvesByIDs batch-loads non-private views for hydration;
// private and missing ids are simply absent.
func (s *Store) SharedShelvesByIDs(ctx context.Context, ids []uuid.UUID) ([]View, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+viewCols+` FROM saved_views
		WHERE id = ANY($1) AND visibility <> 'private'`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: shelves by ids: %w", err)
	}
	return scanAll(rows, []View{}, "", func(r pgx.Rows) (View, error) {
		v, err := scanView(r)
		if err != nil {
			return View{}, fmt.Errorf("store: scan shelf: %w", err)
		}
		return v, nil
	})
}
