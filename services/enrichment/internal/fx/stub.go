package fx

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed fixtures.json
var fixturesJSON []byte

// Stub serves the embedded fixture snapshot: fixed date, round rates,
// fully deterministic for dev clusters and e2e assertions.
type Stub struct {
	rates Rates
}

// NewStub parses the embedded fixtures.
func NewStub() (*Stub, error) {
	var r Rates
	if err := json.Unmarshal(fixturesJSON, &r); err != nil {
		return nil, fmt.Errorf("fx: fixtures: %w", err)
	}
	return &Stub{rates: r}, nil
}

// Latest returns the fixture snapshot.
func (s *Stub) Latest(_ context.Context) (Rates, error) {
	return s.rates, nil
}
