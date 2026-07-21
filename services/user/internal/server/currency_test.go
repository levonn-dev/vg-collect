package server

import "testing"

func TestCurrencyForLocale(t *testing.T) {
	cases := []struct {
		hint, want, wantSource string
	}{
		{"de-DE", "EUR", "locale"},
		{"de", "EUR", "locale"},    // region inferred from the likely subtag
		{"bg-BG", "EUR", "locale"}, // Bulgaria adopted the euro; BG no longer maps to BGN
		{"en-GB", "GBP", "locale"},
		{"ja-JP", "JPY", "locale"},
		{"ja", "JPY", "locale"},
		{"pt-BR", "BRL", "locale"},
		{"en-US", "USD", "locale"}, // a US hint mapping to USD is a locale hit, not a fallback
		{"en", "USD", "locale"},
		{"ar-EG", "USD", "fallback"},         // unmapped region falls back
		{"", "USD", "fallback"},              // absent hint
		{"not a tag !!!", "USD", "fallback"}, // unparseable hint
	}
	for _, tc := range cases {
		got, source := currencyForLocale(tc.hint)
		if got != tc.want || source != tc.wantSource {
			t.Errorf("currencyForLocale(%q) = %q, %q, want %q, %q", tc.hint, got, source, tc.want, tc.wantSource)
		}
	}
}
