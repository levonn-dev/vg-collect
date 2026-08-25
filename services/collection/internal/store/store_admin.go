// Admin and maintenance levers: catalog resnapshot and rematch
// refs, platform and region normalization, and user-data purge.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

// GameEntryRef is the resnapshot walk's row: enough to recompute one
// game-backed entry's date pick, localized trio, and credit arrays.
type GameEntryRef struct {
	EntryID          uuid.UUID
	ProductID        uuid.UUID
	Region           string
	FirstReleaseDate *time.Time

	// The entry's currently stored snapshot fields, read back so the walk can
	// diff a freshly recomputed pick against them and skip an unchanged row.
	LocalizedName         *string
	LocalizedNameTranslit *string
	LocalizedCoverURL     *string
	Developers            []string
	Publishers            []string
}

// CountEntriesByProduct counts entries referencing the product across ALL
// users, the admin delete's safety read; deliberately unscoped like ListGameBackedRefs.
func (s *Store) CountEntriesByProduct(ctx context.Context, productID uuid.UUID) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM entries WHERE product_id = $1`, productID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count entries by product: %w", err)
	}
	return n, nil
}

// ListGameBackedRefs lists every user's game-backed entries (product and
// igdb game both present) with their current snapshot trio, for the
// resnapshot walk to diff against; deliberately unscoped since the pick
// derives from product + region, nothing user-private.
func (s *Store) ListGameBackedRefs(ctx context.Context) ([]GameEntryRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, product_id, region, first_release_date,
			localized_name, localized_name_translit, localized_cover_url,
			developers, publishers
		FROM entries
		WHERE product_id IS NOT NULL AND igdb_game_id IS NOT NULL
		ORDER BY product_id, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list game-backed refs: %w", err)
	}
	out, err := pgkit.ScanAll(rows, nil, func(r pgx.Rows) (GameEntryRef, error) {
		var ref GameEntryRef
		if err := r.Scan(&ref.EntryID, &ref.ProductID, &ref.Region, &ref.FirstReleaseDate,
			&ref.LocalizedName, &ref.LocalizedNameTranslit, &ref.LocalizedCoverURL,
			&ref.Developers, &ref.Publishers); err != nil {
			return GameEntryRef{}, fmt.Errorf("store: list game-backed refs: %w", err)
		}
		return ref, nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: list game-backed refs: %w", err)
	}
	return out, nil
}

// SetSnapshotFields narrowly rewrites one entry's product-derived snapshot
// fields (the resnapshot walk's only write): date, localized trio, credits.
func (s *Store) SetSnapshotFields(ctx context.Context, entryID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE entries SET first_release_date = $2, localized_name = $3,
			localized_name_translit = $4, localized_cover_url = $5,
			developers = $6, publishers = $7, updated_at = now()
		WHERE id = $1`,
		entryID, d, name, translit, cover, developers, publishers)
	if err != nil {
		return fmt.Errorf("store: set snapshot fields: %w", err)
	}
	return nil
}

// RematchEntryRef is the rematch's row: an auto-priced game-backed entry with
// the identity fields a region-aware re-resolve needs, plus its current
// snapshot fields (unused today; the handler overwrites unconditionally).
type RematchEntryRef struct {
	EntryID          uuid.UUID
	ProductID        uuid.UUID
	IGDBGameID       int64
	PlatformIGDBID   int64
	Region           string
	FirstReleaseDate *time.Time

	LocalizedName         *string
	LocalizedNameTranslit *string
	LocalizedCoverURL     *string
}

// ListAutoGameRematchRefs lists every user's auto-priced game-backed entries
// with a platform id, deliberately unscoped. Ordered for deterministic
// output, not because a caller depends on the resulting grouping (the handler regroups via a map).
func (s *Store) ListAutoGameRematchRefs(ctx context.Context) ([]RematchEntryRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, product_id, igdb_game_id, platform_igdb_id, region, first_release_date,
			localized_name, localized_name_translit, localized_cover_url
		FROM entries
		WHERE product_id IS NOT NULL AND igdb_game_id IS NOT NULL
			AND platform_igdb_id IS NOT NULL AND pricing_mode = 'auto'
			AND match_provenance = 'auto'
		ORDER BY igdb_game_id, platform_igdb_id, region, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list rematch refs: %w", err)
	}
	out, err := pgkit.ScanAll(rows, nil, func(r pgx.Rows) (RematchEntryRef, error) {
		var ref RematchEntryRef
		if err := r.Scan(&ref.EntryID, &ref.ProductID, &ref.IGDBGameID, &ref.PlatformIGDBID, &ref.Region,
			&ref.FirstReleaseDate, &ref.LocalizedName, &ref.LocalizedNameTranslit, &ref.LocalizedCoverURL); err != nil {
			return RematchEntryRef{}, fmt.Errorf("store: list rematch refs: %w", err)
		}
		return ref, nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: list rematch refs: %w", err)
	}
	return out, nil
}

