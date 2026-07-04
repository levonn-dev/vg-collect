// Package store owns the enrichment service's MongoDB documents and
// queries. No other package writes queries against these collections.
// It persists provider payload types (igdb.Game) directly: quarantining
// third-party data in document shape is this service's purpose.
package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
)

// ErrNotFound is the sentinel for a missing document; handlers branch
// on it via errors.Is.
var ErrNotFound = errors.New("store: not found")

const (
	colProducts  = "products"
	colRaw       = "igdb_raw"
	colPlatforms = "platforms"
	colSnapshots = "price_snapshots"
)

// Store is the query surface over the enrichment database.
type Store struct{ db *mongo.Database }

// New builds a Store over the migrated database handle.
func New(db *mongo.Database) *Store { return &Store{db: db} }

// Platform is the product's platform reference (a projection of the
// IGDB platform, denormalized for display and identity).
type Platform struct {
	IGDBID int64  `bson:"igdb_id"`
	Name   string `bson:"name"`
}

// Genre keeps both the IGDB id (recommendation queries) and the
// display name.
type Genre struct {
	ID   int64  `bson:"id"`
	Name string `bson:"name"`
}

// Company is a credited company with its role flags.
type Company struct {
	Name      string `bson:"name"`
	Developer bool   `bson:"developer"`
	Publisher bool   `bson:"publisher"`
}

// IGDBMeta is the game-metadata projection embedded in a product; it
// refreshes on its own cadence via SetIGDB (partial update).
type IGDBMeta struct {
	GameID           int64     `bson:"game_id"`
	Name             string    `bson:"name"`
	CoverURL         string    `bson:"cover_url,omitempty"`
	Genres           []Genre   `bson:"genres"`
	Themes           []string  `bson:"themes"`
	Franchises       []string  `bson:"franchises"`
	SimilarGames     []int64   `bson:"similar_games"`
	Companies        []Company `bson:"companies"`
	FirstReleaseYear int       `bson:"first_release_year,omitempty"`
	FetchedAt        time.Time `bson:"fetched_at"`
}

// PriceQuote holds one condition triple in integer cents; nil means
// the provider lists no price for that condition.
type PriceQuote struct {
	LooseCents *int64 `bson:"loose_cents,omitempty"`
	CIBCents   *int64 `bson:"cib_cents,omitempty"`
	NewCents   *int64 `bson:"new_cents,omitempty"`
}

// PCMeta is the PriceCharting mapping + current prices, refreshed on
// the daily cadence via SetCurrentPrices (partial update). A product
// without a PCMeta is unmatched (below-threshold, never guessed).
type PCMeta struct {
	PCProductID     int64      `bson:"pc_product_id"`
	PCName          string     `bson:"pc_name"`
	ConsoleName     string     `bson:"console_name"`
	MatchConfidence float64    `bson:"match_confidence"`
	Verified        bool       `bson:"verified"`
	Current         PriceQuote `bson:"current"`
	AsOf            time.Time  `bson:"as_of"`
}

// Product is the canonical catalog document, lazily created on user
// selection. Region/edition/variant are always present (empty string =
// unspecified) so the unique identity indexes compare consistently.
type Product struct {
	ID            string    `bson:"_id"`
	Type          string    `bson:"type"`
	Name          string    `bson:"name"`
	Platform      *Platform `bson:"platform,omitempty"`
	Region        string    `bson:"region"`
	Edition       string    `bson:"edition"`
	Variant       string    `bson:"variant"`
	IGDB          *IGDBMeta `bson:"igdb,omitempty"`
	PriceCharting *PCMeta   `bson:"pricecharting,omitempty"`
	CreatedAt     time.Time `bson:"created_at"`
	UpdatedAt     time.Time `bson:"updated_at"`
}

// ProductKey is the identity a resolve request maps to: games key on
// (igdb_game_id, platform); console/accessory key on pc_product_id;
// region/edition/variant always participate.
type ProductKey struct {
	Type           string
	IGDBGameID     int64
	PlatformIGDBID int64
	PCProductID    int64
	Region         string
	Edition        string
	Variant        string
}

func (k ProductKey) filter() bson.D {
	if k.Type == "game" {
		return bson.D{
			{Key: "type", Value: "game"},
			{Key: "igdb.game_id", Value: k.IGDBGameID},
			{Key: "platform.igdb_id", Value: k.PlatformIGDBID},
			{Key: "region", Value: k.Region},
			{Key: "edition", Value: k.Edition},
			{Key: "variant", Value: k.Variant},
		}
	}
	return bson.D{
		{Key: "type", Value: k.Type},
		{Key: "pricecharting.pc_product_id", Value: k.PCProductID},
		{Key: "region", Value: k.Region},
		{Key: "edition", Value: k.Edition},
		{Key: "variant", Value: k.Variant},
	}
}

func keyOf(p Product) ProductKey {
	k := ProductKey{Type: p.Type, Region: p.Region, Edition: p.Edition, Variant: p.Variant}
	if p.IGDB != nil {
		k.IGDBGameID = p.IGDB.GameID
	}
	if p.Platform != nil {
		k.PlatformIGDBID = p.Platform.IGDBID
	}
	if p.PriceCharting != nil {
		k.PCProductID = p.PriceCharting.PCProductID
	}
	return k
}

