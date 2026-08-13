package catalogval

import "strings"

// ValidCoverURL enforces the cover-link shape: https only, at most 512
// chars. The image is never fetched server-side (SSRF surface); the
// client renders it with a broken-image fallback.
func ValidCoverURL(s string) bool {
	return len(s) <= 512 && strings.HasPrefix(s, "https://")
}
