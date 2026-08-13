// Entry CRUD, listing, filtering, and bulk mutation.

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/services/collection/internal/rank"
)

// Entry is one physical copy. Nullable columns are pointers. A nil
// ProductID marks a CUSTOM (off-catalog) entry whose display fields
// are user-owned and editable; on product-backed entries they are
// creation-time snapshots and UpdateEntry rewrites them with their
// unchanged current values (product_id stays the live join key for
// prices).
type Entry struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ProductID *uuid.UUID
	ItemType  string
	MediaType string

	DisplayName      string
	PlatformIGDBID   *int64
	PlatformName     *string
	FirstReleaseDate *time.Time
	IGDBGameID       *int64

	Region          string
	Edition         *string
	Packaging       string
	HasBox          bool
	HasManual       bool
	BoxCondition    *string
	ManualCondition *string
	ItemCondition   *string

	PricePaidCents *int64
	Currency       string
	PurchasedAt    *time.Time
	PurchasedFrom  *string

	PricingMode      string
	MatchProvenance  string
	PricingProductID *uuid.UUID

	// The custom-price pair; the DB CHECKs pair them and require the
	// value under pricing_mode custom. set_at is computed in SQL.
	CustomValueCents *int64
	CustomValueSetAt *time.Time

	// The typed custom-price pair (display metadata; the DB CHECKs
	// pair them). custom_value_cents stays the USD value all math
	// uses.
	CustomValueEnteredCents    *int64
	CustomValueEnteredCurrency *string

	Status          string
	Rating          *int
	Notes           *string
	StorageLocation *string
	Pinned          bool
	BacklogRank     *string

	Source      string
	ExternalRef *string
	CoverURL    *string

	// The region-picked presentation snapshot, derived from the
	// product's localization bundles by the entry's region. All nil
	// when the region has no localized form; display falls back to
	// DisplayName / CoverURL.
	LocalizedName         *string
	LocalizedNameTranslit *string
	LocalizedCoverURL     *string

	// The credit snapshot (developer and publisher company names):
	// IGDB company credits where the product carries them, the
	// community block's curated lists as gap-fill, or the user's own
	// facts on a custom entry. nil = no credits known.
	Developers []string
	Publishers []string

	// When the owner dismissed the region-mismatch banner for the
	// entry's CURRENT (region, product_id) choice. UpdateEntry and
	// RepointEntry clear it back to nil whenever either changes, so a
	// new choice notifies once more.
	RegionMismatchAckAt *time.Time

	Tags []TagRef

	CreatedAt time.Time
	UpdatedAt time.Time
}

// entryCols is the canonical SELECT/RETURNING list; scanEntry mirrors
// its order exactly.
const entryCols = `id, user_id, product_id, item_type, media_type,
	display_name, platform_igdb_id, platform_name, first_release_date, igdb_game_id,
	region, edition, packaging, has_box, has_manual,
	box_condition, manual_condition, item_condition,
	price_paid_cents, currency, purchased_at, purchased_from,
	pricing_mode, match_provenance, pricing_product_id,
	status, rating, notes, storage_location, pinned, backlog_rank,
	source, external_ref, created_at, updated_at, cover_url,
	localized_name, localized_name_translit, localized_cover_url,
	region_mismatch_ack_at,
	custom_value_cents, custom_value_set_at,
	custom_value_entered_cents, custom_value_entered_currency,
	developers, publishers`

func scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	err := row.Scan(
		&e.ID, &e.UserID, &e.ProductID, &e.ItemType, &e.MediaType,
		&e.DisplayName, &e.PlatformIGDBID, &e.PlatformName, &e.FirstReleaseDate, &e.IGDBGameID,
		&e.Region, &e.Edition, &e.Packaging, &e.HasBox, &e.HasManual,
		&e.BoxCondition, &e.ManualCondition, &e.ItemCondition,
		&e.PricePaidCents, &e.Currency, &e.PurchasedAt, &e.PurchasedFrom,
		&e.PricingMode, &e.MatchProvenance, &e.PricingProductID,
		&e.Status, &e.Rating, &e.Notes, &e.StorageLocation, &e.Pinned, &e.BacklogRank,
		&e.Source, &e.ExternalRef, &e.CreatedAt, &e.UpdatedAt, &e.CoverURL,
		&e.LocalizedName, &e.LocalizedNameTranslit, &e.LocalizedCoverURL,
		&e.RegionMismatchAckAt,
		&e.CustomValueCents, &e.CustomValueSetAt,
		&e.CustomValueEnteredCents, &e.CustomValueEnteredCurrency,
		&e.Developers, &e.Publishers,
	)
	return e, err
}

