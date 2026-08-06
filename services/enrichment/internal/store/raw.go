package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
)

// RawFieldsVersion stamps which gameFields generation fetched a raw
// payload. Raws below the current version predate fields the
// projection needs and must be refetched, not reprojected; bump it
// whenever gameFields widens.
const RawFieldsVersion = 2

// RawGame is the igdb_raw document: the full provider payload keyed by
// IGDB game id, shared across products (one IGDB game fans out to N
// PriceCharting-grained products) and holding recommendation
// candidates that are not products yet.
type RawGame struct {
	GameID        int64     `bson:"_id"`
	Game          igdb.Game `bson:"game"`
	FetchedAt     time.Time `bson:"fetched_at"`
	FieldsVersion int       `bson:"fields_version"`
}

// UpsertRaw stores payloads by game id, replacing stale copies.
func (s *Store) UpsertRaw(ctx context.Context, games []igdb.Game, fetchedAt time.Time) error {
	if len(games) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(games))
	for _, g := range games {
		// A nil table would persist as bson null and read back as
		// pre-feature "never fetched"; the empty array is the honest
		// fetched-but-none marker the heal paths key on.
		if g.ReleaseDates == nil {
			g.ReleaseDates = []igdb.ReleaseDate{}
		}
		doc := RawGame{GameID: g.ID, Game: g, FetchedAt: fetchedAt.UTC().Truncate(time.Millisecond), FieldsVersion: RawFieldsVersion}
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.D{{Key: "_id", Value: g.ID}}).
			SetReplacement(doc).
			SetUpsert(true))
	}
	if _, err := s.db.Collection(colRaw).BulkWrite(ctx, models); err != nil {
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
	cur, err := s.db.Collection(colRaw).Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}})
	if err != nil {
		return nil, fmt.Errorf("store: raw by ids: %w", err)
	}
	var out []RawGame
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: raw by ids: %w", err)
	}
	return out, nil
}

// UpsertPlatforms replaces the IGDB platform catalog (fetched
// wholesale; consoles borrow names from it where matchable).
// `logo_url` is persisted as a precomputed display string; the raw
// image id is not kept. fetched_at rides on each doc for the
// staleness check.
func (s *Store) UpsertPlatforms(ctx context.Context, ps []igdb.Platform, fetchedAt time.Time) error {
	if len(ps) == 0 {
		return nil
	}
	at := fetchedAt.UTC().Truncate(time.Millisecond)
	models := make([]mongo.WriteModel, 0, len(ps))
	for _, p := range ps {
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.D{{Key: "_id", Value: p.ID}}).
			SetReplacement(bson.D{
				{Key: "_id", Value: p.ID},
				{Key: "name", Value: p.Name},
				{Key: "abbreviation", Value: p.Abbreviation},
				{Key: "generation", Value: p.Generation},
				{Key: "logo_url", Value: p.LogoURL()},
				{Key: "fetched_at", Value: at},
			}).
			SetUpsert(true))
	}
	if _, err := s.db.Collection(colPlatforms).BulkWrite(ctx, models); err != nil {
		return fmt.Errorf("store: upsert platforms: %w", err)
	}
	return nil
}

// ListPlatforms returns the cached platform catalog (empty = never
// fetched), logo_url included as stored.
func (s *Store) ListPlatforms(ctx context.Context) ([]CatalogPlatform, error) {
	cur, err := s.db.Collection(colPlatforms).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("store: list platforms: %w", err)
	}
	var out []CatalogPlatform
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: list platforms: %w", err)
	}
	return out, nil
}

// PlatformsFetchedAt returns the oldest fetch stamp across the
// catalog (ascending sort, first doc), which is the conservative
// staleness signal (a partially-refreshed catalog reads as stale).
// Zero time = never fetched.
func (s *Store) PlatformsFetchedAt(ctx context.Context) (time.Time, error) {
	var doc struct {
		FetchedAt time.Time `bson:"fetched_at"`
	}
	err := s.db.Collection(colPlatforms).FindOne(ctx, bson.D{},
		options.FindOne().SetSort(bson.D{{Key: "fetched_at", Value: 1}})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("store: platforms fetched at: %w", err)
	}
	return doc.FetchedAt, nil
}
