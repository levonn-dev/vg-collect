// Package store owns the enrichment service's MongoDB documents and
// queries; no other package writes queries against these collections.
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

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
)

// ErrNotFound is the sentinel for a missing document; handlers branch via errors.Is.
var ErrNotFound = errors.New("store: not found")

// ErrIdentityTaken is the sentinel for a mapping write the unique
// identity indexes refuse: another product already owns the identity.
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

// findAll runs a Mongo find and drains it into a slice, wrapping the
// Find/decode errors under one op text. Zero matches yields nil, not [].
func findAll[T any](ctx context.Context, col *mongo.Collection, filter bson.D, opts *options.FindOptions, op string) ([]T, error) {
	cur, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", op, err)
	}
	var out []T
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: %s: %w", op, err)
	}
	return out, nil
}

// findPage is findAll's paginated sibling, also returning the total
// match count; countOp/findOp let each caller word the two errors differently.
func findPage[T any](ctx context.Context, col *mongo.Collection, filter bson.D, opts *options.FindOptions, countOp, findOp string) ([]T, int64, error) {
	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("store: %s: %w", countOp, err)
	}
	out, err := findAll[T](ctx, col, filter, opts, findOp)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Platform is the product's platform reference (a projection of the
// IGDB platform, denormalized for display and identity).
type Platform struct {
	IGDBID  int64  `bson:"igdb_id"`
	Name    string `bson:"name"`
	LogoURL string `bson:"logo_url,omitempty"`
}

// CatalogPlatform is one cached platform-catalog row; LogoURL is
// precomputed at upsert (the raw IGDB image id is not kept).
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

// MetaLocalization is one region's presentation: IGDB region id
// stored verbatim (ja-JP, EU, ko-KR); fields are independently optional.
type MetaLocalization struct {
	Region   string `bson:"region"`
	Name     string `bson:"name,omitempty"`
	Translit string `bson:"translit,omitempty"`
	CoverURL string `bson:"cover_url,omitempty"`
}

// IGDBMeta is the game-metadata projection embedded in a product; it
// refreshes on its own cadence via SetIGDB (partial update).
type IGDBMeta struct {
	GameID           int64              `bson:"game_id"`
	Name             string             `bson:"name"`
	CoverURL         string             `bson:"cover_url,omitempty"`
	Genres           []Genre            `bson:"genres"`
	Themes           []string           `bson:"themes"`
	Franchises       []string           `bson:"franchises"`
	SimilarGames     []int64            `bson:"similar_games"`
	Companies        []Company          `bson:"companies"`
	FirstReleaseDate time.Time          `bson:"first_release_date,omitempty"`
	ReleaseDates     []MetaReleaseDate  `bson:"release_dates"`
	Localizations    []MetaLocalization `bson:"localizations"`
	FetchedAt        time.Time          `bson:"fetched_at"`
}

// PriceQuote holds one condition triple in integer cents; nil means
// the provider lists no price for that condition.
type PriceQuote struct {
	LooseCents *int64 `bson:"loose_cents,omitempty"`
	CIBCents   *int64 `bson:"cib_cents,omitempty"`
	NewCents   *int64 `bson:"new_cents,omitempty"`
}

// PCMeta is the PriceCharting mapping + current prices, refreshed
// daily via SetCurrentPrices. A product without one is unmatched.
type PCMeta struct {
	PCProductID     int64      `bson:"pc_product_id"`
	PCName          string     `bson:"pc_name"`
	ConsoleName     string     `bson:"console_name"`
	MatchConfidence float64    `bson:"match_confidence"`
	Verified        bool       `bson:"verified"`
	Current         PriceQuote `bson:"current"`
	AsOf            time.Time  `bson:"as_of"`
}

