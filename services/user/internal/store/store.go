// Package store owns the user service's SQL. No other package writes
// queries against this schema.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("user not found")

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
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("store: scan role: %w", err)
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	AvatarURL   *string
	Roles       []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Upsert creates the user on first login; an existing account is
// returned untouched (the profile belongs to the user once created,
// so logins never overwrite display name or avatar). The default
// `user` role is granted idempotently, all in one transaction.
func (s *Store) Upsert(ctx context.Context, email, displayName string, avatarURL *string) (User, error) {
	var u User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO users (email, display_name, avatar_url)
			VALUES ($1, $2, $3)
			ON CONFLICT (email) DO NOTHING
			RETURNING id, email, display_name, avatar_url, created_at, updated_at`,
			email, displayName, avatarURL,
		).Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			// Existing account: read it (the conflicting insert, if
			// concurrent, has committed by the time DO NOTHING returns).
			err = tx.QueryRow(ctx, `
				SELECT id, email, display_name, avatar_url, created_at, updated_at
				FROM users WHERE email = $1`, email,
			).Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
		}
		if err != nil {
			return fmt.Errorf("store: upsert: %w", err)
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
		return User{}, err
	}
	return u, nil
}

// Update edits the self-serviceable profile fields. displayName nil
// keeps the current value; avatarURL nil keeps, empty string clears.
func (s *Store) Update(ctx context.Context, id uuid.UUID, displayName, avatarURL *string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		UPDATE users SET
			display_name = COALESCE($2, display_name),
			avatar_url = CASE
				WHEN $3::text IS NULL THEN avatar_url
				WHEN $3 = '' THEN NULL
				ELSE $3
			END,
			updated_at = now()
		WHERE id = $1
		RETURNING id, email, display_name, avatar_url, created_at, updated_at`,
		id, displayName, avatarURL,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("store: update: %w", err)
	}
	roles, err := rolesQ(ctx, s.pool, id)
	if err != nil {
		return User{}, err
	}
	u.Roles = roles
	return u, nil
}

// Delete removes the account row (roles cascade). Deleting a missing
// user is a no-op: account deletion retries must converge.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: delete: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, avatar_url, created_at, updated_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
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
