package server

import "testing"

func TestBestLanguageTag(t *testing.T) {
	cases := []struct {
		header, want string
	}{
		{"de-DE,de;q=0.9,en;q=0.5", "de-DE"},
		{"en;q=0.8,de;q=0.9", "de"},
		{"ja", "ja"},
		{"", ""},
		{";;;garbage===", ""},
	}
	for _, tc := range cases {
		if got := bestLanguageTag(tc.header); got != tc.want {
			t.Errorf("bestLanguageTag(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}
