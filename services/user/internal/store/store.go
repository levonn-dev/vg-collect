// Package store owns the user service's SQL. No other package writes
// queries against this schema.
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

var ErrNotFound = errors.New("user not found")
var ErrHandleTaken = errors.New("handle taken")
var ErrHandleCooldown = errors.New("handle changed too recently")

// querier is the subset of pgx querying shared by pool and tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// rolesQ loads a user's roles; nil for a role-less user (handlers
// normalize to [] at the JSON boundary).
func rolesQ(ctx context.Context, q querier, id uuid.UUID) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT role FROM user_roles WHERE user_id = $1 ORDER BY role`, id)
	if err != nil {
		return nil, fmt.Errorf("store: roles: %w", err)
	}
	return pgkit.ScanAll(rows, nil, func(r pgx.Rows) (string, error) {
		var role string
		if err := r.Scan(&role); err != nil {
			return "", fmt.Errorf("store: scan role: %w", err)
		}
		return role, nil
	})
}

type User struct {
	ID                uuid.UUID
	Email             string
	Handle            string
	AvatarURL         *string
	PreferredCurrency string
	ProfileVisibility string
	LandingPage       string
	HandleChangedAt   *time.Time
	Roles             []string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const userCols = `id, email, handle, avatar_url, preferred_currency, profile_visibility, landing_page, handle_changed_at, created_at, updated_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Handle, &u.AvatarURL, &u.PreferredCurrency,
		&u.ProfileVisibility, &u.LandingPage, &u.HandleChangedAt, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// Upsert creates the user on first login; an existing account is returned
