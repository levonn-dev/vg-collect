// Dashboard aggregates and value-history composition.

package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// dashboardFilters funnels the dashboard's filter params through the
// entries-list validator (same dimensions, same 400 details); sort,
// order, grouping, and paging ride the validator's defaults and are
// ignored by the aggregates.
func dashboardFilters(p api.GetDashboardParams) (store.Filters, string) {
	f, _, _, _, detail := listParams(api.ListEntriesParams{
		ItemType:      castSlice[api.ListEntriesParamsItemType](p.ItemType),
		Status:        castSlice[api.ListEntriesParamsStatus](p.Status),
		Packaging:     castSlice[api.ListEntriesParamsPackaging](p.Packaging),
		Region:        p.Region,
		Developer:     p.Developer,
		Publisher:     p.Publisher,
		ItemCondition: castSlice[api.ListEntriesParamsItemCondition](p.ItemCondition),
		PlatformId:    p.PlatformId,
		TagId:         p.TagId,
	})
	return f, detail
}

// GetDashboard composes SQL aggregates with one batched enrichment
// price call, cached briefly per user. Enrichment being down degrades
// pricing (available=false) and skips the cache write so recovery is
// visible immediately. Filtered requests skip the cache both ways:
// the unfiltered dashboard is the hot default view, while filter
// combinations are unbounded and cheap to compute live.
func (h *Handlers) GetDashboard(w http.ResponseWriter, r *http.Request, params api.GetDashboardParams) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	f, detail := dashboardFilters(params)
	if detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_param", detail)
		return
	}
	sub := userID.String()
	if !f.Filtered() {
		body, err := h.cache.GetDashboard(r.Context(), sub)
		if err != nil {
			h.failOpen(r.Context(), "dashboard_get", err)
		}
		h.cacheLookup(r.Context(), "dashboard", body != nil)
		if body != nil {
			writeRawJSON(w, body)
			return
		}
	}

	counts, err := h.store.DashboardCounts(r.Context(), userID, f)
	if err != nil {
		h.internalError(w, r, "aggregation failed", err)
		return
	}
	rows, err := h.store.PricingRows(r.Context(), userID, f)
	if err != nil {
		h.internalError(w, r, "aggregation failed", err)
		return
	}

	pricing := api.DashboardPricing{Available: true}
	var ids []uuid.UUID
	var customTotal int64
	customPriced := 0
	for _, row := range rows {
		if row.PricingMode == "custom" {
			// The DB CHECK guarantees the value under custom mode.
			customTotal += *row.CustomValueCents
			customPriced++
			continue
		}
		if id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID); id != nil {
			ids = append(ids, *id)
		} else {
			pricing.ExcludedEntries++
		}
	}
	pricing.PricedEntries = customPriced
	if len(ids) > 0 {
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, ids)
		h.composeEvent(r.Context(), "dashboard", err)
		if err != nil {
			pricing.Available = false
			h.logger.WarnContext(r.Context(), "dashboard pricing unavailable", "err", err)
		} else {
			total := customTotal
			for _, row := range rows {
				if row.PricingMode == "custom" {
					continue
				}
				id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID)
				if id == nil {
					continue
				}
				p, okPrice := prices[id.String()]
				v := (*int64)(nil)
				if okPrice {
					v = valueForPackaging(row.Packaging, p)
				}
				if v != nil {
					total += *v
					pricing.PricedEntries++
				} else {
					pricing.UnpricedEntries++
				}
			}
			pricing.TotalValueCents = &total
		}
	} else {
		pricing.TotalValueCents = &customTotal
	}

	byPlatform := make([]api.PlatformCount, len(counts.ByPlatform))
	for i, p := range counts.ByPlatform {
		name := p.Name
		if name == "" {
			name = "Unknown"
		}
		byPlatform[i] = api.PlatformCount{Name: name, Count: p.Count}
	}
	spend := make([]api.CurrencySpend, len(counts.Spend))
	for i, s := range counts.Spend {
		spend[i] = api.CurrencySpend{Currency: s.Currency, TotalCents: s.TotalCents}
	}
	dash := api.Dashboard{
		TotalEntries: counts.Total,
		ByStatus:     counts.ByStatus,
		ByItemType:   counts.ByItemType,
		ByPlatform:   byPlatform,
		Spend:        spend,
		Pricing:      pricing,
	}
	body, err := json.Marshal(dash)
	if err != nil {
		h.internalError(w, r, "encoding failed", err)
		return
	}
	if pricing.Available && !f.Filtered() {
		if err := h.cache.PutDashboard(r.Context(), sub, body, h.dashboardTTL); err != nil {
			h.failOpen(r.Context(), "dashboard_put", err)
		}
	}
	writeRawJSON(w, body)
}

// valueHistoryDays fixes the composition window; a window parameter
// can be added later without breaking the contract.
const valueHistoryDays = 90

// pointForPackaging picks the packaging-matched price field from one
// snapshot; nil when the snapshot lists none for that condition.
func pointForPackaging(packaging string, p enrichapi.PricePoint) *int64 {
	switch packaging {
	case "sealed":
		return p.NewCents
	case "cib":
		return p.CibCents
	default:
		return p.LooseCents
	}
}

