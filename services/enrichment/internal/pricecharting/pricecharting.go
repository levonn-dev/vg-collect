// Package pricecharting speaks the PriceCharting API: shared types, a
// real keyed client, and a stub over fixtures with a deterministic
// date-seeded price walk; PRICECHARTING_MODE picks which main.go wires in.
package pricecharting

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNotFound means the provider answered but does not know the
// product id.
var ErrNotFound = errors.New("pricecharting: product not found")

// Product mirrors the API's product object: hyphenated field names,
// prices as integer pennies (nil = no listed price for that condition).
type Product struct {
	ID              int64  `json:"id"`
	Name            string `json:"product-name"`
	ConsoleName     string `json:"console-name"`
	Genre           string `json:"genre,omitempty"`
	LoosePriceCents *int64 `json:"loose-price,omitempty"`
	CIBPriceCents   *int64 `json:"cib-price,omitempty"`
	NewPriceCents   *int64 `json:"new-price,omitempty"`
}

// UnmarshalJSON tolerates both id encodings: the live API serializes
// ids as strings; embedded fixtures use numbers.
func (p *Product) UnmarshalJSON(b []byte) error {
	type product Product // methodless clone: a plain decode, no recursion
	var aux struct {
		product
		RawID json.RawMessage `json:"id"` // depth 0 outranks product.ID for "id"
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*p = Product(aux.product)
	if len(aux.RawID) == 0 || string(aux.RawID) == "null" {
		return nil
	}
	id, err := strconv.ParseInt(strings.Trim(string(aux.RawID), `"`), 10, 64)
	if err != nil {
		return fmt.Errorf("pricecharting: product id %s: %w", aux.RawID, err)
	}
	p.ID = id
	return nil
}
