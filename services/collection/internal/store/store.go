// Package store owns the collection service's SQL. No other package
// writes queries against this schema. Every method is scoped to a
// user id; rows belonging to another user answer ErrNotFound.
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinels the handlers branch on via errors.Is.
var (
	ErrNotFound         = errors.New("store: not found")
	ErrTagNotFound      = errors.New("store: tag not found")
	ErrNameTaken        = errors.New("store: name taken")
	ErrNotInBacklog     = errors.New("store: not in backlog")
	ErrConflictingOrder = errors.New("store: conflicting order")
	// ErrSubmissionPending means the entry already has a pending
	// submission.
	ErrSubmissionPending = errors.New("store: submission pending")
	// ErrSubmissionResolved means a verdict raced this one; the row is
	// no longer pending.
	ErrSubmissionResolved = errors.New("store: submission resolved")
	// ErrTagCapExceeded means a bulk-update's tag additions would leave a
	// targeted entry holding more than entryTagCap tags; the whole transaction rolls back.
	ErrTagCapExceeded = errors.New("store: entry tag cap exceeded")
	// ErrUserTagCapExceeded means the caller already holds TagCap
	// distinct tags; CreateTag rolls back rather than minting one more.
	ErrUserTagCapExceeded = errors.New("store: user tag cap exceeded")
)

// Store is the query surface over the collection database.
type Store struct{ pool *pgxpool.Pool }

// New builds a Store over the migrated pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// querier is the subset of pgx querying shared by pool and tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// isUniqueViolation reports a Postgres unique_violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}