// CommunityMeta carries community-mint facts, kept after promotion
// as gap-fill (provider fields win). Region lives here, not on
// Product: community docs carry no provider hardware identity.
type CommunityMeta struct {
	PlatformName     string    `bson:"platform_name,omitempty"`
	Region           string    `bson:"region,omitempty"`
	FirstReleaseDate time.Time `bson:"first_release_date,omitempty"`
	CoverURL         string    `bson:"cover_url,omitempty"`
	Developers       []string  `bson:"developers,omitempty"`
	Publishers       []string  `bson:"publishers,omitempty"`
}

// PromoteCandidate is one sweep hit: a provider item whose name
// plausibly matches a community product; promotion is a human verdict.
type PromoteCandidate struct {
	Provider   string    `bson:"provider"`
	ProviderID int64     `bson:"provider_id"`
	Name       string    `bson:"name"`
	Score      float64   `bson:"score"`
	FoundAt    time.Time `bson:"found_at"`
}

// CandidateRef identifies a dismissed candidate pair.
type CandidateRef struct {
	Provider   string `bson:"provider"`
	ProviderID int64  `bson:"provider_id"`
}

// Product is the canonical catalog document, lazily created on user
// selection. Identity keys on (igdb game, platform, pc listing); a
// missing mapping is the family's unmatched member. Region/edition/
// variant stay always-present (empty = unspecified) for shape stability.
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
	// MatchHold pins a deliberate admin mapping clear against automated
	// rematching; any mapping set lifts it.
	MatchHold bool `bson:"match_hold,omitempty"`
	// Origin separates provider-identified from admin-minted community
	// products; community docs sit outside the identity indexes (name
	// is their identity) and surface via community search until promoted.
	Origin    string         `bson:"origin"`
	Community *CommunityMeta `bson:"community,omitempty"`
	// PromoteCandidates is stored sorted best-first (index 0 is the
	// worklist sort key); DismissedCandidates silences pairs permanently.
	PromoteCandidates   []PromoteCandidate `bson:"promote_candidates,omitempty"`
	DismissedCandidates []CandidateRef     `bson:"dismissed_candidates,omitempty"`
	CreatedAt           time.Time          `bson:"created_at"`
	UpdatedAt           time.Time          `bson:"updated_at"`
}

// ProductKey is the identity a resolve request maps to: games key on
// (igdb_game_id, platform, pc listing; 0 = unmatched member);
// console/accessory on pc_product_id + region/edition/variant;
// pc_listing on pc_product_id alone.
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
		// Zero PCProductID addresses the unmatched member (bson null
		// matches a missing subdocument, mirroring the unique index).
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
// subdocument, scoped to the platform (game-level date if none dated).
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
	m.Localizations = []MetaLocalization{}
	for _, b := range igdb.BundleLocalizations(g) {
		m.Localizations = append(m.Localizations, MetaLocalization{
			Region: b.Region, Name: b.Name, Translit: b.Translit, CoverURL: b.CoverURL,
		})
	}
	return m
}

// SameProjection reports whether two projections are identical
// ignoring FetchedAt, so a rebuild that only re-stamps fetch time is
// not a write. nil ReleaseDates compares UNEQUAL to an empty one on
// purpose: that forces a rebuild of pre-release-table projections.
func (m IGDBMeta) SameProjection(o IGDBMeta) bool {
	m.FetchedAt, o.FetchedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(m, o)
}

