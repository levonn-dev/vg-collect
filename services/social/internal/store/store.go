// Package store owns the social service's SQL. An activity row is written
// only when its edge actually inserts, and deleted when the edge is
// retracted, so a retried idempotent PUT never double-posts to feeds.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrForbidden   = errors.New("forbidden")
	ErrCapExceeded = errors.New("cap exceeded")
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Cursor is the keyset position (created_at, id), encoded
// "<unixnano>.<uuid>"; stable under inserts, unlike offsets. bff shares
// this httpkit type across the service boundary to avoid wire-format drift.
type Cursor = httpkit.Cursor

var ParseCursor = httpkit.ParseCursor

// capCount counts a user's rows in the rolling 24h window from a live
// table. Only comments use this: rows are tombstoned, not deleted, so the
// count already includes retract-then-recreate cycling. Follows/likes
// hard-delete their edge, so they read cap_events instead (see capEventCount).
func capCount(ctx context.Context, tx pgx.Tx, table, col string, user uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM `+table+` WHERE `+col+` = $1 AND created_at > now() - interval '24 hours'`,
		user).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: cap count %s: %w", table, err)
	}
	return n, nil
}

// capEventCount counts a user's rolling-24h ('follow'/'like') actions from
// cap_events, an append-only log that outlives the edge (see capCount for
// why comments don't need this); unaffected by the 48h sweep's timing.
func capEventCount(ctx context.Context, tx pgx.Tx, kind string, user uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM cap_events WHERE user_id = $1 AND kind = $2 AND created_at > now() - interval '24 hours'`,
		user, kind).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: cap event count %s: %w", kind, err)
	}
	return n, nil
}

// recordCapEvent charges one action against the rolling-24h cap; called
// only after the cap check passes, so an idempotent retry is never charged.
// Sweeps rows older than 48h first, so the table self-retains with no job.
func recordCapEvent(ctx context.Context, tx pgx.Tx, kind string, user uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM cap_events WHERE created_at < now() - interval '48 hours'`); err != nil {
		return fmt.Errorf("store: sweep cap events: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO cap_events (user_id, kind) VALUES ($1, $2)`, user, kind); err != nil {
		return fmt.Errorf("store: cap event: %w", err)
	}
	return nil
}

// Follow inserts the edge and, iff it inserted, the event; returns
// whether this call inserted. The edge insert runs before the cap check,
// so a retry of an already-held edge is never charged. The cap counts
// cap_events, not the live edge, so unfollowing can't re-earn headroom.
func (s *Store) Follow(ctx context.Context, follower, followee uuid.UUID, cap int) (bool, error) {
	inserted := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, follower, followee)
		if err != nil {
			return fmt.Errorf("store: follow: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		n, err := capEventCount(ctx, tx, "follow", follower)
		if err != nil {
			return err
		}
		if n >= cap {
			return ErrCapExceeded
		}
		inserted = true
		if err := recordCapEvent(ctx, tx, "follow", follower); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity (actor_id, verb, target_user_id)
			VALUES ($1, 'followed_user', $2)`, follower, followee); err != nil {
			return fmt.Errorf("store: follow event: %w", err)
		}
		return nil
	})
	return inserted, err
}

// Unfollow removes the edge and its event (feeds never show
// retracted actions). Idempotent.
func (s *Store) Unfollow(ctx context.Context, follower, followee uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, follower, followee)
		if err != nil {
			return fmt.Errorf("store: unfollow: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM activity
			WHERE actor_id = $1 AND verb = 'followed_user' AND target_user_id = $2`,
			follower, followee); err != nil {
			return fmt.Errorf("store: unfollow event: %w", err)
		}
		return nil
	})
}

