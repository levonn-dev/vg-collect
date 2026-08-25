// Package fx speaks the frankfurter.dev exchange-rate API: shared
// types, a real credential-less client (TTL cache, serves stale on
// failure), and a stub over fixtures; FX_MODE picks which wires in.
// Rates are target-units-per-USD; USD itself never appears in the map.
package fx

// Rates is one daily snapshot of USD-based exchange rates.
type Rates struct {
	Base  string             `json:"base"`
	Date  string             `json:"date"`
	Rates map[string]float64 `json:"rates"`
}
