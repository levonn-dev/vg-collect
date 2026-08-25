package store

import (
	"regexp"
	"strings"
)

// Slugs address shelves in URLs; the saved-view UUID stays the identity
// everywhere else. Case and underscores fold away (/shelves/backlog resolves
// Backlog). MUST stay byte-equivalent to migration 000009's generated column: lower(replace(slug, '_', '')).

var slugNonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func NormalizeSlug(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

// DeriveSlug transforms a view name into its slug: symbol runs become single
// underscores, casing kept, edges trimmed, clamped to 30, fallback "shelf".
// NOT deduped; create/update paths append a numeric suffix on collision.
func DeriveSlug(name string) string {
	s := slugNonAlnum.ReplaceAllString(name, "_")
	s = strings.Trim(s, "_")
	if len(s) > 30 {
		s = s[:30]
		s = strings.TrimRight(s, "_")
	}
	if len(NormalizeSlug(s)) < 2 {
		return "shelf"
	}
	return s
}
