// Package store owns the enrichment service's MongoDB documents and
// queries. No other package writes queries against these collections.
// It persists provider payload types (igdb.Game) directly: quarantining
// third-party data in document shape is this service's purpose.
package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
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

// ErrIdentityTaken is the sentinel for a mapping write the unique
// identity indexes refuse: another product already carries the
// resulting identity (a taken listing, or a clear that would mint a
// second unmatched member for one family).
var ErrIdentityTaken = errors.New("store: identity taken")

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
	IGDBID  int64  `bson:"igdb_id"`
	Name    string `bson:"name"`
	LogoURL string `bson:"logo_url,omitempty"`
}

// CatalogPlatform is one cached platform-catalog row. LogoURL is the
// ready display string precomputed at upsert (the raw IGDB image id
// is not kept).
type CatalogPlatform struct {
	ID           int64  `bson:"_id"`
	Name         string `bson:"name"`
	Abbreviation string `bson:"abbreviation"`
	Generation   int    `bson:"generation"`
	LogoURL      string `bson:"logo_url"`
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

// MetaReleaseDate is one per-region date for the product's own
// platform (canonical IGDB region name, day-truncated date).
type MetaReleaseDate struct {
	Region string    `bson:"region"`
	Date   time.Time `bson:"date"`
}

// IGDBMeta is the game-metadata projection embedded in a product; it
// refreshes on its own cadence via SetIGDB (partial update).
type IGDBMeta struct {
	GameID           int64             `bson:"game_id"`
	Name             string            `bson:"name"`
	CoverURL         string            `bson:"cover_url,omitempty"`
	Genres           []Genre           `bson:"genres"`
	Themes           []string          `bson:"themes"`
	Franchises       []string          `bson:"franchises"`
	SimilarGames     []int64           `bson:"similar_games"`
	Companies        []Company         `bson:"companies"`
	FirstReleaseDate time.Time         `bson:"first_release_date,omitempty"`
	ReleaseDates     []MetaReleaseDate `bson:"release_dates"`
	FetchedAt        time.Time         `bson:"fetched_at"`
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
// selection. Game identity is listing-keyed (igdb game, platform,
// PriceCharting listing; a missing mapping is the family's single
// unmatched member). Region/edition/variant are always present
// (empty string = unspecified): they are part of hardware identity
// and vestigial on games (kept for document-shape stability).
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
	// MatchHold marks a deliberate admin mapping clear: the nightly
	// re-match walk skips held products. Any mapping set lifts it.
	MatchHold bool      `bson:"match_hold,omitempty"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// ProductKey is the identity a resolve request maps to: games key on
// (igdb_game_id, platform, pc listing - 0 means the unmatched
// member); console/accessory key on pc_product_id with
// region/edition/variant; pc_listing keys on pc_product_id alone.
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
	// A pc_listing IS the exact variant: identity is the listing id
	// alone (region/edition/variant are stored empty).
	if k.Type == "pc_listing" {
		return bson.D{
			{Key: "type", Value: "pc_listing"},
			{Key: "pricecharting.pc_product_id", Value: k.PCProductID},
		}
	}
	if k.Type == "game" {
		// The listing is the member differentiator; a zero PCProductID
		// addresses the family's unmatched member (bson null matches a
		// missing subdocument, which is also how the unique index sees
		// it). Region/edition/variant are entry-level facts, not game
		// identity.
		pcID := any(k.PCProductID)
		if k.PCProductID == 0 {
			pcID = nil
		}
		return bson.D{
			{Key: "type", Value: "game"},
			{Key: "igdb.game_id", Value: k.IGDBGameID},
			{Key: "platform.igdb_id", Value: k.PlatformIGDBID},
			{Key: "pricecharting.pc_product_id", Value: pcID},
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

// NewIGDBMeta projects a raw IGDB payload onto the product
// subdocument, scoping the release table to the product's platform:
// the scalar becomes the platform's earliest date (game-level when the
// platform has no dated rows).
func NewIGDBMeta(g igdb.Game, platformIGDBID int64, fetchedAt time.Time) IGDBMeta {
	m := IGDBMeta{
		GameID:           g.ID,
		Name:             g.Name,
		CoverURL:         g.CoverURL(),
		Genres:           make([]Genre, 0, len(g.Genres)),
		Themes:           make([]string, 0, len(g.Themes)),
		Franchises:       make([]string, 0, len(g.Franchises)),
		SimilarGames:     append([]int64(nil), g.SimilarGames...),
		Companies:        make([]Company, 0, len(g.InvolvedCompanies)),
		FirstReleaseDate: g.ReleaseDate(),
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
	m.ReleaseDates = platformReleaseDates(g, platformIGDBID)
	if len(m.ReleaseDates) > 0 {
		m.FirstReleaseDate = m.ReleaseDates[0].Date // sorted ascending: [0] is the earliest
	}
	return m
}

// SameProjection reports whether two projections are identical ignoring
// FetchedAt: the reprojection walk's diff gate, so a rebuild that only
// re-stamps the fetch time is not a write. A nil ReleaseDates compares
// UNEQUAL to an empty one - that exact difference is a pre-feature
// projection (one that never carried a release table) which must be
// rebuilt, not skipped.
func (m IGDBMeta) SameProjection(o IGDBMeta) bool {
	m.FetchedAt, o.FetchedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(m, o)
}

// platformReleaseDates keeps one platform's concrete-dated rows, the
// earliest per region, sorted by date then region so the stored
// document shape is deterministic. It also folds in the platform's JP
// regional twin: IGDB models Super Nintendo (19) and Super Famicom (58)
// as separate platforms (likewise NES/Family Computer), so a SNES
// product's japan date rides the Super Famicom platform id and would
// otherwise never appear here.
func platformReleaseDates(g igdb.Game, platformIGDBID int64) []MetaReleaseDate {
	twin := igdb.TwinPlatformID(platformIGDBID)
	earliest := map[string]time.Time{}
	for _, rd := range g.ReleaseDates {
		// A platform-0 row matches no real platform; skipping it also
		// defends the pid=0 (no-platform) product, where a bare equality
		// check would otherwise fold every dateless-platform row in.
		if rd.Platform == 0 || rd.Date == 0 {
			continue
		}
		// The product's own platform, or its JP twin folded in.
		matchesPlatform := rd.Platform == platformIGDBID || (twin != 0 && rd.Platform == twin)
		if !matchesPlatform {
			continue
		}
		name, ok := igdb.RegionName(rd.Region)
		if !ok {
			continue
		}
		d := time.Unix(rd.Date, 0).UTC().Truncate(24 * time.Hour)
		if cur, seen := earliest[name]; !seen || d.Before(cur) {
			earliest[name] = d
		}
	}
	out := make([]MetaReleaseDate, 0, len(earliest))
	for name, d := range earliest {
		out = append(out, MetaReleaseDate{Region: name, Date: d})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.Before(out[j].Date)
		}
		return out[i].Region < out[j].Region
	})
	return out
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
// product becomes unmatched). A mapping change is an identity move
// for games, so a write the unique index refuses surfaces
// ErrIdentityTaken. Clearing sets match_hold - the nightly re-match
// walk must not undo a deliberate clear - and any set lifts it.
func (s *Store) SetPriceCharting(ctx context.Context, id string, m *PCMeta) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "pricecharting", Value: m},
			{Key: "updated_at", Value: now},
		}},
		{Key: "$unset", Value: bson.D{{Key: "match_hold", Value: ""}}},
	}
	if m == nil {
		update = bson.D{
			{Key: "$unset", Value: bson.D{{Key: "pricecharting", Value: ""}}},
			{Key: "$set", Value: bson.D{
				{Key: "match_hold", Value: true},
				{Key: "updated_at", Value: now},
			}},
		}
	}
	res, err := s.db.Collection(colProducts).UpdateByID(ctx, id, update)
	if mongo.IsDuplicateKeyError(err) {
		return ErrIdentityTaken
	}
	if err != nil {
		return fmt.Errorf("store: set pricecharting: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPriceChartingIfMissing sets the mapping only while the product
// is unmatched (the bson nil filter matches a missing or null
// subdocument) AND not admin-held, and reports whether this call
// landed it. This is the re-match walk's write: a deliberate clear
// landing between the walk's worklist read and this write sets
// match_hold, so the filter excludes it and the clear is never
// overwritten. Racing fillers still converge on one winner (the
// caller gates side effects on the bool), and a taken (game,
// platform, listing) slot still surfaces ErrIdentityTaken so the walk
// skips - never merges.
func (s *Store) SetPriceChartingIfMissing(ctx context.Context, id string, m *PCMeta) (bool, error) {
	res, err := s.db.Collection(colProducts).UpdateOne(ctx,
		bson.D{
			{Key: "_id", Value: id},
			{Key: "pricecharting", Value: nil},
			{Key: "match_hold", Value: bson.D{{Key: "$ne", Value: true}}},
		},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "pricecharting", Value: m},
				{Key: "updated_at", Value: time.Now().UTC().Truncate(time.Millisecond)},
			}},
		},
	)
	if mongo.IsDuplicateKeyError(err) {
		return false, ErrIdentityTaken
	}
	if err != nil {
		return false, fmt.Errorf("store: set pricecharting if missing: %w", err)
	}
	return res.MatchedCount == 1, nil
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

// ListUnmatchedGames returns game products with no mapping and no
// match_hold, oldest updated_at first, capped at limit (the re-match
// walk's rotating worklist).
func (s *Store) ListUnmatchedGames(ctx context.Context, limit int) ([]Product, error) {
	filter := bson.D{
		{Key: "type", Value: "game"},
		{Key: "pricecharting", Value: nil},
		{Key: "match_hold", Value: bson.D{{Key: "$ne", Value: true}}},
	}
	cur, err := s.db.Collection(colProducts).Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "updated_at", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("store: list unmatched games: %w", err)
	}
	var out []Product
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: list unmatched games: %w", err)
	}
	return out, nil
}

// ListIGDBProducts returns every product carrying an IGDB projection,
// in stable _id order: the reprojection walk's worklist. Uncapped, like
// ListPriced - a full nightly sweep rather than a capped window, because
// the walk is read-cheap (Mongo reads plus a DeepEqual diff gate; a
// provider call fires only for the missing/nil-table raw set, which
// drains to zero) so sweeping the whole catalog is the honest shape.
// Unlike the retired missing-release-dates query it does not filter on
// the release table - the walk rebuilds each projection from the raw
// payload and writes only when it actually changed, so already-healed
// products must still be revisited (any future projection-logic change
// then self-deploys on the very next walk, instead of waiting for a
// capped window to drain past them).
func (s *Store) ListIGDBProducts(ctx context.Context) ([]Product, error) {
	cur, err := s.db.Collection(colProducts).Find(ctx,
		bson.D{{Key: "igdb", Value: bson.D{{Key: "$exists", Value: true}}}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("store: list igdb products: %w", err)
	}
	var out []Product
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: list igdb products: %w", err)
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