// ProfileSummary answers the profile page's social strip in one call.
func (s *Store) ProfileSummary(ctx context.Context, userID, viewer uuid.UUID) (int, int, bool, error) {
	var followers, following int
	var viewerFollows bool
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM follows WHERE followee_id = $1),
			(SELECT count(*) FROM follows WHERE follower_id = $1),
			EXISTS(SELECT 1 FROM follows WHERE follower_id = $2 AND followee_id = $1)`,
		userID, viewer).Scan(&followers, &following, &viewerFollows)
	if err != nil {
		return 0, 0, false, fmt.Errorf("store: profile summary: %w", err)
	}
	return followers, following, viewerFollows, nil
}

// FolloweeIDs lists who a user follows (the following-feed scope).
func (s *Store) FolloweeIDs(ctx context.Context, follower uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT followee_id FROM follows WHERE follower_id = $1`, follower)
	if err != nil {
		return nil, fmt.Errorf("store: followees: %w", err)
	}
	return pgkit.ScanAll(rows, []uuid.UUID{}, func(r pgx.Rows) (uuid.UUID, error) {
		var id uuid.UUID
		if err := r.Scan(&id); err != nil {
			return uuid.UUID{}, fmt.Errorf("store: scan followee: %w", err)
		}
		return id, nil
	})
}

// Like inserts the edge (+event iff inserted); shelfOwner is denormalized
// from the caller's collection resolve. As with Follow, the cap counts
// cap_events, not the live edge, so unliking can't re-earn headroom.
func (s *Store) Like(ctx context.Context, user, shelf, shelfOwner uuid.UUID, cap int) (bool, error) {
	inserted := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO likes (user_id, shelf_id, shelf_owner_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, user, shelf, shelfOwner)
		if err != nil {
			return fmt.Errorf("store: like: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		n, err := capEventCount(ctx, tx, "like", user)
		if err != nil {
			return err
		}
		if n >= cap {
			return ErrCapExceeded
		}
		inserted = true
		if err := recordCapEvent(ctx, tx, "like", user); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity (actor_id, verb, object_shelf_id, target_user_id)
			VALUES ($1, 'liked_shelf', $2, $3)`, user, shelf, shelfOwner); err != nil {
			return fmt.Errorf("store: like event: %w", err)
		}
		return nil
	})
	return inserted, err
}

// Unlike removes the edge and its event. Idempotent.
func (s *Store) Unlike(ctx context.Context, user, shelf uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM likes WHERE user_id = $1 AND shelf_id = $2`, user, shelf)
		if err != nil {
			return fmt.Errorf("store: unlike: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM activity
			WHERE actor_id = $1 AND verb = 'liked_shelf' AND object_shelf_id = $2`,
			user, shelf); err != nil {
			return fmt.Errorf("store: unlike event: %w", err)
		}
		return nil
	})
}

// ShelfSummary is the batch social strip for shelf cards.
type ShelfSummary struct {
	ShelfID      uuid.UUID
	LikeCount    int
	CommentCount int
	ViewerLikes  bool
}

// ShelfSummaries answers every requested id (zeroed when no rows).
func (s *Store) ShelfSummaries(ctx context.Context, ids []uuid.UUID, viewer uuid.UUID) ([]ShelfSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT req.id,
		       (SELECT count(*) FROM likes WHERE shelf_id = req.id),
		       (SELECT count(*) FROM comments WHERE shelf_id = req.id AND deleted_at IS NULL),
		       EXISTS(SELECT 1 FROM likes WHERE shelf_id = req.id AND user_id = $2)
		FROM unnest($1::uuid[]) AS req(id)`, ids, viewer)
	if err != nil {
		return nil, fmt.Errorf("store: shelf summaries: %w", err)
	}
	return pgkit.ScanAll(rows, []ShelfSummary{}, func(r pgx.Rows) (ShelfSummary, error) {
		var x ShelfSummary
		if err := r.Scan(&x.ShelfID, &x.LikeCount, &x.CommentCount, &x.ViewerLikes); err != nil {
			return ShelfSummary{}, fmt.Errorf("store: scan summary: %w", err)
		}
		return x, nil
	})
}

// TopShelves is the all-time like-count leaderboard.
func (s *Store) TopShelves(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT shelf_id FROM likes
		GROUP BY shelf_id ORDER BY count(*) DESC, shelf_id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: top shelves: %w", err)
	}
	return pgkit.ScanAll(rows, []uuid.UUID{}, func(r pgx.Rows) (uuid.UUID, error) {
		var id uuid.UUID
		if err := r.Scan(&id); err != nil {
			return uuid.UUID{}, fmt.Errorf("store: scan top: %w", err)
		}
		return id, nil
	})
}

// Pool exposes the pool for test assertions only.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