// RepointEntry moves one entry to a sibling member and rewrites its
// product-derived snapshot in the same statement (the rematch's only write);
// always a product change, so the region-mismatch ack unconditionally clears.
func (s *Store) RepointEntry(ctx context.Context, entryID, productID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE entries SET product_id = $2, first_release_date = $3, localized_name = $4,
			localized_name_translit = $5, localized_cover_url = $6,
			developers = $7, publishers = $8, updated_at = now(),
			region_mismatch_ack_at = NULL
		WHERE id = $1`,
		entryID, productID, d, name, translit, cover, developers, publishers)
	if err != nil {
		return fmt.Errorf("store: repoint entry: %w", err)
	}
	return nil
}

// PlatformEntryRef is the normalize lever's row: a custom entry with a
// free-text platform and no canonical id yet.
type PlatformEntryRef struct {
	EntryID      uuid.UUID
	PlatformName string
}

// ListNameOnlyPlatformEntries lists every entry with a platform_name but no
// platform_igdb_id, across all users; stamped rows leave the set (re-runnable).
func (s *Store) ListNameOnlyPlatformEntries(ctx context.Context) ([]PlatformEntryRef, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, platform_name FROM entries WHERE platform_name IS NOT NULL AND platform_igdb_id IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: list name-only platforms: %w", err)
	}
	out, err := pgkit.ScanAll(rows, nil, func(r pgx.Rows) (PlatformEntryRef, error) {
		var ref PlatformEntryRef
		if err := r.Scan(&ref.EntryID, &ref.PlatformName); err != nil {
			return PlatformEntryRef{}, fmt.Errorf("store: scan platform ref: %w", err)
		}
		return ref, nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: list name-only platforms: %w", err)
	}
	return out, nil
}

// SetEntryPlatformIdentity stamps one entry's canonical platform id and name
// (the lever's only write); unscoped by user_id, matching the unscoped selection.
func (s *Store) SetEntryPlatformIdentity(ctx context.Context, entryID uuid.UUID, igdbID int64, name string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE entries SET platform_igdb_id = $2, platform_name = $3, updated_at = now() WHERE id = $1`,
		entryID, igdbID, name)
	if err != nil {
		return fmt.Errorf("store: set entry platform identity: %w", err)
	}
	return nil
}

// OpenRegionEntryRef is one row of the normalize-regions selection: entries
// outside the known set, with enough identity to pick the plain-write vs
// snapshot-re-pick arm.
type OpenRegionEntryRef struct {
	EntryID    uuid.UUID
	ProductID  *uuid.UUID
	IGDBGameID *int64
	Region     string
}

// ListOpenRegionEntries lists entries holding a region outside the known set;
// deliberately unscoped across users like its platform sibling.
func (s *Store) ListOpenRegionEntries(ctx context.Context, known []string) ([]OpenRegionEntryRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, product_id, igdb_game_id, region FROM entries
		WHERE NOT (region = ANY($1))
		ORDER BY id`, known)
	if err != nil {
		return nil, fmt.Errorf("store: list open region entries: %w", err)
	}
	out, err := pgkit.ScanAll(rows, nil, func(r pgx.Rows) (OpenRegionEntryRef, error) {
		var ref OpenRegionEntryRef
		if err := r.Scan(&ref.EntryID, &ref.ProductID, &ref.IGDBGameID, &ref.Region); err != nil {
			return OpenRegionEntryRef{}, fmt.Errorf("store: scan open region ref: %w", err)
		}
		return ref, nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: list open region entries: %w", err)
	}
	return out, nil
}

// PromoteEntryRegion canonicalizes one entry's region string; a region change
// is a fresh choice, so the mismatch ack clears.
func (s *Store) PromoteEntryRegion(ctx context.Context, entryID uuid.UUID, region string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE entries SET region = $2, updated_at = now(), region_mismatch_ack_at = NULL
		WHERE id = $1`, entryID, region)
	if err != nil {
		return fmt.Errorf("store: promote entry region: %w", err)
	}
	return nil
}

// PromoteEntryRegionSnapshot canonicalizes the region and re-picks the
// product-derived snapshot in one statement, since promotion may unlock a localization chain.
func (s *Store) PromoteEntryRegionSnapshot(ctx context.Context, entryID uuid.UUID, region string, d *time.Time, name, translit, cover *string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE entries SET region = $2, first_release_date = $3, localized_name = $4,
			localized_name_translit = $5, localized_cover_url = $6,
			updated_at = now(), region_mismatch_ack_at = NULL
		WHERE id = $1`, entryID, region, d, name, translit, cover)
	if err != nil {
		return fmt.Errorf("store: promote entry region snapshot: %w", err)
	}
	return nil
}

// PurgeUserData erases everything the user owns in one transaction: entries
// (entry_tags cascade), tags, and saved views; purging an empty collection is a no-op.
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
