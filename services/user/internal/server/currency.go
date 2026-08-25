package server

import "golang.org/x/text/language"

// currencyByRegion maps ISO 3166 regions to a new account's default
// currency, matching the ECB reference list plus USD (unmapped falls back
// to USD). Bulgaria adopted the euro on 2026-01-01, replacing BGN.
var currencyByRegion = map[string]string{
	// eurozone
	"AT": "EUR", "BE": "EUR", "BG": "EUR", "CY": "EUR", "DE": "EUR", "EE": "EUR",
	"ES": "EUR", "FI": "EUR", "FR": "EUR", "GR": "EUR", "HR": "EUR",
	"IE": "EUR", "IT": "EUR", "LT": "EUR", "LU": "EUR", "LV": "EUR",
	"MT": "EUR", "NL": "EUR", "PT": "EUR", "SI": "EUR", "SK": "EUR",
	// one region, one currency
	"AU": "AUD", "BR": "BRL", "CA": "CAD", "CH": "CHF",
	"CN": "CNY", "CZ": "CZK", "DK": "DKK", "GB": "GBP", "HK": "HKD",
	"HU": "HUF", "ID": "IDR", "IL": "ILS", "IN": "INR", "IS": "ISK",
	"JP": "JPY", "KR": "KRW", "MX": "MXN", "MY": "MYR", "NO": "NOK",
	"NZ": "NZD", "PH": "PHP", "PL": "PLN", "RO": "RON", "SE": "SEK",
	"SG": "SGD", "TH": "THB", "TR": "TRY", "US": "USD", "ZA": "ZAR",
}

// currencyForLocale derives a default currency from a BCP 47 tag ("de-DE"
// -> EUR; bare "de" via the likely subtag); absent, unparseable, or
// unmapped hints default to USD. source is the vg.user.currency.seeds
// label ("locale" if the hint mapped, "fallback" otherwise).
func currencyForLocale(hint string) (currency, source string) {
	if hint == "" {
		return "USD", "fallback"
	}
	tag, err := language.Parse(hint)
	if err != nil {
		return "USD", "fallback"
	}
	region, _ := tag.Region()
	if cur, ok := currencyByRegion[region.String()]; ok {
		return cur, "locale"
	}
	return "USD", "fallback"
}
