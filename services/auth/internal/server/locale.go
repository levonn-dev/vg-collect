package server

import "golang.org/x/text/language"

// bestLanguageTag picks the highest-quality tag from an
// Accept-Language header; empty when the header is absent or
// unparseable. Auth only forwards it: the user service owns mapping
// it to an account default.
func bestLanguageTag(header string) string {
	if header == "" {
		return ""
	}
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return ""
	}
	return tags[0].String()
}
