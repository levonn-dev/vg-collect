package server

import "golang.org/x/text/language"

// bestLanguageTag picks the highest-quality tag from Accept-Language, empty when absent
// or unparseable. Auth only forwards it; the user service owns the account-default mapping.
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
