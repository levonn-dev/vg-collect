// Package store owns the enrichment service's SQL. No other package
// writes queries against this schema.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
)

// Sentinels the handlers branch on via errors.Is.
var (
	ErrNotFound = errors.New("store: not found")
	// ErrIdentityTaken is the sentinel for a mapping write the unique
	// identity indexes refuse: another product already owns the identity.
	ErrIdentityTaken = errors.New("store: identity taken")
)

// Store is the query surface over the enrichment database.
type Store struct{ pool *pgxpool.Pool }

// New builds a Store over the migrated pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// querier is the subset of pgx querying shared by pool and tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// isUniqueViolation reports a Postgres unique_violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

// queryAll runs a query and scans every row, wrapping both error legs
// under one op text. Zero matches yields nil, not [].
func queryAll[T any](ctx context.Context, q querier, sql string, args []any, scan func(pgx.Rows) (T, error), op string) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", op, err)
	}
	out, err := pgkit.ScanAll(rows, nil, scan)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", op, err)
	}
	return out, nil
}

// queryPage is queryAll's paginated sibling, also returning the total
// match count; countOp/findOp let each caller word the two errors differently.
func queryPage[T any](ctx context.Context, q querier, countSQL, pageSQL string, countArgs, pageArgs []any, scan func(pgx.Rows) (T, error), countOp, findOp string) ([]T, int64, error) {
	var total int64
	if err := q.QueryRow(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: %s: %w", countOp, err)
	}
	out, err := queryAll(ctx, q, pageSQL, pageArgs, scan, findOp)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// jsonbPtr encodes an optional subdocument for a jsonb column: nil
// pointer becomes SQL NULL, never a json null scalar.
func jsonbPtr[T any](v *T) (any, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// jsonbSlice encodes an array for a jsonb column: empty means SQL NULL
// (the exists-style filters key on IS NOT NULL).
func jsonbSlice[T any](v []T) (any, error) {
	if len(v) == 0 {
		return nil, nil
	}
	return json.Marshal(v)
}

// likeEscape neutralizes LIKE metacharacters so q matches literally.
func likeEscape(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

const productCols = `id, type, origin, name, region, edition, variant, match_hold,
	igdb, platform, pricecharting, community, promote_candidates, dismissed_candidates,
	created_at, updated_at`

func scanProduct(rows pgx.Rows) (Product, error) {
	var p Product
	err := rows.Scan(&p.ID, &p.Type, &p.Origin, &p.Name, &p.Region, &p.Edition, &p.Variant,
		&p.MatchHold, &p.IGDB, &p.Platform, &p.PriceCharting, &p.Community,
		&p.PromoteCandidates, &p.DismissedCandidates, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Product{}, err
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, nil
}

// Platform is the product's platform reference (a projection of the
// IGDB platform, denormalized for display and identity).
type Platform struct {
	IGDBID  int64  `json:"igdb_id"`
	Name    string `json:"name"`
	LogoURL string `json:"logo_url,omitempty"`
}

// CatalogPlatform is one cached platform-catalog row; LogoURL is
// precomputed at upsert (the raw IGDB image id is not kept).
type CatalogPlatform struct {
	ID           int64
	Name         string
	Abbreviation string
	Generation   int
	LogoURL      string
}

// Genre keeps both the IGDB id (recommendation queries) and the
// display name.
type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Company is a credited company with its role flags.
type Company struct {
	Name      string `json:"name"`
	Developer bool   `json:"developer"`
	Publisher bool   `json:"publisher"`
}

// MetaReleaseDate is one per-region date for the product's own
// platform (canonical IGDB region name, day-truncated date).
type MetaReleaseDate struct {
	Region string    `json:"region"`
	Date   time.Time `json:"date"`
}

// MetaLocalization is one region's presentation: IGDB region id
// stored verbatim (ja-JP, EU, ko-KR); fields are independently optional.
type MetaLocalization struct {
	Region   string `json:"region"`
	Name     string `json:"name,omitempty"`
	Translit string `json:"translit,omitempty"`
	CoverURL string `json:"cover_url,omitempty"`
}

// IGDBMeta is the game-metadata projection embedded in a product; it
// refreshes on its own cadence via SetIGDB (partial update).
type IGDBMeta struct {
	GameID           int64              `json:"game_id"`
	Name             string             `json:"name"`
	CoverURL         string             `json:"cover_url,omitempty"`
	Genres           []Genre            `json:"genres"`
	Themes           []string           `json:"themes"`
	Franchises       []string           `json:"franchises"`
	SimilarGames     []int64            `json:"similar_games"`
	Companies        []Company          `json:"companies"`
	FirstReleaseDate time.Time          `json:"first_release_date,omitempty"`
	ReleaseDates     []MetaReleaseDate  `json:"release_dates"`
	Localizations    []MetaLocalization `json:"localizations"`
	FetchedAt        time.Time          `json:"fetched_at"`
}

// PriceQuote holds one condition triple in integer cents; nil means
// the provider lists no price for that condition.
type PriceQuote struct {
	LooseCents *int64 `json:"loose_cents,omitempty"`
	CIBCents   *int64 `json:"cib_cents,omitempty"`
	NewCents   *int64 `json:"new_cents,omitempty"`
}

// PCMeta is the PriceCharting mapping + current prices, refreshed
// daily via SetCurrentPrices. A product without one is unmatched.
type PCMeta struct {
	PCProductID     int64      `json:"pc_product_id"`
	PCName          string     `json:"pc_name"`
	ConsoleName     string     `json:"console_name"`
	MatchConfidence float64    `json:"match_confidence"`
	Verified        bool       `json:"verified"`
	Current         PriceQuote `json:"current"`
	AsOf            time.Time  `json:"as_of"`
}

// CommunityMeta carries community-mint facts, kept after promotion
// as gap-fill (provider fields win). Region lives here, not on
// Product: community docs carry no provider hardware identity.
type CommunityMeta struct {
	PlatformName     string    `json:"platform_name,omitempty"`
	Region           string    `json:"region,omitempty"`
	FirstReleaseDate time.Time `json:"first_release_date,omitempty"`
	CoverURL         string    `json:"cover_url,omitempty"`
	Developers       []string  `json:"developers,omitempty"`
	Publishers       []string  `json:"publishers,omitempty"`
}

// PromoteCandidate is one sweep hit: a provider item whose name
// plausibly matches a community product; promotion is a human verdict.
type PromoteCandidate struct {
	Provider   string    `json:"provider"`
	ProviderID int64     `json:"provider_id"`
	Name       string    `json:"name"`
	Score      float64   `json:"score"`
	FoundAt    time.Time `json:"found_at"`
}

// CandidateRef identifies a dismissed candidate pair.
type CandidateRef struct {
	Provider   string `json:"provider"`
	ProviderID int64  `json:"provider_id"`
}

// Product is the canonical catalog document, lazily created on user
// selection. Identity keys on (igdb game, platform, pc listing); a
// missing mapping is the family's unmatched member. Region/edition/
// variant stay always-present (empty = unspecified) for shape stability.
type Product struct {
	ID            string
	Type          string
	Name          string
	Platform      *Platform
	Region        string
	Edition       string
	Variant       string
	IGDB          *IGDBMeta
	PriceCharting *PCMeta
	// MatchHold pins a deliberate admin mapping clear against automated
	// rematching; any mapping set lifts it.
	MatchHold bool
	// Origin separates provider-identified from admin-minted community
	// products; community docs sit outside the identity indexes (name
	// is their identity) and surface via community search until promoted.
	Origin    string
	Community *CommunityMeta
	// PromoteCandidates is stored sorted best-first (index 0 is the
	// worklist sort key); DismissedCandidates silences pairs permanently.
	PromoteCandidates   []PromoteCandidate
	DismissedCandidates []CandidateRef
	CreatedAt           time.Time
	UpdatedAt           time.Time
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

func (k ProductKey) filterSQL() (string, []any) {
	// A pc_listing IS the exact variant: identity is the listing id
	// alone (region/edition/variant are stored empty).
	if k.Type == "pc_listing" {
		return "type = 'pc_listing' AND pc_product_id = $1", []any{k.PCProductID}
	}
	if k.Type == "game" {
		// Zero PCProductID addresses the unmatched member (SQL NULL,
		// mirroring the NULLS NOT DISTINCT unique index).
		pcID := any(k.PCProductID)
		if k.PCProductID == 0 {
			pcID = nil
		}
		return "type = 'game' AND igdb_game_id = $1 AND platform_igdb_id = $2 AND pc_product_id IS NOT DISTINCT FROM $3",
			[]any{k.IGDBGameID, k.PlatformIGDBID, pcID}
	}
	// Zero PCProductID addresses the unmatched member here too (SQL
	// NULL, mirroring the NULLS NOT DISTINCT unique index).
	pcID := any(k.PCProductID)
	if k.PCProductID == 0 {
		pcID = nil
	}
	return "type = $1 AND pc_product_id IS NOT DISTINCT FROM $2 AND region = $3 AND edition = $4 AND variant = $5",
		[]any{k.Type, pcID, k.Region, k.Edition, k.Variant}
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

// oneProduct runs a single-row product query, or ErrNotFound on zero rows.
func (s *Store) oneProduct(ctx context.Context, where string, args []any, op string) (Product, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+productCols+" FROM products WHERE "+where+" LIMIT 1", args...)
	if err != nil {
		return Product{}, fmt.Errorf("store: %s: %w", op, err)
	}
	out, err := pgkit.ScanAll(rows, nil, scanProduct)
	if err != nil {
		return Product{}, fmt.Errorf("store: %s: %w", op, err)
	}
	if len(out) == 0 {
		return Product{}, ErrNotFound
	}
	return out[0], nil
}

// FindProduct returns the product with the given identity, or
// ErrNotFound.
func (s *Store) FindProduct(ctx context.Context, key ProductKey) (Product, error) {
	where, args := key.filterSQL()
	return s.oneProduct(ctx, where, args, "find product")
}

// GetProduct fetches by id, or ErrNotFound.
func (s *Store) GetProduct(ctx context.Context, id string) (Product, error) {
	return s.oneProduct(ctx, "id = $1", []any{id}, "get product")
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
	igdbArg, err := jsonbPtr(p.IGDB)
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	platformArg, err := jsonbPtr(p.Platform)
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	pcArg, err := jsonbPtr(p.PriceCharting)
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	communityArg, err := jsonbPtr(p.Community)
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	candsArg, err := jsonbSlice(p.PromoteCandidates)
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	dismissedArg, err := jsonbSlice(p.DismissedCandidates)
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO products
		(id, type, origin, name, region, edition, variant, match_hold,
		 igdb, platform, pricecharting, community, promote_candidates, dismissed_candidates,
		 created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		p.ID, p.Type, p.Origin, p.Name, p.Region, p.Edition, p.Variant, p.MatchHold,
		igdbArg, platformArg, pcArg, communityArg, candsArg, dismissedArg,
		p.CreatedAt, p.UpdatedAt)
	if isUniqueViolation(err) {
		return s.FindProduct(ctx, keyOf(p))
	}
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	return p, nil
}

// SetIGDB replaces the product's IGDB projection (its refresh cadence
// is independent of pricing).
func (s *Store) SetIGDB(ctx context.Context, id string, m IGDBMeta) error {
	v, err := jsonbPtr(&m)
	if err != nil {
		return fmt.Errorf("store: set igdb: %w", err)
	}
	tag, err := s.pool.Exec(ctx, "UPDATE products SET igdb = $2, updated_at = $3 WHERE id = $1",
		id, v, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return fmt.Errorf("store: set igdb: %w", err)
	}
	if tag.RowsAffected() == 0 {
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
	var tag pgconn.CommandTag
	var err error
	if m == nil {
		tag, err = s.pool.Exec(ctx,
			"UPDATE products SET pricecharting = NULL, match_hold = true, updated_at = $2 WHERE id = $1", id, now)
	} else {
		var v any
		v, err = jsonbPtr(m)
		if err != nil {
			return fmt.Errorf("store: set pricecharting: %w", err)
		}
		tag, err = s.pool.Exec(ctx,
			"UPDATE products SET pricecharting = $2, match_hold = false, updated_at = $3 WHERE id = $1", id, v, now)
	}
	// Clearing can collide too: an unmatched sibling already owns the
	// NULLS NOT DISTINCT game identity this product would fall back to.
	if isUniqueViolation(err) {
		return ErrIdentityTaken
	}
	if err != nil {
		return fmt.Errorf("store: set pricecharting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PromoteProduct atomically attaches provider anchors, flips a
// community product to provider origin, and clears its candidates.
// A duplicate identity returns ErrIdentityTaken; nothing changes.
func (s *Store) PromoteProduct(ctx context.Context, id string, igdbMeta *IGDBMeta, platform *Platform, pc *PCMeta) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	set := []string{"origin = 'provider'", "promote_candidates = NULL", "dismissed_candidates = NULL", "updated_at = $2"}
	args := []any{id, now}
	appendSet := func(col string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if igdbMeta != nil {
		v, err := jsonbPtr(igdbMeta)
		if err != nil {
			return fmt.Errorf("store: promote product: %w", err)
		}
		appendSet("igdb", v)
	}
	if platform != nil {
		v, err := jsonbPtr(platform)
		if err != nil {
			return fmt.Errorf("store: promote product: %w", err)
		}
		appendSet("platform", v)
	}
	if pc != nil {
		v, err := jsonbPtr(pc)
		if err != nil {
			return fmt.Errorf("store: promote product: %w", err)
		}
		appendSet("pricecharting", v)
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE products SET "+strings.Join(set, ", ")+" WHERE id = $1 AND origin = 'community'", args...)
	if isUniqueViolation(err) {
		return ErrIdentityTaken
	}
	if err != nil {
		return fmt.Errorf("store: promote product: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCurrentPrices updates the mapped product's current prices.
// ErrNotFound covers both a missing product and an unmatched one.
func (s *Store) SetCurrentPrices(ctx context.Context, id string, q PriceQuote, asOf time.Time) error {
	current, err := json.Marshal(q)
	if err != nil {
		return fmt.Errorf("store: set current prices: %w", err)
	}
	asOfStr := asOf.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
	tag, err := s.pool.Exec(ctx, `UPDATE products SET
		pricecharting = pricecharting || jsonb_build_object('current', $2::jsonb, 'as_of', to_jsonb($3::text)),
		updated_at = $4
		WHERE id = $1 AND pricecharting IS NOT NULL`,
		id, current, asOfStr, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return fmt.Errorf("store: set current prices: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPriced returns every product with a PriceCharting mapping, in
// stable id order (catalog is small by construction; unpaginated).
func (s *Store) ListPriced(ctx context.Context) ([]Product, error) {
	return queryAll(ctx, s.pool, "SELECT "+productCols+" FROM products WHERE pricecharting IS NOT NULL ORDER BY id",
		nil, scanProduct, "list priced")
}

// DeleteUnmatchedProduct removes a product (and its snapshots, via
// cascade) only while unmatched (a priced identity must be cleared
// first). The deleted bool is false for both missing and matched
// products; the caller must classify which. Entry references are the
// caller's problem.
func (s *Store) DeleteUnmatchedProduct(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM products WHERE id = $1 AND pricecharting IS NULL", id)
	if err != nil {
		return false, fmt.Errorf("store: delete unmatched product: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListUnmatchedProducts pages provider-origin products with no
// PriceCharting mapping, INCLUDING match_hold ones (an admin
// revisiting a clear is intended). Sorted oldest updated_at first,
// id tiebreak, deterministic; also returns the filtered total.
func (s *Store) ListUnmatchedProducts(ctx context.Context, limit, offset int) ([]Product, int64, error) {
	return queryPage(ctx, s.pool,
		"SELECT count(*) FROM products WHERE origin = 'provider' AND pricecharting IS NULL",
		"SELECT "+productCols+" FROM products WHERE origin = 'provider' AND pricecharting IS NULL ORDER BY updated_at, id LIMIT $1 OFFSET $2",
		nil, []any{limit, offset}, scanProduct, "list unmatched products", "list unmatched products")
}

// ListIGDBProducts returns every product with an IGDB projection,
// uncapped like ListPriced (reprojection is read-cheap; a provider
// call fires only for missing/nil-table raw data). Unfiltered on the
// release table, so a projection-logic change self-deploys next run.
func (s *Store) ListIGDBProducts(ctx context.Context) ([]Product, error) {
	return queryAll(ctx, s.pool, "SELECT "+productCols+" FROM products WHERE igdb IS NOT NULL ORDER BY id",
		nil, scanProduct, "list igdb products")
}

// ProductsByIDs returns the products it finds; unknown ids are silently
// absent (batch-prices semantics).
func (s *Store) ProductsByIDs(ctx context.Context, ids []string) ([]Product, error) {
	return queryAll(ctx, s.pool, "SELECT "+productCols+" FROM products WHERE id = ANY($1)",
		[]any{ids}, scanProduct, "products by ids")
}

// SearchByName is the degraded-mode fallback: substring match over
// name and localization fields, mirroring what live search covers so
// an outage doesn't lose native-script finds. Table scan is
// accepted (small catalog). Provider-origin only; types filters
// server-side, ahead of the limit, so it can't crowd out the requested kind.
func (s *Store) SearchByName(ctx context.Context, types []string, q string, limit int) ([]Product, error) {
	pattern := "%" + likeEscape(q) + "%"
	return queryAll(ctx, s.pool, `SELECT `+productCols+` FROM products
		WHERE origin = 'provider' AND type = ANY($1)
		  AND (name ILIKE $2 OR EXISTS (
		      SELECT 1 FROM jsonb_array_elements(coalesce(nullif(igdb->'localizations', 'null'::jsonb), '[]'::jsonb)) loc
		      WHERE loc->>'name' ILIKE $2 OR loc->>'translit' ILIKE $2))
		ORDER BY name, id
		LIMIT $3`,
		[]any{types, pattern, limit}, scanProduct, "search by name")
}

// SearchCommunityProducts matches community products by name
// (case-insensitive substring; an unindexed scan is fine, population is tiny).
func (s *Store) SearchCommunityProducts(ctx context.Context, types []string, q string, limit int) ([]Product, error) {
	return queryAll(ctx, s.pool,
		"SELECT "+productCols+" FROM products WHERE origin = 'community' AND type = ANY($1) AND name ILIKE $2 ORDER BY name, id LIMIT $3",
		[]any{types, "%" + likeEscape(q) + "%", limit}, scanProduct, "search community")
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
	return queryAll(ctx, s.pool, `SELECT id, community->>'region' FROM products
		WHERE origin = 'community' AND coalesce(community->>'region', '') <> ''
		  AND NOT (community->>'region' = ANY($1))
		ORDER BY id`, []any{known},
		func(rows pgx.Rows) (CommunityRegionRef, error) {
			var r CommunityRegionRef
			err := rows.Scan(&r.ID, &r.Region)
			return r, err
		}, "list community region docs")
}

// SetCommunityRegion rewrites one community product's curated region.
// Rows-affected is unchecked: a doc that left community origin or
// vanished between list and write is a silent no-op, not an error.
func (s *Store) SetCommunityRegion(ctx context.Context, id, region string) error {
	// Origin-scoped, like ReplacePromoteCandidates: a promote racing
	// this write is a no-op, not stray residue on a provider doc.
	_, err := s.pool.Exec(ctx,
		`UPDATE products SET community = coalesce(community, '{}'::jsonb) || jsonb_build_object('region', to_jsonb($2::text)), updated_at = $3
		WHERE id = $1 AND origin = 'community'`,
		id, region, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return fmt.Errorf("store: set community region: %w", err)
	}
	return nil
}

// ListCommunityProducts returns every community product (the sweep's
// worklist; tiny by construction - admin-moderated mints only).
func (s *Store) ListCommunityProducts(ctx context.Context) ([]Product, error) {
	return queryAll(ctx, s.pool,
		"SELECT "+productCols+" FROM products WHERE origin = 'community' ORDER BY updated_at, id",
		nil, scanProduct, "list community")
}

// ListCommunityProductsPage pages un-promoted community products
// (promote flips origin to provider, leaving this set). Sorted oldest
// updated_at first, id tiebreak, deterministic; returns filtered count too.
func (s *Store) ListCommunityProductsPage(ctx context.Context, limit, offset int) ([]Product, int64, error) {
	return queryPage(ctx, s.pool,
		"SELECT count(*) FROM products WHERE origin = 'community'",
		"SELECT "+productCols+" FROM products WHERE origin = 'community' ORDER BY updated_at, id LIMIT $1 OFFSET $2",
		nil, []any{limit, offset}, scanProduct, "list community products", "list community products")
}

// ReplacePromoteCandidates swaps a product's candidate set (caller
// filters dismissed pairs, sorts best-first). No updated_at bump.
// Origin-guarded to community: a doc promoted mid-write is a no-op.
func (s *Store) ReplacePromoteCandidates(ctx context.Context, id string, cands []PromoteCandidate) error {
	v, err := jsonbSlice(cands)
	if err != nil {
		return fmt.Errorf("store: replace candidates: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		"UPDATE products SET promote_candidates = $2 WHERE id = $1 AND origin = 'community'", id, v); err != nil {
		return fmt.Errorf("store: replace candidates: %w", err)
	}
	return nil
}

// ListPromoteCandidateProducts pages community products carrying
// candidates, strongest first (promote_candidates.0.score), id
// tiebreak; productID narrows to one product when non-empty.
func (s *Store) ListPromoteCandidateProducts(ctx context.Context, limit, offset int, productID string) ([]Product, int64, error) {
	where := "origin = 'community' AND promote_candidates IS NOT NULL"
	var args []any
	if productID != "" {
		args = append(args, productID)
		where += fmt.Sprintf(" AND id = $%d", len(args))
	}
	pageArgs := append(append([]any{}, args...), limit, offset)
	countSQL := "SELECT count(*) FROM products WHERE " + where
	pageSQL := "SELECT " + productCols + " FROM products WHERE " + where +
		fmt.Sprintf(" ORDER BY (promote_candidates->0->>'score')::float8 DESC, id LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	return queryPage(ctx, s.pool, countSQL, pageSQL, args, pageArgs, scanProduct, "count candidates", "list candidates")
}

// DismissPromoteCandidate records the pair as dismissed and drops the
// matching candidate; the sweep never re-flags a dismissed pair.
func (s *Store) DismissPromoteCandidate(ctx context.Context, id, provider string, providerID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: dismiss candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cands []PromoteCandidate
	var dismissed []CandidateRef
	err = tx.QueryRow(ctx,
		"SELECT promote_candidates, dismissed_candidates FROM products WHERE id = $1 FOR UPDATE", id).
		Scan(&cands, &dismissed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: dismiss candidate: %w", err)
	}
	kept := cands[:0]
	for _, c := range cands {
		if c.Provider != provider || c.ProviderID != providerID {
			kept = append(kept, c)
		}
	}
	ref := CandidateRef{Provider: provider, ProviderID: providerID}
	if !slices.Contains(dismissed, ref) {
		dismissed = append(dismissed, ref)
	}
	candsArg, err := jsonbSlice(kept)
	if err != nil {
		return fmt.Errorf("store: dismiss candidate: %w", err)
	}
	dismissedArg, err := jsonbSlice(dismissed)
	if err != nil {
		return fmt.Errorf("store: dismiss candidate: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"UPDATE products SET promote_candidates = $2, dismissed_candidates = $3 WHERE id = $1",
		id, candsArg, dismissedArg); err != nil {
		return fmt.Errorf("store: dismiss candidate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: dismiss candidate: %w", err)
	}
	return nil
}