// platformReleaseDates keeps the earliest dated row per region for
// one platform, sorted by date then region for a deterministic shape.
// It also folds in the JP twin platform (see igdb.TwinPlatformID),
// since Japan dates ride the twin id, not the product's own platform.
func platformReleaseDates(g igdb.Game, platformIGDBID int64) []MetaReleaseDate {
	twin := igdb.TwinPlatformID(platformIGDBID)
	earliest := map[string]time.Time{}
	for _, rd := range g.ReleaseDates {
		// Platform-0 rows match no real platform; skipping also guards
		// the pid=0 product from folding in every dateless-platform row.
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

// CreateProduct inserts p (minting id and timestamps) and returns it.
// A concurrent duplicate identity returns the winner's document
// instead (find-or-create via the unique index).
func (s *Store) CreateProduct(ctx context.Context, p Product) (Product, error) {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Origin == "" {
		p.Origin = "provider"
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

// SetPriceCharting replaces the product's mapping; nil clears it
// (product becomes unmatched). A refused unique-index write surfaces
// ErrIdentityTaken (mapping changes move game identity). Clearing sets
// match_hold so automated matching won't undo it; any set lifts it.
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

// PromoteProduct atomically attaches provider anchors, flips a
// community product to provider origin, and clears its candidates.
// A duplicate identity returns ErrIdentityTaken; nothing changes.
func (s *Store) PromoteProduct(ctx context.Context, id string, igdbMeta *IGDBMeta, platform *Platform, pc *PCMeta) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	set := bson.D{
		{Key: "origin", Value: "provider"},
		{Key: "updated_at", Value: now},
	}
	if igdbMeta != nil {
		set = append(set, bson.E{Key: "igdb", Value: igdbMeta})
	}
	if platform != nil {
		set = append(set, bson.E{Key: "platform", Value: platform})
	}
	if pc != nil {
		set = append(set, bson.E{Key: "pricecharting", Value: pc})
	}
	res, err := s.db.Collection(colProducts).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "origin", Value: "community"}},
		bson.D{
			{Key: "$set", Value: set},
			{Key: "$unset", Value: bson.D{
				{Key: "promote_candidates", Value: ""},
				{Key: "dismissed_candidates", Value: ""},
			}},
		})
	if mongo.IsDuplicateKeyError(err) {
		return ErrIdentityTaken
	}
	if err != nil {
		return fmt.Errorf("store: promote product: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCurrentPrices updates the mapped product's current prices.
// ErrNotFound covers both a missing product and an unmatched one.
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
// stable id order (catalog is small by construction; unpaginated).
func (s *Store) ListPriced(ctx context.Context) ([]Product, error) {
	filter := bson.D{{Key: "pricecharting.pc_product_id", Value: bson.D{{Key: "$exists", Value: true}}}}
	return findAll[Product](ctx, s.db.Collection(colProducts), filter,
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}), "list priced")
}

// DeleteUnmatchedProduct removes a product and its snapshots only
// while unmatched (a priced identity must be cleared first). The
// deleted bool is false for both missing and matched products; the
// caller must classify which. Entry references are the caller's problem.
func (s *Store) DeleteUnmatchedProduct(ctx context.Context, id string) (bool, error) {
	res, err := s.db.Collection(colProducts).DeleteOne(ctx, bson.D{
		{Key: "_id", Value: id},
		{Key: "pricecharting", Value: nil},
	})
	if err != nil {
		return false, fmt.Errorf("store: delete unmatched product: %w", err)
	}
	if res.DeletedCount == 0 {
		return false, nil
	}
	if _, err := s.db.Collection(colSnapshots).DeleteMany(ctx,
		bson.D{{Key: "product_id", Value: id}}); err != nil {
		return true, fmt.Errorf("store: delete product snapshots: %w", err)
	}
	return true, nil
}

// ListUnmatchedProducts pages provider-origin products with no
// PriceCharting mapping, INCLUDING match_hold ones (an admin
// revisiting a clear is intended). Sorted oldest updated_at first,
// _id tiebreak, deterministic; also returns the filtered total.
func (s *Store) ListUnmatchedProducts(ctx context.Context, limit, offset int) ([]Product, int64, error) {
	filter := bson.D{
		{Key: "origin", Value: "provider"},
		{Key: "pricecharting", Value: nil},
	}
	return findPage[Product](ctx, s.db.Collection(colProducts), filter, options.Find().
		SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}).
		SetSkip(int64(offset)).
		SetLimit(int64(limit)), "list unmatched products", "list unmatched products")
}