// maxRank returns the user's highest backlog rank, or "" for an empty
// backlog (the lower bound for an append-at-end).
func maxRank(ctx context.Context, q querier, userID uuid.UUID) (string, error) {
	var r *string
	err := q.QueryRow(ctx,
		`SELECT max(backlog_rank) FROM entries WHERE user_id = $1 AND status = 'backlog'`,
		userID).Scan(&r)
	if err != nil {
		return "", fmt.Errorf("store: max rank: %w", err)
	}
	if r == nil {
		return "", nil
	}
	return *r, nil
}

// replaceTags rewrites an entry's tag set. Ownership is validated by
// the INSERT itself: selecting from the caller's tags means a foreign
// or unknown id inserts nothing, and the count mismatch surfaces as
// ErrTagNotFound.
func replaceTags(ctx context.Context, q querier, userID, entryID uuid.UUID, tagIDs []uuid.UUID) error {
	if _, err := q.Exec(ctx, `DELETE FROM entry_tags WHERE entry_id = $1`, entryID); err != nil {
		return fmt.Errorf("store: clear tags: %w", err)
	}
	if len(tagIDs) == 0 {
		return nil
	}
	uniq := make([]uuid.UUID, 0, len(tagIDs))
	seen := make(map[uuid.UUID]bool, len(tagIDs))
	for _, id := range tagIDs {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO entry_tags (entry_id, tag_id)
		SELECT $1, t.id FROM tags t WHERE t.user_id = $2 AND t.id = ANY($3)`,
		entryID, userID, uniq)
	if err != nil {
		return fmt.Errorf("store: link tags: %w", err)
	}
	if int(tag.RowsAffected()) != len(uniq) {
		return ErrTagNotFound
	}
	return nil
}

// loadTags fetches an entry's tags ordered by name.
func loadTags(ctx context.Context, q querier, entryID uuid.UUID) ([]TagRef, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, t.name FROM entry_tags et
		JOIN tags t ON t.id = et.tag_id
		WHERE et.entry_id = $1 ORDER BY t.name`, entryID)
	if err != nil {
		return nil, fmt.Errorf("store: load tags: %w", err)
	}
	return scanAll(rows, []TagRef{}, "", func(r pgx.Rows) (TagRef, error) {
		var t TagRef
		if err := r.Scan(&t.ID, &t.Name); err != nil {
			return TagRef{}, fmt.Errorf("store: scan tag: %w", err)
		}
		return t, nil
	})
}

