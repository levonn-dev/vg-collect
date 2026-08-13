// Package catalogval holds catalog input-validation rules shared by
// collection and enrichment. Neither service can import the other's
// internals, and this is validation logic rather than HTTP plumbing
// (httpkit's domain), so a small shared lib is the home for the rules
// both sides need identically.
package catalogval

import (
	"strings"
	"unicode/utf8"
)

// NormalizeCredits trims a curated credit list, drops empty elements,
// and enforces the contract caps the generated router does not check
// itself (maxItems 10, maxLength 120 per name). nil in, nil out; a
// non-empty detail is the 400 text.
func NormalizeCredits(field string, names *[]string) ([]string, string) {
	if names == nil {
		return nil, ""
	}
	out := make([]string, 0, len(*names))
	for _, n := range *names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if utf8.RuneCountInString(n) > 120 {
			return nil, field + " names must be at most 120 characters"
		}
		out = append(out, n)
	}
	if len(out) > 10 {
		return nil, field + " must list at most 10 names"
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, ""
}
