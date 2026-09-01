package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
)

// RawFieldsVersion stamps which gameFields generation fetched a raw
// payload; bump it whenever gameFields widens (older raws refetch, not reproject).
const RawFieldsVersion = 2

// RawGame is the igdb_raw row: the full provider payload keyed by
// IGDB game id, shared across products and unmade recommendation candidates.
type RawGame struct {
	GameID        int64
	Game          igdb.Game
	FetchedAt     time.Time
	FieldsVersion int
}

// UpsertRaw stores payloads by game id, replacing stale copies.
func (s *Store) UpsertRaw(ctx context.Context, games []igdb.Game, fetchedAt time.Time) error {
	if len(games) == 0 {
		return nil
	}
	at := fetchedAt.UTC().Truncate(time.Millisecond)
	b := &pgx.Batch{}
	for _, g := range games {
		// A nil table would read back as pre-feature; the empty array is
		// the honest fetched-but-none marker heal paths key on.
		if g.ReleaseDates == nil {
			g.ReleaseDates = []igdb.ReleaseDate{}
		}
		payload, err := json.Marshal(g)
		if err != nil {
			return fmt.Errorf("store: upsert raw: %w", err)
		}
		b.Queue(`INSERT INTO igdb_raw (id, game, fetched_at, fields_version) VALUES ($1,$2,$3,$4)
			ON CONFLICT (id) DO UPDATE SET game = EXCLUDED.game,
			fetched_at = EXCLUDED.fetched_at, fields_version = EXCLUDED.fields_version`,
			g.ID, payload, at, RawFieldsVersion)
	}
	if err := s.pool.SendBatch(ctx, b).Close(); err != nil {
		return fmt.Errorf("store: upsert raw: %w", err)
	}
	return nil
}

// RawByIDs returns the payloads it has; missing ids are silently
// absent (callers diff against the request to find fetch work).
func (s *Store) RawByIDs(ctx context.Context, ids []int64) ([]RawGame, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return queryAll(ctx, s.pool, "SELECT id, game, fetched_at, fields_version FROM igdb_raw WHERE id = ANY($1)",
		[]any{ids}, func(rows pgx.Rows) (RawGame, error) {
			var r RawGame
			err := rows.Scan(&r.GameID, &r.Game, &r.FetchedAt, &r.FieldsVersion)
			r.FetchedAt = r.FetchedAt.UTC()
			return r, err
		}, "raw by ids")
}

// UpsertPlatforms replaces the IGDB platform catalog (fetched
// wholesale). logo_url is a precomputed display string, not the raw
// image id; fetched_at rides on each row for the staleness check.
func (s *Store) UpsertPlatforms(ctx context.Context, ps []igdb.Platform, fetchedAt time.Time) error {
	if len(ps) == 0 {
		return nil
	}
	at := fetchedAt.UTC().Truncate(time.Millisecond)
	b := &pgx.Batch{}
	for _, p := range ps {
		b.Queue(`INSERT INTO platforms (igdb_id, name, abbreviation, generation, logo_url, fetched_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (igdb_id) DO UPDATE SET name = EXCLUDED.name, abbreviation = EXCLUDED.abbreviation,
			generation = EXCLUDED.generation, logo_url = EXCLUDED.logo_url, fetched_at = EXCLUDED.fetched_at`,
			p.ID, p.Name, p.Abbreviation, p.Generation, p.LogoURL(), at)
	}
	if err := s.pool.SendBatch(ctx, b).Close(); err != nil {
		return fmt.Errorf("store: upsert platforms: %w", err)
	}
	return nil
}

// ListPlatforms returns the cached platform catalog (empty = never
// fetched), logo_url included as stored.
func (s *Store) ListPlatforms(ctx context.Context) ([]CatalogPlatform, error) {
	return queryAll(ctx, s.pool, "SELECT igdb_id, name, abbreviation, generation, logo_url FROM platforms ORDER BY igdb_id",
		nil, func(rows pgx.Rows) (CatalogPlatform, error) {
			var p CatalogPlatform
			err := rows.Scan(&p.ID, &p.Name, &p.Abbreviation, &p.Generation, &p.LogoURL)
			return p, err
		}, "list platforms")
}

// PlatformsFetchedAt returns the oldest fetch stamp across the
// catalog (conservative: a partially-refreshed catalog reads as
// stale). Zero time = never fetched.
func (s *Store) PlatformsFetchedAt(ctx context.Context) (time.Time, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, "SELECT min(fetched_at) FROM platforms").Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("store: platforms fetched at: %w", err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return t.UTC(), nil
}
