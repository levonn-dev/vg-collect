// Prices: the FX snapshot and batch current-price and price-history
// reads.

package server

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
)

// idsToStrings converts product_ids to the string form the store
// layer takes - the shared tail of BatchPrices and BatchPriceHistory.
// product_ids' maxItems (500, the request cap) is specval's job.
func idsToStrings(ids []openapi_types.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// GetFxLatest serves the provider's cached rate snapshot. Rates power
// display-side conversion in the SPA only; nothing stored ever
// depends on them, so a failure here degrades display to USD.
func (h *Handlers) GetFxLatest(w http.ResponseWriter, r *http.Request) {
	rates, err := h.fxRates.Latest(r.Context())
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_unavailable", "exchange rates are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, api.FXRates{Base: rates.Base, Date: rates.Date, Rates: rates.Rates})
}

// BatchPrices reads current prices straight from the catalog (the
// daily refresh keeps them fresh). Unknown ids are absent from the map;
// unmatched products carry unmatched=true and no price fields.
func (h *Handlers) BatchPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.PricesBatchRequest
	if !httpkit.DecodeBody(w, r, 64*1024, &req) {
		return
	}
	ids := idsToStrings(req.ProductIds)
	prods, err := h.store.ProductsByIDs(ctx, ids)
	if err != nil {
		h.internalError(w, r, "batch_prices", "price lookup failed", err)
		return
	}
	prices := make(map[string]api.ProductPrices, len(prods))
	for _, p := range prods {
		pp := api.ProductPrices{Unmatched: p.PriceCharting == nil}
		if pc := p.PriceCharting; pc != nil {
			pp.LooseCents = pc.Current.LooseCents
			pp.CibCents = pc.Current.CIBCents
			pp.NewCents = pc.Current.NewCents
			asOf := pc.AsOf
			pp.AsOf = &asOf
		}
		prices[p.ID] = pp
	}
	writeJSON(w, http.StatusOK, api.PricesBatchResponse{Prices: prices})
}

// BatchPriceHistory returns each requested product's snapshot series
// inside the window, oldest first (the collection dashboard's
// value-over-time composition reads it). Unknown ids and products with
// no in-window points are absent from the map.
func (h *Handlers) BatchPriceHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.PriceHistoryRequest
	if !httpkit.DecodeBody(w, r, 64*1024, &req) {
		return
	}
	ids := idsToStrings(req.ProductIds)
	// days' range (1-365) is specval's job; only the default-when-absent
	// case needs handling here (the contract's default:90 is documentation
	// - the request validator does not rewrite an absent field into the
	// body, so an omitted days still reaches here as nil).
	days := 90
	if req.Days != nil {
		days = *req.Days
	}
	snaps, err := h.store.SnapshotsSince(ctx, ids, h.now().UTC().AddDate(0, 0, -days))
	if err != nil {
		h.internalError(w, r, "batch_price_history", "history lookup failed", err)
		return
	}
	series := make(map[string][]api.PricePoint, len(snaps))
	for id, points := range snaps {
		out := make([]api.PricePoint, len(points))
		for i, p := range points {
			out[i] = api.PricePoint{
				CapturedAt: p.CapturedAt,
				LooseCents: p.LooseCents,
				CibCents:   p.CIBCents,
				NewCents:   p.NewCents,
			}
		}
		series[id] = out
	}
	writeJSON(w, http.StatusOK, api.PriceHistoryResponse{Series: series})
}