// ListIGDBProducts returns every product with an IGDB projection,
// uncapped like ListPriced (reprojection is read-cheap; a provider
// call fires only for missing/nil-table raw data). Unfiltered on the
// release table, so a projection-logic change self-deploys next run.
func (s *Store) ListIGDBProducts(ctx context.Context) ([]Product, error) {
	return findAll[Product](ctx, s.db.Collection(colProducts),
		bson.D{{Key: "igdb", Value: bson.D{{Key: "$exists", Value: true}}}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}), "list igdb products")
}

// ProductsByIDs returns the products it finds; unknown ids are silently
// absent (batch-prices semantics).
func (s *Store) ProductsByIDs(ctx context.Context, ids []string) ([]Product, error) {
	return findAll[Product](ctx, s.db.Collection(colProducts),
		bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, nil, "products by ids")
}

// SearchByName is the degraded-mode fallback: substring match over
// name and localization fields, mirroring what live search covers so
// an outage doesn't lose native-script finds. Collection scan is
// accepted (small catalog). Provider-origin only; types filters
// server-side, ahead of the limit, so it can't crowd out the requested kind.
func (s *Store) SearchByName(ctx context.Context, types []string, q string, limit int) ([]Product, error) {
	rx := bson.D{{Key: "$regex", Value: regexp.QuoteMeta(q)}, {Key: "$options", Value: "i"}}
	filter := bson.D{
		{Key: "origin", Value: "provider"},
		{Key: "type", Value: bson.D{{Key: "$in", Value: types}}},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "name", Value: rx}},
			bson.D{{Key: "igdb.localizations.name", Value: rx}},
			bson.D{{Key: "igdb.localizations.translit", Value: rx}},
		}},
	}
	return findAll[Product](ctx, s.db.Collection(colProducts), filter,
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}).SetLimit(int64(limit)), "search by name")
}

// SearchCommunityProducts matches community products by name
// (case-insensitive substring; unindexed regex is fine, population is tiny).
func (s *Store) SearchCommunityProducts(ctx context.Context, types []string, q string, limit int) ([]Product, error) {
	filter := bson.D{
		{Key: "origin", Value: "community"},
		{Key: "type", Value: bson.D{{Key: "$in", Value: types}}},
		{Key: "name", Value: bson.D{
			{Key: "$regex", Value: regexp.QuoteMeta(q)},
			{Key: "$options", Value: "i"},
		}},
	}
	return findAll[Product](ctx, s.db.Collection(colProducts), filter,
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}).SetLimit(int64(limit)), "search community")
}

// CommunityRegionRef is one community product's curated region
// string and id (twin of collection's OpenRegionEntryRef).
type CommunityRegionRef struct {
	ID     string
	Region string
}

// ListCommunityRegionDocs lists community products whose curated
// region is outside known (regionkit.KnownRegions from the caller).
// Excluding exact known matches, not just empty ones, lets repeated
// runs converge to zero; a synonym like "usa" stays selected until
// the handler folds it in.
func (s *Store) ListCommunityRegionDocs(ctx context.Context, known []string) ([]CommunityRegionRef, error) {
	nin := bson.A{"", nil}
	for _, k := range known {
		nin = append(nin, k)
	}
	filter := bson.D{
		{Key: "origin", Value: "community"},
		{Key: "community.region", Value: bson.D{{Key: "$nin", Value: nin}}},
	}
	opts := options.Find().
		SetProjection(bson.D{{Key: "community.region", Value: 1}}).
		SetSort(bson.D{{Key: "_id", Value: 1}})
	// findAll's T is this projection's shape, not CommunityRegionRef;
	// the Community.Region -> Region remap below is what findAll can't absorb.
	type regionDoc struct {
		ID        string        `bson:"_id"`
		Community CommunityMeta `bson:"community"`
	}
	rows, err := findAll[regionDoc](ctx, s.db.Collection(colProducts), filter, opts, "list community region docs")
	if err != nil {
		return nil, err
	}
	out := make([]CommunityRegionRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, CommunityRegionRef{ID: row.ID, Region: row.Community.Region})
	}
	return out, nil
}

