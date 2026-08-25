package pricecharting

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"time"
)

//go:embed fixtures.json
var fixturesJSON []byte

// Stub serves the embedded fixture products with prices from a
// deterministic id+UTC-day walk: stable across restarts, moving day to day.
type Stub struct {
	products []Product
	byID     map[int64]Product

	// now is a test seam; prices depend on the UTC day only.
	now func() time.Time
}

// NewStub parses the embedded fixtures.
func NewStub() (*Stub, error) {
	var f struct {
		Products []Product `json:"products"`
	}
	if err := json.Unmarshal(fixturesJSON, &f); err != nil {
		return nil, fmt.Errorf("pricecharting: fixtures: %w", err)
	}
	s := &Stub{products: f.Products, byID: make(map[int64]Product, len(f.Products)), now: time.Now}
	for _, p := range f.Products {
		s.byID[p.ID] = p
	}
	return s, nil
}

// Search approximates the real endpoint's fuzziness: a hit when the
// folded query contains the folded name or vice versa (so "zelda" and
// the full IGDB spelling both find it). Results are priced as of today.
func (s *Stub) Search(_ context.Context, q string) ([]Product, error) {
	needle := fold(q)
	if needle == "" {
		return nil, nil
	}
	var out []Product
	for _, p := range s.products {
		name := fold(p.Name)
		if strings.Contains(name, needle) || strings.Contains(needle, name) {
			out = append(out, s.priced(p))
		}
	}
	return out, nil
}

// fold lowercases, folds punctuation to spaces, collapses runs, and
// drops a leading article (containment-grade; scoring-grade lives in internal/match).
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	tokens := strings.Fields(b.String())
	if len(tokens) > 1 && tokens[0] == "the" {
		tokens = tokens[1:]
	}
	return strings.Join(tokens, " ")
}

// Product returns one fixture priced as of today, or ErrNotFound.
func (s *Stub) Product(_ context.Context, id int64) (Product, error) {
	p, ok := s.byID[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	return s.priced(p), nil
}

func (s *Stub) priced(p Product) Product {
	loose, cib, brandNew := walkPrices(p.ID, s.now())
	p.LoosePriceCents = &loose
	p.CIBPriceCents = &cib
	p.NewPriceCents = &brandNew
	return p
}

// walkPrices derives the day's prices for a product id: an id-seeded
// base in [5.00, 149.99] modulated by a sinusoid over the day index
// (+/-15%, id-seeded period/phase). Same id + day => same prices.
func walkPrices(id int64, at time.Time) (loose, cib, brandNew int64) {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d", id)
	seed := h.Sum64()

	base := 500 + int64(seed%14500)
	period := 20 + int64((seed>>16)%30)
	phase := int64((seed >> 32) % uint64(period)) //nolint:gosec // G115: bounded by period (% uint64(period)), cannot overflow int64

	day := at.UTC().Unix() / 86400
	f := math.Sin(2 * math.Pi * float64(day+phase) / float64(period))
	loose = int64(math.Round(float64(base) * (1 + 0.15*f)))
	cib = loose * 8 / 5
	brandNew = loose * 11 / 4
	return loose, cib, brandNew
}