// CreateEntry inserts e for e.UserID, assigning an end-of-backlog rank
// when it arrives in backlog status, and links the given tags. The
// returned Entry carries the generated id, rank, tags, and timestamps.
func (s *Store) CreateEntry(ctx context.Context, e Entry, tagIDs []uuid.UUID) (Entry, error) {
	var out Entry
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if e.Status == "backlog" {
			prev, err := maxRank(ctx, tx, e.UserID)
			if err != nil {
				return err
			}
			r, err := rank.Between(prev, "")
			if err != nil {
				return fmt.Errorf("store: assign rank: %w", err)
			}
			e.BacklogRank = &r
		} else {
			e.BacklogRank = nil
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO entries
			(user_id, product_id, item_type, media_type,
			 display_name, platform_igdb_id, platform_name, first_release_date, igdb_game_id,
			 region, edition, packaging, has_box, has_manual,
			 box_condition, manual_condition, item_condition,
			 price_paid_cents, currency, purchased_at, purchased_from,
			 pricing_mode, pricing_product_id,
			 status, rating, notes, storage_location, pinned, backlog_rank,
			 source, external_ref, cover_url,
			 localized_name, localized_name_translit, localized_cover_url,
			 custom_value_cents, custom_value_set_at,
			 custom_value_entered_cents, custom_value_entered_currency, match_provenance,
			 developers, publishers)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
			        $18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,
			        $33,$34,$35,
			        $36, CASE WHEN $36::bigint IS NULL THEN NULL ELSE now() END,
			        $37, $38, $39, $40, $41)
			RETURNING `+entryCols,
			e.UserID, e.ProductID, e.ItemType, e.MediaType,
			e.DisplayName, e.PlatformIGDBID, e.PlatformName, e.FirstReleaseDate, e.IGDBGameID,
			e.Region, e.Edition, e.Packaging, e.HasBox, e.HasManual,
			e.BoxCondition, e.ManualCondition, e.ItemCondition,
			e.PricePaidCents, e.Currency, e.PurchasedAt, e.PurchasedFrom,
			e.PricingMode, e.PricingProductID,
			e.Status, e.Rating, e.Notes, e.StorageLocation, e.Pinned, e.BacklogRank,
			e.Source, e.ExternalRef, e.CoverURL,
			e.LocalizedName, e.LocalizedNameTranslit, e.LocalizedCoverURL,
			e.CustomValueCents,
			e.CustomValueEnteredCents, e.CustomValueEnteredCurrency,
			// match_provenance and the credit arrays are appended here
			// rather than placed beside pricing_mode and the localized
			// trio, where entryCols lists them: inserting them there
			// would renumber the placeholders already assigned above.
			// The SELECT/RETURNING and INSERT column orders
			// deliberately differ.
			e.MatchProvenance, e.Developers, e.Publishers)
		created, err := scanEntry(row)
		if err != nil {
			return fmt.Errorf("store: create entry: %w", err)
		}
		if err := replaceTags(ctx, tx, e.UserID, created.ID, tagIDs); err != nil {
			return err
		}
		tags, err := loadTags(ctx, tx, created.ID)
		if err != nil {
			return err
		}
		created.Tags = tags
		out = created
		return nil
	})
	if err != nil {
		return Entry{}, err
	}
	return out, nil
}

// GetEntry fetches one of the user's entries with its tags.
func (s *Store) GetEntry(ctx context.Context, userID, id uuid.UUID) (Entry, error) {
	e, err := scanEntry(s.pool.QueryRow(ctx,
		`SELECT `+entryCols+` FROM entries WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("store: get entry: %w", err)
	}
	tags, err := loadTags(ctx, s.pool, e.ID)
	if err != nil {
		return Entry{}, err
	}
	e.Tags = tags
	return e, nil
}

