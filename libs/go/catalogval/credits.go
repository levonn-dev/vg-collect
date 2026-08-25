// Package catalogval holds catalog input-normalization rules shared by collection and
// enrichment: domain logic, not HTTP plumbing, so services that can't import each other's
// internals share it here.
package catalogval

import "strings"

// NormalizeCredits trims a curated credit list and drops empty elements; nil in, nil out. Cap
// enforcement (maxItems, per-name maxLength) happens upstream in the request validator.
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
