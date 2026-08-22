// Package catalogval holds catalog input-normalization rules shared
// by collection and enrichment. Neither service can import the
// other's internals, and this rule is domain logic rather than HTTP
// plumbing (httpkit's domain), so a small shared lib is the home both
// sides need identically.
package catalogval

import "strings"

// NormalizeCredits trims a curated credit list and drops empty
// elements; nil in, nil out. The contract caps this list used to
// enforce here too (maxItems, per-name maxLength) are the request
// validator's job now - it rejects an over-cap body before this ever
// runs, so this function only ever sees an already-conforming list.
func NormalizeCredits(names *[]string) []string {
	if names == nil {
		return nil
	}
	out := make([]string, 0, len(*names))
	for _, n := range *names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
