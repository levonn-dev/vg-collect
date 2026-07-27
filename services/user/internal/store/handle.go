package store

import (
	"regexp"
	"strings"
)

// handleRe is the typed form: 2-30 chars, first and last alphanumeric,
// underscores interior only (consecutive allowed - decoration folds).
var handleRe = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9_]{0,28}[a-zA-Z0-9])?$`)

// nonAlnumRuns matches every run of characters outside the handle
// alphabet, for the derivation transform.
var nonAlnumRuns = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// ReservedHandles are folded keys no user may claim; checking on the
// fold means a_dmin cannot impersonate admin. Keys must already be
// folded (lowercase, no underscores).
var ReservedHandles = map[string]bool{
	"admin": true, "api": true, "explore": true, "feed": true,
	"login": true, "me": true, "search": true, "settings": true,
	"shelves": true, "u": true, "vg": true,
}

// NormalizeHandle folds a handle to its identity key. MUST stay
// byte-equivalent to the generated column expression in migration
// 000003: lower(replace(handle, '_', empty)).
func NormalizeHandle(h string) string {
	return strings.ToLower(strings.ReplaceAll(h, "_", ""))
}

// ValidHandle reports whether the typed form is acceptable. A valid
// typed form always folds to a key of at least 2 characters (first and
// last are alphanumeric), so no separate fold-length check is needed.
func ValidHandle(h string) bool {
	if len(h) < 2 || len(h) > 30 {
		return false
	}
	return handleRe.MatchString(h)
}

// DeriveHandle builds a mint candidate from the provider name seed:
// whitespace/symbol runs become single underscores, source casing is
// kept, edges are trimmed, length clamps to 30. Falls back to the email
// local part transformed the same way, then to "collector". The result
// is NOT deduplicated; Upsert appends a numeric suffix on collision.
func DeriveHandle(seed, emailLocal string) string {
	for _, candidate := range []string{seed, emailLocal, "collector"} {
		h := nonAlnumRuns.ReplaceAllString(candidate, "_")
		h = strings.Trim(h, "_")
		if len(h) > 30 {
			h = h[:30]
			h = strings.TrimRight(h, "_")
		}
		if len(NormalizeHandle(h)) >= 2 {
			return h
		}
	}
	return "collector"
}

// localPart returns the email's local part for derivation fallback.
func localPart(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return email
}
