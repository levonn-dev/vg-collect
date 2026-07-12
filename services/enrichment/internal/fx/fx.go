// Package fx speaks the frankfurter.dev exchange-rate API: shared
// types, a real credential-less client with a TTL cache that serves
// stale on upstream failure, and a stub over embedded fixtures. The
// mode switch (FX_MODE) picks which one main.go wires in. Rates are
// target-units-per-USD; USD itself never appears in the map.
package fx

// Rates is one daily snapshot of USD-based exchange rates.
type Rates struct {
	Base  string             `json:"base"`
	Date  string             `json:"date"`
	Rates map[string]float64 `json:"rates"`
}