// SetCommunityRegion rewrites one community product's curated region.
// Matched-count is unchecked: a doc that left community origin or
// vanished between list and write is a silent no-op, not an error.
func (s *Store) SetCommunityRegion(ctx context.Context, id, region string) error {
	// Origin-scoped, like ReplacePromoteCandidates: a promote racing
	// this write is a no-op, not stray residue on a provider doc.
	_, err := s.db.Collection(colProducts).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "origin", Value: "community"}},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "community.region", Value: region},
				{Key: "updated_at", Value: time.Now().UTC().Truncate(time.Millisecond)},
			}},
		})
	if err != nil {
		return fmt.Errorf("store: set community region: %w", err)
	}
	return nil
}

// ListCommunityProducts returns every community product (the sweep's
// worklist; tiny by construction - admin-moderated mints only).
func (s *Store) ListCommunityProducts(ctx context.Context) ([]Product, error) {
	return findAll[Product](ctx, s.db.Collection(colProducts),
		bson.D{{Key: "origin", Value: "community"}},
		options.Find().SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}), "list community")
}

// ListCommunityProductsPage pages un-promoted community products
// (promote flips origin to provider, leaving this set). Sorted oldest
// updated_at first, _id tiebreak, deterministic; returns filtered count too.
func (s *Store) ListCommunityProductsPage(ctx context.Context, limit, offset int) ([]Product, int64, error) {
	filter := bson.D{{Key: "origin", Value: "community"}}
	return findPage[Product](ctx, s.db.Collection(colProducts), filter, options.Find().
		SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}).
		SetSkip(int64(offset)).
		SetLimit(int64(limit)), "list community products", "list community products")
}

// ReplacePromoteCandidates swaps a product's candidate set (caller
// filters dismissed pairs, sorts best-first). No updated_at bump.
// Origin-guarded to community: a doc promoted mid-write is a no-op.
func (s *Store) ReplacePromoteCandidates(ctx context.Context, id string, cands []PromoteCandidate) error {
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "promote_candidates", Value: cands}}}}
	if len(cands) == 0 {
		update = bson.D{{Key: "$unset", Value: bson.D{{Key: "promote_candidates", Value: ""}}}}
	}
	if _, err := s.db.Collection(colProducts).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "origin", Value: "community"}}, update); err != nil {
		return fmt.Errorf("store: replace candidates: %w", err)
	}
	return nil
}

// ListPromoteCandidateProducts pages community products carrying
// candidates, strongest first (promote_candidates.0.score), _id
// tiebreak; productID narrows to one product when non-empty.
func (s *Store) ListPromoteCandidateProducts(ctx context.Context, limit, offset int, productID string) ([]Product, int64, error) {
	filter := bson.D{
		{Key: "origin", Value: "community"},
		{Key: "promote_candidates.0", Value: bson.D{{Key: "$exists", Value: true}}},
	}
	if productID != "" {
		filter = append(filter, bson.E{Key: "_id", Value: productID})
	}
	return findPage[Product](ctx, s.db.Collection(colProducts), filter, options.Find().
		SetSort(bson.D{{Key: "promote_candidates.0.score", Value: -1}, {Key: "_id", Value: 1}}).
		SetSkip(int64(offset)).SetLimit(int64(limit)), "count candidates", "list candidates")
}

// DismissPromoteCandidate records the pair as dismissed and drops the
// matching candidate; the sweep never re-flags a dismissed pair.
func (s *Store) DismissPromoteCandidate(ctx context.Context, id, provider string, providerID int64) error {
	res, err := s.db.Collection(colProducts).UpdateByID(ctx, id, bson.D{
		{Key: "$pull", Value: bson.D{{Key: "promote_candidates", Value: bson.D{
			{Key: "provider", Value: provider},
			{Key: "provider_id", Value: providerID},
		}}}},
		{Key: "$addToSet", Value: bson.D{{Key: "dismissed_candidates", Value: CandidateRef{Provider: provider, ProviderID: providerID}}}},
	})
	if err != nil {
		return fmt.Errorf("store: dismiss candidate: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}
