package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Event is one activity row - ids only, never visibility; the bff
// hydrates and gates at read time.
type Event struct {
	ID              uuid.UUID
	ActorID         uuid.UUID
	Verb            string
	ObjectShelfID   *uuid.UUID
	ObjectCommentID *uuid.UUID
	TargetUserID    uuid.UUID
	CreatedAt       time.Time
}

const eventCols = `id, actor_id, verb, object_shelf_id, object_comment_id, target_user_id, created_at`

func scanEvent(row pgx.Row) (Event, error) {
	var e Event
	err := row.Scan(&e.ID, &e.ActorID, &e.Verb, &e.ObjectShelfID, &e.ObjectCommentID,
		&e.TargetUserID, &e.CreatedAt)
	return e, err
}

// Feed pages raw events for a tab. following = events by people the
// viewer follows; you = events targeting the viewer by others.
func (s *Store) Feed(ctx context.Context, viewer uuid.UUID, tab string, cursor *Cursor, limit int) ([]Event, error) {
	after := time.Now().Add(time.Hour)
	afterID := uuid.Max
	if cursor != nil {
		after, afterID = cursor.CreatedAt, cursor.ID
	}
	var query string
	switch tab {
	case "following":
		// Per-followee top-K merge: each followee contributes at most
		// one LIMIT worth of rows via its own (actor_id, created_at,
		// id) index walk, and the outer sort merges those small sets.
		// A flat IN-list query would instead gather every candidate
		// row from every followee before sorting, so its cost grows
		// with total activity volume; this shape's grows only with the
		// followee count. The result is provably identical: the global
		// newest LIMIT rows are always contained in the union of each
		// followee's newest LIMIT rows below the cursor.
		query = `SELECT a.* FROM follows f
			CROSS JOIN LATERAL (
				SELECT ` + eventCols + ` FROM activity
				WHERE actor_id = f.followee_id
				  AND (created_at, id) < ($2, $3)
				ORDER BY created_at DESC, id DESC LIMIT $4
			) a
			WHERE f.follower_id = $1
			ORDER BY a.created_at DESC, a.id DESC LIMIT $4`
	case "you":
		query = `SELECT ` + eventCols + ` FROM activity
			WHERE target_user_id = $1 AND actor_id <> $1
			  AND (created_at, id) < ($2, $3)
			ORDER BY created_at DESC, id DESC LIMIT $4`
	default:
		return nil, fmt.Errorf("store: unknown feed tab %q", tab)
	}
	rows, err := s.pool.Query(ctx, query, viewer, after, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: feed: %w", err)
	}
	return scanAll(rows, func(r pgx.Rows) (Event, error) {
		e, err := scanEvent(r)
		if err != nil {
			return Event{}, fmt.Errorf("store: scan event: %w", err)
		}
		return e, nil
	})
}

// RecordPublish keeps exactly one live publish row per shelf.
// Republish refreshes created_at (a feed bump) at most once per
// throttle window; inside the window it is a no-op ("throttled").
func (s *Store) RecordPublish(ctx context.Context, actor, shelf uuid.UUID, throttle time.Duration) (string, error) {
	outcome := ""
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO activity (actor_id, verb, object_shelf_id, target_user_id)
			VALUES ($1, 'published_shelf', $2, $1)
			ON CONFLICT (object_shelf_id) WHERE verb = 'published_shelf' DO NOTHING`,
			actor, shelf)
		if err != nil {
			return fmt.Errorf("store: record publish: %w", err)
		}
		if tag.RowsAffected() > 0 {
			outcome = "created"
			return nil
		}
		// pgx does not bind time.Duration to interval; pass seconds and
		// build the interval in SQL via make_interval.
		tag, err = tx.Exec(ctx, `
			UPDATE activity SET created_at = now(), actor_id = $1, target_user_id = $1
			WHERE verb = 'published_shelf' AND object_shelf_id = $2
			  AND created_at < now() - make_interval(secs => $3)`,
			actor, shelf, throttle.Seconds())
		if err != nil {
			return fmt.Errorf("store: refresh publish: %w", err)
		}
		if tag.RowsAffected() > 0 {
			outcome = "refreshed"
		} else {
			outcome = "throttled"
		}
		return nil
	})
	return outcome, err
}

// PurgeUser is the account-deletion leg: fully self-contained
// (denormalized shelf_owner_id) and idempotent. Hard-deletes the
// graph; anonymizes authored comments on surviving shelves.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		steps := []struct{ q string }{
			{`DELETE FROM follows WHERE follower_id = $1 OR followee_id = $1`},
			{`DELETE FROM likes WHERE user_id = $1 OR shelf_owner_id = $1`},
			// cap_events would self-expire within 48h anyway, but purge
			// leaves nothing behind on principle.
			{`DELETE FROM cap_events WHERE user_id = $1`},
			{`DELETE FROM activity WHERE actor_id = $1 OR target_user_id = $1`},
			{`DELETE FROM comments WHERE shelf_owner_id = $1`},
			// deleted_by is nulled only when it names the purged user
			// (their own prior self-delete); a shelf owner's earlier
			// removal of this comment must survive purge-anonymization
			// intact - the owner is not who is being purged, and erasing
			// their moderation attribution here would be collateral
			// damage the owner never asked for.
			{`UPDATE comments SET author_id = NULL, body = NULL,
				deleted_at = COALESCE(deleted_at, now()),
				deleted_by = CASE WHEN deleted_by = $1 THEN NULL ELSE deleted_by END
				WHERE author_id = $1`},
			{`UPDATE comments SET deleted_by = NULL WHERE deleted_by = $1`},
		}
		for _, st := range steps {
			if _, err := tx.Exec(ctx, st.q, userID); err != nil {
				return fmt.Errorf("store: purge: %w", err)
			}
		}
		return nil
	})
}
