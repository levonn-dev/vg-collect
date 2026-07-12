package server

import "testing"

func TestCurrencyForLocale(t *testing.T) {
	cases := []struct {
		hint, want string
	}{
		{"de-DE", "EUR"},
		{"de", "EUR"},    // region inferred from the likely subtag
		{"bg-BG", "EUR"}, // Bulgaria adopted the euro; BG no longer maps to BGN
		{"en-GB", "GBP"},
		{"ja-JP", "JPY"},
		{"ja", "JPY"},
		{"pt-BR", "BRL"},
		{"en-US", "USD"},
		{"en", "USD"},
		{"ar-EG", "USD"}, // unmapped region falls back
		{"", "USD"},
		{"not a tag !!!", "USD"},
	}
	for _, tc := range cases {
		if got := currencyForLocale(tc.hint); got != tc.want {
			t.Errorf("currencyForLocale(%q) = %q, want %q", tc.hint, got, tc.want)
		}
	}
}
