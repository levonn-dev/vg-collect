// Prices: the FX snapshot and batch current-price and price-history
// reads.

package server

import (
	"encoding/json"
	"net/http"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
)

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
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	// The schema's maxItems is documentation; the generated models do
	// not validate, so the cap is enforced here.
	if len(req.ProductIds) > 500 {
		problem(w, r, http.StatusBadRequest, "invalid_body", "at most 500 product_ids per call")
		return
	}
	ids := make([]string, len(req.ProductIds))
	for i, id := range req.ProductIds {
		ids[i] = id.String()
	}
	prods, err := h.store.ProductsByIDs(ctx, ids)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "price lookup failed")
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
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	// The schema's maxItems is documentation; the generated models do
	// not validate, so the cap is enforced here.
	if len(req.ProductIds) > 500 {
		problem(w, r, http.StatusBadRequest, "invalid_body", "at most 500 product_ids per call")
		return
	}
	days := 90
	if req.Days != nil {
		days = *req.Days
	}
	if days < 1 || days > 365 {
		problem(w, r, http.StatusBadRequest, "invalid_body", "days must be between 1 and 365")
		return
	}
	ids := make([]string, len(req.ProductIds))
	for i, id := range req.ProductIds {
		ids[i] = id.String()
	}
	snaps, err := h.store.SnapshotsSince(ctx, ids, h.now().UTC().AddDate(0, 0, -days))
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "history lookup failed")
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