// NewIGDBMeta projects a raw IGDB payload onto the product subdocument.
func NewIGDBMeta(g igdb.Game, fetchedAt time.Time) IGDBMeta {
	m := IGDBMeta{
		GameID:           g.ID,
		Name:             g.Name,
		CoverURL:         g.CoverURL(),
		Genres:           make([]Genre, 0, len(g.Genres)),
		Themes:           make([]string, 0, len(g.Themes)),
		Franchises:       make([]string, 0, len(g.Franchises)),
		SimilarGames:     append([]int64(nil), g.SimilarGames...),
		Companies:        make([]Company, 0, len(g.InvolvedCompanies)),
		FirstReleaseYear: g.ReleaseYear(),
		FetchedAt:        fetchedAt,
	}
	if m.SimilarGames == nil {
		m.SimilarGames = []int64{}
	}
	for _, gen := range g.Genres {
		m.Genres = append(m.Genres, Genre{ID: gen.ID, Name: gen.Name})
	}
	for _, th := range g.Themes {
		m.Themes = append(m.Themes, th.Name)
	}
	for _, fr := range g.Franchises {
		m.Franchises = append(m.Franchises, fr.Name)
	}
	for _, ic := range g.InvolvedCompanies {
		m.Companies = append(m.Companies, Company{Name: ic.Company.Name, Developer: ic.Developer, Publisher: ic.Publisher})
	}
	return m
}

// FindProduct returns the product with the given identity, or
// ErrNotFound.
func (s *Store) FindProduct(ctx context.Context, key ProductKey) (Product, error) {
	var p Product
	err := s.db.Collection(colProducts).FindOne(ctx, key.filter()).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, fmt.Errorf("store: find product: %w", err)
	}
	return p, nil
}

// CreateProduct inserts p (minting its id and timestamps) and returns
// it. When a concurrent resolve already created the same identity, the
// unique index rejects the insert and the winner's document is
// returned instead: find-or-create converges on one product per
// identity.
func (s *Store) CreateProduct(ctx context.Context, p Product) (Product, error) {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	p.CreatedAt, p.UpdatedAt = now, now
	_, err := s.db.Collection(colProducts).InsertOne(ctx, p)
	if mongo.IsDuplicateKeyError(err) {
		return s.FindProduct(ctx, keyOf(p))
	}
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	return p, nil
}

// GetProduct fetches by id, or ErrNotFound.
func (s *Store) GetProduct(ctx context.Context, id string) (Product, error) {
	var p Product
	err := s.db.Collection(colProducts).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, fmt.Errorf("store: get product: %w", err)
	}
	return p, nil
}

// SetIGDB replaces the product's IGDB projection (its refresh cadence
// is independent of pricing).
func (s *Store) SetIGDB(ctx context.Context, id string, m IGDBMeta) error {
	res, err := s.db.Collection(colProducts).UpdateByID(ctx, id, bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "igdb", Value: m},
			{Key: "updated_at", Value: time.Now().UTC().Truncate(time.Millisecond)},
		}},
	})
	if err != nil {
		return fmt.Errorf("store: set igdb: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPriceCharting replaces the product's mapping; nil clears it (the
// product becomes unmatched).
func (s *Store) SetPriceCharting(ctx context.Context, id string, m *PCMeta) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "pricecharting", Value: m},
			{Key: "updated_at", Value: now},
		}},
	}
	if m == nil {
		update = bson.D{
			{Key: "$unset", Value: bson.D{{Key: "pricecharting", Value: ""}}},
			{Key: "$set", Value: bson.D{{Key: "updated_at", Value: now}}},
		}
	}
	res, err := s.db.Collection(colProducts).UpdateByID(ctx, id, update)
	if err != nil {
		return fmt.Errorf("store: set pricecharting: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCurrentPrices updates the mapped product's current prices (the
// daily walk's partial update). ErrNotFound covers both a missing
// product and an unmatched one.
func (s *Store) SetCurrentPrices(ctx context.Context, id string, q PriceQuote, asOf time.Time) error {
	filter := bson.D{
		{Key: "_id", Value: id},
		{Key: "pricecharting.pc_product_id", Value: bson.D{{Key: "$exists", Value: true}}},
	}
	res, err := s.db.Collection(colProducts).UpdateOne(ctx, filter, bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "pricecharting.current", Value: q},
			{Key: "pricecharting.as_of", Value: asOf.UTC().Truncate(time.Millisecond)},
			{Key: "updated_at", Value: time.Now().UTC().Truncate(time.Millisecond)},
		}},
	})
	if err != nil {
		return fmt.Errorf("store: set current prices: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPriced returns every product with a PriceCharting mapping, in
// stable id order (the daily walk's worklist; the catalog is small by
// construction).
func (s *Store) ListPriced(ctx context.Context) ([]Product, error) {
	filter := bson.D{{Key: "pricecharting.pc_product_id", Value: bson.D{{Key: "$exists", Value: true}}}}
	cur, err := s.db.Collection(colProducts).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("store: list priced: %w", err)
	}
	var out []Product
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: list priced: %w", err)
	}
	return out, nil
}

// ProductsByIDs returns the products it finds; unknown ids are silently
// absent (batch-prices semantics).
func (s *Store) ProductsByIDs(ctx context.Context, ids []string) ([]Product, error) {
	cur, err := s.db.Collection(colProducts).Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}})
	if err != nil {
		return nil, fmt.Errorf("store: products by ids: %w", err)
	}
	var out []Product
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: products by ids: %w", err)
	}
	return out, nil
}

// SearchByName is the degraded-mode fallback: a case-insensitive
// substring match over the local catalog. A collection scan is
// accepted here (small catalog, cold-cache-and-provider-down only).
func (s *Store) SearchByName(ctx context.Context, q string, limit int) ([]Product, error) {
	filter := bson.D{{Key: "name", Value: bson.D{
		{Key: "$regex", Value: regexp.QuoteMeta(q)},
		{Key: "$options", Value: "i"},
	}}}
	cur, err := s.db.Collection(colProducts).Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("store: search by name: %w", err)
	}
	var out []Product
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: search by name: %w", err)
	}
	return out, nil
}
