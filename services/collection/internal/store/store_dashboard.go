// Dashboard aggregates: counts, spend, pricing rows, and library summary.

package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

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

// CoverURLs returns the first non-empty cover urls of a shelf's
// filtered set in shelf order - the summary card strip.
func (s *Store) CoverURLs(ctx context.Context, userID uuid.UUID, f Filters, limit int) ([]string, error) {
	where, args := filterWhere(userID, f)
	where = append(where, "cover_url IS NOT NULL", "cover_url <> ''")
	args = append(args, limit)
	rows, err := s.pool.Query(ctx,
		`SELECT cover_url FROM entries WHERE `+strings.Join(where, " AND ")+
			` ORDER BY `+orderClause(f.Sort, f.Order)+` LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("store: cover urls: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("store: scan cover: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
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