// untouched, and displayNameSeed never overwrites an existing handle.
// Handle-key collisions dedupe via a numeric suffix. A returning user costs
// one indexed SELECT; the derive/dedupe/insert path only runs on first login.
func (s *Store) Upsert(ctx context.Context, email, displayNameSeed string, avatarURL *string, preferredCurrency string) (User, bool, error) {
	var u User
	created := true
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx,
			`SELECT `+userCols+` FROM users WHERE email = $1`, email))
		if err == nil {
			created = false
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("store: upsert read existing: %w", err)
		} else {
			base := DeriveHandle(displayNameSeed, localPart(email))
			if ReservedHandles[NormalizeHandle(base)] {
				base = base + "1"
			}
			// Find a free key BEFORE inserting: a unique-violation would abort
			// the transaction, so the dedupe loop is SELECT-based. The
			// residual insert race is rare and simply errors the login, which retries.
			handle := base
			for attempt := 2; ; attempt++ {
				var taken bool
				if err := tx.QueryRow(ctx,
					`SELECT EXISTS(SELECT 1 FROM users WHERE handle_key = $1)`,
					NormalizeHandle(handle)).Scan(&taken); err != nil {
					return fmt.Errorf("store: handle probe: %w", err)
				}
				if !taken {
					break
				}
				suffix := strconv.Itoa(attempt)
				handle = base
				if len(handle)+len(suffix) > 30 {
					handle = strings.TrimRight(handle[:30-len(suffix)], "_")
				}
				handle = handle + suffix
			}
			u, err = scanUser(tx.QueryRow(ctx, `
				INSERT INTO users (email, handle, avatar_url, preferred_currency)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (email) DO NOTHING
				RETURNING `+userCols,
				email, handle, avatarURL, preferredCurrency))
			if errors.Is(err, pgx.ErrNoRows) {
				// Lost the race to a concurrent create between the SELECT and
				// this INSERT; fall back to the same re-SELECT a plain conflict always used.
				created = false
				u, err = scanUser(tx.QueryRow(ctx,
					`SELECT `+userCols+` FROM users WHERE email = $1`, email))
				if err != nil {
					return fmt.Errorf("store: upsert read existing: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("store: upsert: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, role) VALUES ($1, 'user') ON CONFLICT DO NOTHING`, u.ID); err != nil {
			return fmt.Errorf("store: grant default role: %w", err)
		}
		roles, err := rolesQ(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		u.Roles = roles
		return nil
	})
	if err != nil {
		return User{}, false, err
	}
	return u, created, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

// Update edits self-serviceable fields; nil keeps the current value,
// empty avatarURL clears it. Any handle change (incl. decoration-only)
// stamps handle_changed_at; ErrHandleCooldown if too recent, ErrHandleTaken if the key's owned.
func (s *Store) Update(ctx context.Context, id uuid.UUID, handle, avatarURL, preferredCurrency, profileVisibility, landingPage *string, cooldown time.Duration) (User, error) {
	var u User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if handle != nil {
			var current string
			var changedAt *time.Time
			err := tx.QueryRow(ctx,
				`SELECT handle, handle_changed_at FROM users WHERE id = $1`, id).
				Scan(&current, &changedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("store: read handle: %w", err)
			}
			if *handle == current {
				handle = nil // no-op; do not stamp or gate
			} else if changedAt != nil && time.Since(*changedAt) < cooldown {
				return ErrHandleCooldown
			}
		}
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `
			UPDATE users SET
				handle = COALESCE($2, handle),
				handle_changed_at = CASE WHEN $2::text IS NULL THEN handle_changed_at ELSE now() END,
				avatar_url = CASE
					WHEN $3::text IS NULL THEN avatar_url
					WHEN $3 = '' THEN NULL
					ELSE $3
				END,
				preferred_currency = COALESCE($4, preferred_currency),
				profile_visibility = COALESCE($5, profile_visibility),
				landing_page = COALESCE($6, landing_page),
				updated_at = now()
			WHERE id = $1
			RETURNING `+userCols,
			id, handle, avatarURL, preferredCurrency, profileVisibility, landingPage))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if isUniqueViolation(err) {
			return ErrHandleTaken
		}
		if err != nil {
			return fmt.Errorf("store: update: %w", err)
		}
		roles, err := rolesQ(ctx, tx, id)
		if err != nil {
			return err
		}
		u.Roles = roles
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// Delete removes the account row (roles cascade); deleting a missing user
// is a no-op so retries converge. Reports whether a row was removed.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("store: delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("store: get: %w", err)
	}
	roles, err := rolesQ(ctx, s.pool, id)
	if err != nil {
		return User{}, err
	}
	u.Roles = roles
	return u, nil
}

// GetByHandle resolves a folded handle key to its user at any visibility;
// the handler applies the visibility gate, so unknown/private stay indistinguishable.
func (s *Store) GetByHandle(ctx context.Context, foldedHandle string) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE handle_key = $1`, foldedHandle))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("store: get by handle: %w", err)
	}
	return u, nil
}

// GetByIDs batch-loads users for hydration; missing ids are simply
// absent from the result. No roles (cards never need them).
func (s *Store) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+userCols+` FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: get by ids: %w", err)
	}
	return pgkit.ScanAll(rows, []User{}, func(r pgx.Rows) (User, error) {
		u, err := scanUser(r)
		if err != nil {
			return User{}, fmt.Errorf("store: scan user: %w", err)
		}
		return u, nil
	})
}

// escapeLike escapes backslash and % before splicing into '%' || $1 ||
// '%', so a literal % in a search term can't wildcard. Underscore needs no
// escaping: NormalizeHandle already strips it from every handle_key.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`).Replace(s)
}

// SearchListed substring-matches listed profiles on the folded key
// (e.g. "aliceprime" finds a_l_i_c_e_p_r_i_m_e); LIKE-escaped via escapeLike.
func (s *Store) SearchListed(ctx context.Context, foldedQuery string, limit int) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+userCols+` FROM users
		WHERE profile_visibility = 'listed' AND handle_key LIKE '%' || $1 || '%' ESCAPE '\'
		ORDER BY handle_key LIMIT $2`, escapeLike(foldedQuery), limit)
	if err != nil {
		return nil, fmt.Errorf("store: search listed: %w", err)
	}
	return pgkit.ScanAll(rows, []User{}, func(r pgx.Rows) (User, error) {
		u, err := scanUser(r)
		if err != nil {
			return User{}, fmt.Errorf("store: scan user: %w", err)
		}
		return u, nil
	})
}
