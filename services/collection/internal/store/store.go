// Package store owns the collection service's SQL. No other package
// writes queries against this schema. Every method is scoped to a
// user id; rows belonging to another user answer ErrNotFound.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vg-collect/services/collection/internal/rank"
)

// Sentinels the handlers branch on via errors.Is.
var (
	ErrNotFound         = errors.New("store: not found")
	ErrTagNotFound      = errors.New("store: tag not found")
	ErrNameTaken        = errors.New("store: name taken")
	ErrNotInBacklog     = errors.New("store: not in backlog")
	ErrConflictingOrder = errors.New("store: conflicting order")
)

// Store is the query surface over the collection database.
type Store struct{ pool *pgxpool.Pool }

// New builds a Store over the migrated pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

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

	Tags []TagRef

	CreatedAt time.Time
	UpdatedAt time.Time
}

// querier is the subset of pgx querying shared by pool and tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// entryCols is the canonical SELECT/RETURNING list; scanEntry mirrors
// its order exactly.
const entryCols = `id, user_id, product_id, item_type, media_type,
	display_name, platform_igdb_id, platform_name, first_release_date, igdb_game_id,
	region, edition, packaging, has_box, has_manual,
	box_condition, manual_condition, item_condition,
	price_paid_cents, currency, purchased_at, purchased_from,
	pricing_mode, pricing_product_id,
	status, rating, notes, storage_location, pinned, backlog_rank,
	source, external_ref, created_at, updated_at, cover_url,
	custom_value_cents, custom_value_set_at,
	custom_value_entered_cents, custom_value_entered_currency`

func scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	err := row.Scan(
		&e.ID, &e.UserID, &e.ProductID, &e.ItemType, &e.MediaType,
		&e.DisplayName, &e.PlatformIGDBID, &e.PlatformName, &e.FirstReleaseDate, &e.IGDBGameID,
		&e.Region, &e.Edition, &e.Packaging, &e.HasBox, &e.HasManual,
		&e.BoxCondition, &e.ManualCondition, &e.ItemCondition,
		&e.PricePaidCents, &e.Currency, &e.PurchasedAt, &e.PurchasedFrom,
		&e.PricingMode, &e.PricingProductID,
		&e.Status, &e.Rating, &e.Notes, &e.StorageLocation, &e.Pinned, &e.BacklogRank,
		&e.Source, &e.ExternalRef, &e.CreatedAt, &e.UpdatedAt, &e.CoverURL,
		&e.CustomValueCents, &e.CustomValueSetAt,
		&e.CustomValueEnteredCents, &e.CustomValueEnteredCurrency,
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
	defer rows.Close()
	tags := []TagRef{}
	for rows.Next() {
		var t TagRef
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
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
			 source, external_ref, cover_url, custom_value_cents, custom_value_set_at,
			 custom_value_entered_cents, custom_value_entered_currency)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
			        $18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,
			        $33, CASE WHEN $33::bigint IS NULL THEN NULL ELSE now() END,
			        $34, $35)
			RETURNING `+entryCols,
			e.UserID, e.ProductID, e.ItemType, e.MediaType,
			e.DisplayName, e.PlatformIGDBID, e.PlatformName, e.FirstReleaseDate, e.IGDBGameID,
			e.Region, e.Edition, e.Packaging, e.HasBox, e.HasManual,
			e.BoxCondition, e.ManualCondition, e.ItemCondition,
			e.PricePaidCents, e.Currency, e.PurchasedAt, e.PurchasedFrom,
			e.PricingMode, e.PricingProductID,
			e.Status, e.Rating, e.Notes, e.StorageLocation, e.Pinned, e.BacklogRank,
			e.Source, e.ExternalRef, e.CoverURL, e.CustomValueCents,
			e.CustomValueEnteredCents, e.CustomValueEnteredCurrency)
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
			 custom_value_cents = $27,
			 custom_value_set_at = CASE
			   WHEN $27::bigint IS NOT DISTINCT FROM custom_value_cents THEN custom_value_set_at
			   WHEN $27::bigint IS NULL THEN NULL
			   ELSE now() END,
			 custom_value_entered_cents = $28,
			 custom_value_entered_currency = $29,
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
			e.ProductID)
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

// GameEntryRef is the resnapshot walk's row: just enough to recompute
// one game-backed entry's date pick.
type GameEntryRef struct {
	EntryID          uuid.UUID
	ProductID        uuid.UUID
	Region           string
	FirstReleaseDate *time.Time
}

// ListGameBackedRefs lists every user's game-backed entries (product
// and igdb game both present) for the one-shot release-date
// resnapshot; deliberately unscoped - the pick derives from product +
// entry region, nothing user-private.
func (s *Store) ListGameBackedRefs(ctx context.Context) ([]GameEntryRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, product_id, region, first_release_date FROM entries
		WHERE product_id IS NOT NULL AND igdb_game_id IS NOT NULL
		ORDER BY product_id, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list game-backed refs: %w", err)
	}
	defer rows.Close()
	var out []GameEntryRef
	for rows.Next() {
		var r GameEntryRef
		if err := rows.Scan(&r.EntryID, &r.ProductID, &r.Region, &r.FirstReleaseDate); err != nil {
			return nil, fmt.Errorf("store: list game-backed refs: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list game-backed refs: %w", err)
	}
	return out, nil
}

// SetFirstReleaseDate narrowly rewrites one entry's snapshotted date
// (the resnapshot walk's only write).
func (s *Store) SetFirstReleaseDate(ctx context.Context, entryID uuid.UUID, d *time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE entries SET first_release_date = $2, updated_at = now() WHERE id = $1`,
		entryID, d)
	if err != nil {
		return fmt.Errorf("store: set first release date: %w", err)
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

// CreateTag creates a user-scoped tag; names are unique per user
// case-insensitively (citext).
func (s *Store) CreateTag(ctx context.Context, userID uuid.UUID, name string) (Tag, error) {
	var t Tag
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tags (user_id, name) VALUES ($1, $2)
		RETURNING id, name`, userID, name).Scan(&t.ID, &t.Name)
	if isUniqueViolation(err) {
		return Tag{}, ErrNameTaken
	}
	if err != nil {
		return Tag{}, fmt.Errorf("store: create tag: %w", err)
	}
	return t, nil
}

// isUniqueViolation reports a Postgres unique_violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
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
	Sort           string
	Order          string
}

// Filtered reports whether any dimension narrows the entry set.
func (f Filters) Filtered() bool {
	return len(f.ItemTypes)+len(f.Statuses)+len(f.Packagings)+len(f.Regions)+
		len(f.ItemConditions)+len(f.PlatformIDs)+len(f.TagIDs) > 0
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
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list entries: %w", err)
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

// LibraryGame is one deduplicated owned game for recommendation
// scoring: the best rating across copies, and dropped only when every
// copy is dropped.
type LibraryGame struct {
	IGDBGameID int64
	Rating     *int
	AllDropped bool
}

// LibrarySummary aggregates the user's games that carry an IGDB id.
func (s *Store) LibrarySummary(ctx context.Context, userID uuid.UUID) ([]LibraryGame, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT igdb_game_id, max(rating), bool_and(status = 'dropped')
		FROM entries
		WHERE user_id = $1 AND igdb_game_id IS NOT NULL
		GROUP BY igdb_game_id ORDER BY igdb_game_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: library summary: %w", err)
	}
	defer rows.Close()
	out := []LibraryGame{}
	for rows.Next() {
		var g LibraryGame
		if err := rows.Scan(&g.IGDBGameID, &g.Rating, &g.AllDropped); err != nil {
			return nil, fmt.Errorf("store: scan library game: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
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
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.EntryCount); err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
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

// View is a saved list configuration; Params is the frontend's opaque
// JSON document, stored and returned verbatim.
type View struct {
	ID        uuid.UUID
	Name      string
	Params    []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

const viewCols = `id, name, params, created_at, updated_at`

func scanView(row pgx.Row) (View, error) {
	var v View
	err := row.Scan(&v.ID, &v.Name, &v.Params, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

// ListViews returns the user's saved views ordered by name.
func (s *Store) ListViews(ctx context.Context, userID uuid.UUID) ([]View, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+viewCols+` FROM saved_views WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list views: %w", err)
	}
	defer rows.Close()
	out := []View{}
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan view: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateView saves a view; names are unique per user case-insensitively.
func (s *Store) CreateView(ctx context.Context, userID uuid.UUID, name string, params []byte) (View, error) {
	v, err := scanView(s.pool.QueryRow(ctx, `
		INSERT INTO saved_views (user_id, name, params) VALUES ($1, $2, $3)
		RETURNING `+viewCols, userID, name, params))
	if isUniqueViolation(err) {
		return View{}, ErrNameTaken
	}
	if err != nil {
		return View{}, fmt.Errorf("store: create view: %w", err)
	}
	return v, nil
}

// UpdateView replaces a view's name and params.
func (s *Store) UpdateView(ctx context.Context, userID, id uuid.UUID, name string, params []byte) (View, error) {
	v, err := scanView(s.pool.QueryRow(ctx, `
		UPDATE saved_views SET name = $3, params = $4, updated_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING `+viewCols, id, userID, name, params))
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if isUniqueViolation(err) {
		return View{}, ErrNameTaken
	}
	if err != nil {
		return View{}, fmt.Errorf("store: update view: %w", err)
	}
	return v, nil
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

// PlatformCount is one dashboard platform bucket ("" = no platform).
type PlatformCount struct {
	Name  string
	Count int
}

// CurrencySpend is the paid total for one currency.
type CurrencySpend struct {
	Currency   string
	TotalCents int64
}

// DashboardCounts is the SQL half of the dashboard.
type DashboardCounts struct {
	Total      int
	ByStatus   map[string]int
	ByItemType map[string]int
	ByPlatform []PlatformCount
	Spend      []CurrencySpend
}

// DashboardCounts aggregates the user's collection, narrowed by the
// same filter matrix as ListEntries (zero-value Filters = everything).
func (s *Store) DashboardCounts(ctx context.Context, userID uuid.UUID, f Filters) (DashboardCounts, error) {
	where, args := filterWhere(userID, f)
	cond := strings.Join(where, " AND ")
	out := DashboardCounts{ByStatus: map[string]int{}, ByItemType: map[string]int{}}
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM entries WHERE `+cond, args...).Scan(&out.Total); err != nil {
		return DashboardCounts{}, fmt.Errorf("store: dashboard total: %w", err)
	}
	groupInto := func(query string, m map[string]int) error {
		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			m[k] = n
		}
		return rows.Err()
	}
	if err := groupInto(
		`SELECT status, count(*) FROM entries WHERE `+cond+` GROUP BY status`,
		out.ByStatus); err != nil {
		return DashboardCounts{}, fmt.Errorf("store: dashboard status: %w", err)
	}
	if err := groupInto(
		`SELECT item_type, count(*) FROM entries WHERE `+cond+` GROUP BY item_type`,
		out.ByItemType); err != nil {
		return DashboardCounts{}, fmt.Errorf("store: dashboard item type: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT coalesce(platform_name, ''), count(*) FROM entries
		WHERE `+cond+` GROUP BY 1 ORDER BY count(*) DESC, 1`, args...)
	if err != nil {
		return DashboardCounts{}, fmt.Errorf("store: dashboard platforms: %w", err)
	}
	defer rows.Close()
	out.ByPlatform = []PlatformCount{}
	for rows.Next() {
		var p PlatformCount
		if err := rows.Scan(&p.Name, &p.Count); err != nil {
			return DashboardCounts{}, fmt.Errorf("store: scan platform count: %w", err)
		}
		out.ByPlatform = append(out.ByPlatform, p)
	}
	if err := rows.Err(); err != nil {
		return DashboardCounts{}, fmt.Errorf("store: dashboard platforms: %w", err)
	}
	srows, err := s.pool.Query(ctx, `
		SELECT currency, sum(price_paid_cents) FROM entries
		WHERE `+cond+` AND price_paid_cents IS NOT NULL
		GROUP BY currency ORDER BY currency`, args...)
	if err != nil {
		return DashboardCounts{}, fmt.Errorf("store: dashboard spend: %w", err)
	}
	defer srows.Close()
	out.Spend = []CurrencySpend{}
	for srows.Next() {
		var c CurrencySpend
		if err := srows.Scan(&c.Currency, &c.TotalCents); err != nil {
			return DashboardCounts{}, fmt.Errorf("store: scan spend: %w", err)
		}
		out.Spend = append(out.Spend, c)
	}
	return out, srows.Err()
}

// PricingRow is the slim projection the dashboard's value composition
// needs for every entry (including disabled ones, which it counts as
// excluded; ProductID is nil for custom entries).
type PricingRow struct {
	EntryID          uuid.UUID
	Packaging        string
	PricingMode      string
	ProductID        *uuid.UUID
	PricingProductID *uuid.UUID
	// Present together whenever set (DB CHECK); the value IS the
	// entry's worth under pricing_mode custom.
	CustomValueCents *int64
	CustomValueSetAt *time.Time
}

// PricingRows lists the pricing coordinates of every entry matching
// the filter matrix (zero-value Filters = everything).
func (s *Store) PricingRows(ctx context.Context, userID uuid.UUID, f Filters) ([]PricingRow, error) {
	where, args := filterWhere(userID, f)
	rows, err := s.pool.Query(ctx, `
		SELECT id, packaging, pricing_mode, product_id, pricing_product_id,
		       custom_value_cents, custom_value_set_at
		FROM entries WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return nil, fmt.Errorf("store: pricing rows: %w", err)
	}
	defer rows.Close()
	out := []PricingRow{}
	for rows.Next() {
		var r PricingRow
		if err := rows.Scan(&r.EntryID, &r.Packaging, &r.PricingMode, &r.ProductID,
			&r.PricingProductID, &r.CustomValueCents, &r.CustomValueSetAt); err != nil {
			return nil, fmt.Errorf("store: scan pricing row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PurgeUserData erases everything the user owns in one transaction:
// entries (entry_tags cascade), tags, and saved views. Account
// deletion calls this; purging an empty collection is a no-op.
func (s *Store) PurgeUserData(ctx context.Context, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, q := range []string{
			`DELETE FROM entries WHERE user_id = $1`,
			`DELETE FROM tags WHERE user_id = $1`,
			`DELETE FROM saved_views WHERE user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, userID); err != nil {
				return fmt.Errorf("store: purge user data: %w", err)
			}
		}
		return nil
	})
}