// UpdateEntry replaces the mutable state of the entry selected by
// e.ID + e.UserID and rewrites its tag set. The display fields and
// igdb_game_id ride along (the handler passes them unchanged for
// product-backed entries; for customs they are user-edited, with the
// game identity following the pricing proxy). Status transitions
// manage the rank: entering backlog appends at the end, leaving
// clears, staying keeps the position.
func (s *Store) UpdateEntry(ctx context.Context, e Entry, tagIDs []uuid.UUID) (Entry, error) {
	var out Entry
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var oldStatus string
		var oldRank *string
		err := tx.QueryRow(ctx,
			`SELECT status, backlog_rank FROM entries
			 WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			e.ID, e.UserID).Scan(&oldStatus, &oldRank)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("store: lock entry: %w", err)
		}
		switch {
		case e.Status == "backlog" && oldStatus == "backlog":
			e.BacklogRank = oldRank
		case e.Status == "backlog":
			prev, err := maxRank(ctx, tx, e.UserID)
			if err != nil {
				return err
			}
			r, err := rank.Between(prev, "")
			if err != nil {
				return fmt.Errorf("store: assign rank: %w", err)
			}
			e.BacklogRank = &r
		default:
			e.BacklogRank = nil
		}
		row := tx.QueryRow(ctx, `
			UPDATE entries SET
			 region = $3, edition = $4, packaging = $5, has_box = $6, has_manual = $7,
			 box_condition = $8, manual_condition = $9, item_condition = $10,
			 price_paid_cents = $11, currency = $12, purchased_at = $13, purchased_from = $14,
			 pricing_mode = $15, pricing_product_id = $16,
			 status = $17, rating = $18, notes = $19, storage_location = $20,
			 pinned = $21, backlog_rank = $22,
			 display_name = $23, platform_name = $24, first_release_date = $25,
			 igdb_game_id = $26, product_id = $30,
			 cover_url = $31, platform_igdb_id = $32,
			 localized_name = $33, localized_name_translit = $34,
			 localized_cover_url = $35,
			 custom_value_cents = $27,
			 custom_value_set_at = CASE
			   WHEN $27::bigint IS NOT DISTINCT FROM custom_value_cents THEN custom_value_set_at
			   WHEN $27::bigint IS NULL THEN NULL
			   ELSE now() END,
			 custom_value_entered_cents = $28,
			 custom_value_entered_currency = $29,
			 match_provenance = $36,
			 developers = $37, publishers = $38,
			 region_mismatch_ack_at = CASE
			   WHEN $3::text IS DISTINCT FROM region
			     OR $30::uuid IS DISTINCT FROM product_id THEN NULL
			   ELSE region_mismatch_ack_at END,
			 updated_at = now()
			WHERE id = $1 AND user_id = $2
			RETURNING `+entryCols,
			e.ID, e.UserID,
			e.Region, e.Edition, e.Packaging, e.HasBox, e.HasManual,
			e.BoxCondition, e.ManualCondition, e.ItemCondition,
			e.PricePaidCents, e.Currency, e.PurchasedAt, e.PurchasedFrom,
			e.PricingMode, e.PricingProductID,
			e.Status, e.Rating, e.Notes, e.StorageLocation,
			e.Pinned, e.BacklogRank,
			e.DisplayName, e.PlatformName, e.FirstReleaseDate,
			e.IGDBGameID, e.CustomValueCents,
			e.CustomValueEnteredCents, e.CustomValueEnteredCurrency,
			e.ProductID, e.CoverURL, e.PlatformIGDBID,
			e.LocalizedName, e.LocalizedNameTranslit, e.LocalizedCoverURL,
			e.MatchProvenance, e.Developers, e.Publishers)
		updated, err := scanEntry(row)
		if err != nil {
			return fmt.Errorf("store: update entry: %w", err)
		}
		if err := replaceTags(ctx, tx, e.UserID, updated.ID, tagIDs); err != nil {
			return err
		}
		tags, err := loadTags(ctx, tx, updated.ID)
		if err != nil {
			return err
		}
		updated.Tags = tags
		out = updated
		return nil
	})
	if err != nil {
		return Entry{}, err
	}
	return out, nil
}

// BulkActions is the delta a bulk-update applies across a batch of the
// caller's own entries. A nil Status or StorageLocation leaves that
// dimension untouched; a nil/empty AddTagIDs or RemoveTagIDs means no
// tag change on that side. StorageLocation follows the bulk-update's
// own clearing rule (a non-nil pointer to "" clears the column) -
// the OPPOSITE of the full-replacement update, where an ABSENT field
// clears.
type BulkActions struct {
	AddTagIDs       []uuid.UUID
	RemoveTagIDs    []uuid.UUID
	Status          *string
	StorageLocation *string
}

// entryTagCap is the per-entry tag ceiling every tagging path
// enforces (mirrors EntryCreate/EntryUpdate's tag_ids maxItems); the
// bulk delta checks it explicitly here since it does not go through
// replaceTags' full-replacement shape.
const entryTagCap = 50

// bulkEnterBacklog is BulkUpdateEntries' status='backlog' arm. A flat
// `SET status = 'backlog'` cannot stand alone: the entries table
// CHECKs (status = 'backlog') = (backlog_rank IS NOT NULL), so every
// entry newly entering needs a rank assigned in the SAME write that
// flips its status. Entries already in backlog are left untouched
// entirely (status and rank both already correct - "staying keeps
// the position", same as UpdateEntry); entries newly entering append
// at the end, oldest-created first among the batch, each strictly
// after the last (rank.Between chained off the running max).
func bulkEnterBacklog(ctx context.Context, tx pgx.Tx, userID uuid.UUID, entryIDs []uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT id FROM entries
		WHERE id = ANY($1) AND user_id = $2 AND status <> 'backlog'
		ORDER BY created_at, id FOR UPDATE`,
		entryIDs, userID)
	if err != nil {
		return fmt.Errorf("store: bulk enter backlog: select: %w", err)
	}
	var entering []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("store: bulk enter backlog: scan: %w", err)
		}
		entering = append(entering, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: bulk enter backlog: %w", err)
	}
	if len(entering) == 0 {
		return nil
	}

	prev, err := maxRank(ctx, tx, userID)
	if err != nil {
		return err
	}
	for _, id := range entering {
		r, err := rank.Between(prev, "")
		if err != nil {
			return fmt.Errorf("store: bulk enter backlog: assign rank: %w", err)
		}
		prev = r
		if _, err := tx.Exec(ctx,
			`UPDATE entries SET status = 'backlog', backlog_rank = $2, updated_at = now() WHERE id = $1`,
			id, r); err != nil {
			return fmt.Errorf("store: bulk enter backlog: update: %w", err)
		}
	}
	return nil
}

