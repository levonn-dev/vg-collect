// Package pricecharting speaks the PriceCharting API: shared types, a
// real keyed client, and a stub over embedded fixtures with a
// deterministic date-seeded price walk. The mode switch
// (PRICECHARTING_MODE) picks which one main.go wires in.
package pricecharting

import "errors"

// ErrNotFound: the provider answered but does not know the product id.
var ErrNotFound = errors.New("pricecharting: product not found")

// Product mirrors the API's product object: hyphenated field names,
// prices as integer pennies (nil = the provider lists no price for
// that condition).
type Product struct {
	ID              int64  `json:"id"`
	Name            string `json:"product-name"`
	ConsoleName     string `json:"console-name"`
	Genre           string `json:"genre,omitempty"`
	LoosePriceCents *int64 `json:"loose-price,omitempty"`
	CIBPriceCents   *int64 `json:"cib-price,omitempty"`
	NewPriceCents   *int64 `json:"new-price,omitempty"`
}