// ComposeValueSeries builds one point per day - the union of snapshot
// days plus each custom-priced row's set-at day (clamped into the
// window). Product-priced entries contribute their packaging-matched
// price from the latest snapshot on or before the day; custom-priced
// entries contribute their amount from their set-at day forward.
// Prices carry forward between points; entries with nothing known yet
// contribute nothing that day. Each product's points are sorted
// oldest-first internally, regardless of the order series arrives in.
// Exported for tests.
func ComposeValueSeries(rows []store.PricingRow, series map[string][]enrichapi.PricePoint, windowStart time.Time) []api.ValuePoint {
	for _, points := range series {
		sort.SliceStable(points, func(i, j int) bool { return points[i].CapturedAt.Before(points[j].CapturedAt) })
	}
	windowDay := windowStart.UTC().Truncate(24 * time.Hour)
	daySet := map[time.Time]bool{}
	for _, points := range series {
		for _, p := range points {
			daySet[p.CapturedAt.UTC().Truncate(24*time.Hour)] = true
		}
	}
	customDay := func(row store.PricingRow) time.Time {
		d := row.CustomValueSetAt.UTC().Truncate(24 * time.Hour)
		if d.Before(windowDay) {
			return windowDay
		}
		return d
	}
	for _, row := range rows {
		if row.PricingMode == "custom" {
			daySet[customDay(row)] = true
		}
	}
	days := make([]time.Time, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	out := make([]api.ValuePoint, 0, len(days))
	for _, day := range days {
		var total int64
		for _, row := range rows {
			if row.PricingMode == "custom" {
				if !customDay(row).After(day) {
					total += *row.CustomValueCents
				}
				continue
			}
			id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID)
			if id == nil {
				continue
			}
			points := series[id.String()]
			var latest *enrichapi.PricePoint
			for i := range points {
				if points[i].CapturedAt.UTC().Truncate(24 * time.Hour).After(day) {
					break
				}
				latest = &points[i]
			}
			if latest == nil {
				continue
			}
			if v := pointForPackaging(row.Packaging, *latest); v != nil {
				total += *v
			}
		}
		out = append(out, api.ValuePoint{Date: openapi_types.Date{Time: day}, ValueCents: total})
	}
	return out
}

// GetValueHistory answers the caller's collection value over the last
// ninety days: the CURRENT entry set valued at historical snapshot
// prices (the composition does not reconstruct past collection
// contents). Cached and invalidated exactly like the dashboard; a
// degraded answer is served but never cached.
func (h *Handlers) GetValueHistory(w http.ResponseWriter, r *http.Request) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	sub := userID.String()
	body, err := h.cache.GetValueHistory(r.Context(), sub)
	if err != nil {
		h.failOpen(r.Context(), "value_history_get", err)
	}
	h.cacheLookup(r.Context(), "value_history", body != nil)
	if body != nil {
		writeRawJSON(w, body)
		return
	}

	// Value history is always whole-collection: snapshots record
	// aggregate history, so no filter narrows this composition.
	rows, err := h.store.PricingRows(r.Context(), userID, store.Filters{})
	if err != nil {
		h.internalError(w, r, "aggregation failed", err)
		return
	}
	var ids []uuid.UUID
	for _, row := range rows {
		if row.PricingMode == "custom" {
			continue
		}
		if id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID); id != nil {
			ids = append(ids, *id)
		}
	}
	vh := api.ValueHistory{Available: true, Points: []api.ValuePoint{}}
	series := map[string][]enrichapi.PricePoint{}
	if len(ids) > 0 {
		var err error
		series, err = h.enrichment.PriceHistory(r.Context(), bearer, ids, valueHistoryDays)
		h.composeEvent(r.Context(), "value_history", err)
		if err != nil {
			vh.Available = false
			h.logger.WarnContext(r.Context(), "value history unavailable", "err", err)
		}
	}
	if vh.Available {
		windowStart := time.Now().UTC().AddDate(0, 0, -valueHistoryDays)
		vh.Points = ComposeValueSeries(rows, series, windowStart)
	}
	body, err = json.Marshal(vh)
	if err != nil {
		h.internalError(w, r, "encoding failed", err)
		return
	}
	if vh.Available {
		if err := h.cache.PutValueHistory(r.Context(), sub, body, h.dashboardTTL); err != nil {
			h.failOpen(r.Context(), "value_history_put", err)
		}
	}
	writeRawJSON(w, body)
}

// GetLibrarySummary answers the deduplicated game library, shaped for
// the enrichment scoring contract.
func (h *Handlers) GetLibrarySummary(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	lib, err := h.store.LibrarySummary(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, "summary failed", err)
		return
	}
	games := make([]api.LibraryGame, len(lib))
	for i, g := range lib {
		games[i] = api.LibraryGame{IgdbGameId: g.IGDBGameID, Rating: g.Rating}
		if g.AllDropped {
			dropped := "dropped"
			games[i].Status = &dropped
		}
	}
	writeJSON(w, http.StatusOK, api.LibrarySummary{Library: games})
}

// castSlice re-types a generated enum slice onto its mirror from
// another operation; the dashboard params repeat the entries-list
// contract, only the generated Go types differ.
func castSlice[Dst ~string, Src ~string](src *[]Src) *[]Dst {
	if src == nil {
		return nil
	}
	out := make([]Dst, len(*src))
	for i, v := range *src {
		out[i] = Dst(v)
	}
	return &out
}