// BulkUpdateEntries applies actions across the caller's own entries
// named in entryIDs, in ONE transaction. entryIDs the caller does not
// own are silently excluded from every write below (the same
// ownership posture as tag attachment elsewhere in this file); a
// foreign id in AddTagIDs/RemoveTagIDs matches nothing for the same
// reason. If any targeted entry would end up holding more than
// entryTagCap tags, the whole transaction rolls back with
// ErrTagCapExceeded - a skipped-entry partial apply is never allowed.
// Returns the count of entryIDs that are the caller's own, whether or
// not any field they touch actually changed (idempotent re-runs
// report the same count). A status change manages backlog_rank like
// UpdateEntry does (entries table CHECKs status='backlog' exactly
// when backlog_rank is set, so a bare status write cannot skip this):
// entering backlog appends each newly-entering entry at the end
// (oldest-created first among the batch); already-backlog entries
// keep their position; leaving backlog clears it.
func (s *Store) BulkUpdateEntries(ctx context.Context, userID uuid.UUID, entryIDs []uuid.UUID, actions BulkActions) (int, error) {
	var count int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM entries WHERE id = ANY($1) AND user_id = $2`,
			entryIDs, userID).Scan(&count); err != nil {
			return fmt.Errorf("store: bulk update count: %w", err)
		}

		if actions.StorageLocation != nil {
			if _, err := tx.Exec(ctx, `
				UPDATE entries SET storage_location = NULLIF($2, ''), updated_at = now()
				WHERE id = ANY($1) AND user_id = $3`,
				entryIDs, *actions.StorageLocation, userID); err != nil {
				return fmt.Errorf("store: bulk storage location: %w", err)
			}
		}

		if actions.Status != nil {
			if *actions.Status == "backlog" {
				if err := bulkEnterBacklog(ctx, tx, userID, entryIDs); err != nil {
					return err
				}
			} else if _, err := tx.Exec(ctx, `
				UPDATE entries SET status = $2, backlog_rank = NULL, updated_at = now()
				WHERE id = ANY($1) AND user_id = $3`,
				entryIDs, *actions.Status, userID); err != nil {
				return fmt.Errorf("store: bulk status: %w", err)
			}
		}

		if len(actions.RemoveTagIDs) > 0 {
			if _, err := tx.Exec(ctx, `
				DELETE FROM entry_tags
				WHERE tag_id = ANY($1)
				  AND entry_id IN (SELECT id FROM entries WHERE id = ANY($2) AND user_id = $3)`,
				actions.RemoveTagIDs, entryIDs, userID); err != nil {
				return fmt.Errorf("store: bulk remove tags: %w", err)
			}
		}

		if len(actions.AddTagIDs) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO entry_tags (entry_id, tag_id)
				SELECT e.id, t.id FROM entries e, tags t
				WHERE e.id = ANY($1) AND e.user_id = $3
				  AND t.id = ANY($2) AND t.user_id = $3
				ON CONFLICT DO NOTHING`,
				entryIDs, actions.AddTagIDs, userID); err != nil {
				return fmt.Errorf("store: bulk add tags: %w", err)
			}
			var overCap bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM entry_tags et
					JOIN entries e ON e.id = et.entry_id
					WHERE et.entry_id = ANY($1) AND e.user_id = $2
					GROUP BY et.entry_id HAVING count(*) > $3
				)`, entryIDs, userID, entryTagCap).Scan(&overCap); err != nil {
				return fmt.Errorf("store: bulk tag cap check: %w", err)
			}
			if overCap {
				return ErrTagCapExceeded
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// AckRegionMismatch stamps the acknowledgement time for the owner's
// entry, for its current (region, product_id) choice. The ownership
// WHERE doubles as the existence check, same shape as DeleteEntry;
// every call restamps now() (no already-acked guard - the exact
// moment carries no meaning beyond non-null, unlike a submission
// verdict's acknowledgement).
func (s *Store) AckRegionMismatch(ctx context.Context, userID, entryID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE entries SET region_mismatch_ack_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2`, entryID, userID)
	if err != nil {
		return fmt.Errorf("store: ack region mismatch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteEntry removes one of the user's entries (tag links cascade).
func (s *Store) DeleteEntry(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM entries WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("store: delete entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Reorder moves a backlog entry between two neighbors (either may be
// nil at a list edge). The entry and neighbors are locked in one
// deterministic-order statement; the neighbors' ranks must straddle,
// otherwise the drag was against a list that has moved.
func (s *Store) Reorder(ctx context.Context, userID, entryID uuid.UUID, afterID, beforeID *uuid.UUID) (Entry, error) {
	var out Entry
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ids := []uuid.UUID{entryID}
		if afterID != nil {
			ids = append(ids, *afterID)
		}
		if beforeID != nil {
			ids = append(ids, *beforeID)
		}
		rows, err := tx.Query(ctx, `
			SELECT id, status, backlog_rank FROM entries
			WHERE user_id = $1 AND id = ANY($2)
			ORDER BY id FOR UPDATE`, userID, ids)
		if err != nil {
			return fmt.Errorf("store: lock rows: %w", err)
		}
		type lockedRow struct {
			status string
			rank   *string
		}
		locked := map[uuid.UUID]lockedRow{}
		for rows.Next() {
			var id uuid.UUID
			var lr lockedRow
			if err := rows.Scan(&id, &lr.status, &lr.rank); err != nil {
				rows.Close()
				return fmt.Errorf("store: scan lock: %w", err)
			}
			locked[id] = lr
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("store: lock rows: %w", err)
		}

		self, ok := locked[entryID]
		if !ok {
			return ErrNotFound
		}
		if self.status != "backlog" {
			return ErrNotInBacklog
		}
		prev, next := "", ""
		if afterID != nil {
			n, ok := locked[*afterID]
			if !ok || n.status != "backlog" || n.rank == nil {
				return ErrNotInBacklog
			}
			prev = *n.rank
		}
		if beforeID != nil {
			n, ok := locked[*beforeID]
			if !ok || n.status != "backlog" || n.rank == nil {
				return ErrNotInBacklog
			}
			next = *n.rank
		}
		r, err := rank.Between(prev, next)
		if err != nil {
			return ErrConflictingOrder
		}
		row := tx.QueryRow(ctx, `
			UPDATE entries SET backlog_rank = $3, updated_at = now()
			WHERE id = $1 AND user_id = $2
			RETURNING `+entryCols, entryID, userID, r)
		moved, err := scanEntry(row)
		if err != nil {
			return fmt.Errorf("store: reorder: %w", err)
		}
		tags, err := loadTags(ctx, tx, moved.ID)
		if err != nil {
			return err
		}
		moved.Tags = tags
		out = moved
		return nil
	})
	if err != nil {
		return Entry{}, err
	}
	return out, nil
}

// Filters is the list-matrix input. Multi-value dimensions OR within
// themselves and AND across; TagIDs requires ALL listed tags. Sort and
// Order arrive pre-validated against the contract enums.
type Filters struct {
	ItemTypes      []string
	Statuses       []string
	Packagings     []string
	Regions        []string
	ItemConditions []string
	PlatformIDs    []int64
	TagIDs         []uuid.UUID
	Developers     []string
	Publishers     []string
	Sort           string
	Order          string
}

// Filtered reports whether any dimension narrows the entry set.
func (f Filters) Filtered() bool {
	return len(f.ItemTypes)+len(f.Statuses)+len(f.Packagings)+len(f.Regions)+
		len(f.ItemConditions)+len(f.PlatformIDs)+len(f.TagIDs)+
		len(f.Developers)+len(f.Publishers) > 0
}

// filterWhere builds the WHERE clauses and args every entries query
// shares: the list and the dashboard aggregates narrow by the same
// filter matrix.
func filterWhere(userID uuid.UUID, f Filters) ([]string, []any) {
	where := []string{"user_id = $1"}
	args := []any{userID}
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if len(f.ItemTypes) > 0 {
		add("item_type = ANY($%d)", f.ItemTypes)
	}
	if len(f.Statuses) > 0 {
		add("status = ANY($%d)", f.Statuses)
	}
	if len(f.Packagings) > 0 {
		add("packaging = ANY($%d)", f.Packagings)
	}
	if len(f.Regions) > 0 {
		add("region = ANY($%d)", f.Regions)
	}
	if len(f.ItemConditions) > 0 {
		add("item_condition = ANY($%d)", f.ItemConditions)
	}
	if len(f.PlatformIDs) > 0 {
		add("platform_igdb_id = ANY($%d)", f.PlatformIDs)
	}
	// Overlap, not equality: a filter value matches any entry whose
	// credit array contains it, so multi-company entries match each of
	// their companies.
	if len(f.Developers) > 0 {
		add("developers && $%d", f.Developers)
	}
	if len(f.Publishers) > 0 {
		add("publishers && $%d", f.Publishers)
	}
	if len(f.TagIDs) > 0 {
		uniq := make([]uuid.UUID, 0, len(f.TagIDs))
		seen := make(map[uuid.UUID]bool, len(f.TagIDs))
		for _, id := range f.TagIDs {
			if !seen[id] {
				seen[id] = true
				uniq = append(uniq, id)
			}
		}
		args = append(args, uniq, len(uniq))
		where = append(where, fmt.Sprintf(
			`id IN (SELECT entry_id FROM entry_tags WHERE tag_id = ANY($%d)
			        GROUP BY entry_id HAVING count(*) = $%d)`,
			len(args)-1, len(args)))
	}
	return where, args
}

// orderClause maps a validated sort dimension onto SQL. The switch is
// the whitelist: nothing user-supplied is ever concatenated.
func orderClause(sort, order string) string {
	dir := "ASC"
	if order == "desc" {
		dir = "DESC"
	}
	switch sort {
	case "name":
		return "pinned DESC, lower(display_name) " + dir + ", created_at DESC, id"
	case "release_date":
		return "pinned DESC, first_release_date " + dir + " NULLS LAST, created_at DESC, id"
	case "purchased_at":
		return "pinned DESC, purchased_at " + dir + " NULLS LAST, created_at DESC, id"
	case "paid":
		return "pinned DESC, price_paid_cents " + dir + " NULLS LAST, created_at DESC, id"
	case "rating":
		return "pinned DESC, rating " + dir + " NULLS LAST, created_at DESC, id"
	case "created_at":
		return "pinned DESC, created_at " + dir + ", id"
	case "backlog_rank":
		// The drag-order read: pure rank order, no pinned prefix, or
		// the frontend's drop-slot neighbors would disagree with rank
		// adjacency.
		return "backlog_rank " + dir + " NULLS LAST, created_at DESC, id"
	default:
		// "value" sorts in the handler after price composition; this is
		// the stable base order it starts from.
		return "pinned DESC, created_at DESC, id"
	}
}

// ListEntries runs the filter matrix in SQL and bulk-loads tags.
func (s *Store) ListEntries(ctx context.Context, userID uuid.UUID, f Filters) ([]Entry, error) {
	where, args := filterWhere(userID, f)
	query := `SELECT ` + entryCols + ` FROM entries WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY ` + orderClause(f.Sort, f.Order)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list entries: %w", err)
	}
	entries, err := scanAll(rows, []Entry{}, "list entries", func(r pgx.Rows) (Entry, error) {
		e, err := scanEntry(r)
		if err != nil {
			return Entry{}, fmt.Errorf("store: scan entry: %w", err)
		}
		return e, nil
	})
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return entries, nil
	}
	ids := make([]uuid.UUID, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	byEntry, err := loadTagsBulk(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if t, ok := byEntry[entries[i].ID]; ok {
			entries[i].Tags = t
		} else {
			entries[i].Tags = []TagRef{}
		}
	}
	return entries, nil
}

// loadTagsBulk fetches tags for many entries in one query, each
// entry's tags ordered by name.
func loadTagsBulk(ctx context.Context, q querier, entryIDs []uuid.UUID) (map[uuid.UUID][]TagRef, error) {
	rows, err := q.Query(ctx, `
		SELECT et.entry_id, t.id, t.name FROM entry_tags et
		JOIN tags t ON t.id = et.tag_id
		WHERE et.entry_id = ANY($1) ORDER BY t.name`, entryIDs)
	if err != nil {
		return nil, fmt.Errorf("store: bulk tags: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]TagRef{}
	for rows.Next() {
		var entryID uuid.UUID
		var t TagRef
		if err := rows.Scan(&entryID, &t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("store: scan bulk tag: %w", err)
		}
		out[entryID] = append(out[entryID], t)
	}
	return out, rows.Err()
}

// CountEntriesFiltered sizes a shelf (its filtered entry set).
func (s *Store) CountEntriesFiltered(ctx context.Context, userID uuid.UUID, f Filters) (int, error) {
	where, args := filterWhere(userID, f)
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM entries WHERE `+strings.Join(where, " AND "), args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count filtered: %w", err)
	}
	return n, nil
}
