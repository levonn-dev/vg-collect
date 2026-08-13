package catalogval_test

// TDD for ValidCoverURL: lifted from the byte-identical validCoverURL
// twins in collection's handlers_entries.go and enrichment's
// handlers_community.go. These cases pin the https-only, 512-byte-cap
// shape both call sites relied on.

import (
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/catalogval"
)

func TestValidCoverURL_512BytesPasses(t *testing.T) {
	const prefix = "https://img.example/"
	url := prefix + strings.Repeat("a", 512-len(prefix))
	if len(url) != 512 {
		t.Fatalf("fixture length = %d, want 512", len(url))
	}
	if !catalogval.ValidCoverURL(url) {
		t.Fatal("a 512-byte https URL must pass")
	}
}

func TestValidCoverURL_513BytesFails(t *testing.T) {
	const prefix = "https://img.example/"
	url := prefix + strings.Repeat("a", 513-len(prefix))
	if len(url) != 513 {
		t.Fatalf("fixture length = %d, want 513", len(url))
	}
	if catalogval.ValidCoverURL(url) {
		t.Fatal("a 513-byte https URL must fail")
	}
}

func TestValidCoverURL_RequiresHTTPSScheme(t *testing.T) {
	cases := []string{
		"http://img.example/cover.jpg",
		"ftp://img.example/cover.jpg",
		"//img.example/cover.jpg",
		"img.example/cover.jpg",
		"HTTPS://img.example/cover.jpg", // scheme match is case-sensitive
	}
	for _, s := range cases {
		if catalogval.ValidCoverURL(s) {
			t.Errorf("ValidCoverURL(%q) = true, want false (not an https:// prefix)", s)
		}
	}
}

func TestValidCoverURL_EmptyStringFails(t *testing.T) {
	if catalogval.ValidCoverURL("") {
		t.Fatal("an empty string must fail")
	}
}

func TestValidCoverURL_BareSchemeIsShortEnoughAndPasses(t *testing.T) {
	// "https://" alone is 8 bytes, well under the cap, and does carry
	// the required prefix (it IS the prefix) - the function checks
	// shape only, never resolves or fetches the URL.
	if !catalogval.ValidCoverURL("https://") {
		t.Fatal(`"https://" satisfies both the prefix and length checks`)
	}
}
