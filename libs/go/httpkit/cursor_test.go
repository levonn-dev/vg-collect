package httpkit_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func TestCursor_StringParseRoundTrip(t *testing.T) {
	c := httpkit.Cursor{CreatedAt: time.Unix(0, 1730000000123456789), ID: uuid.New()}
	parsed, err := httpkit.ParseCursor(c.String())
	if err != nil {
		t.Fatalf("ParseCursor: %v", err)
	}
	if !parsed.CreatedAt.Equal(c.CreatedAt) || parsed.ID != c.ID {
		t.Fatalf("round trip = %+v, want %+v", parsed, c)
	}
}

func TestCursor_StringShape(t *testing.T) {
	id := uuid.New()
	c := httpkit.Cursor{CreatedAt: time.Unix(0, 42), ID: id}
	want := "42." + id.String()
	if got := c.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestCursor_ParseRejectsMalformed covers malformed shapes: no dot,
// an empty half on either side, and a uuid-shaped tail that still
// fails once a spurious extra dot shifts the split.
func TestCursor_ParseRejectsMalformed(t *testing.T) {
	cases := []string{"", "abc", "123.", ".uuid", "1.2.not-a-uuid", "notanumber." + uuid.New().String()}
	for _, s := range cases {
		if _, err := httpkit.ParseCursor(s); err == nil {
			t.Errorf("ParseCursor(%q) = nil error, want error", s)
		}
	}
}

// TestCursor_ParseSplitsOnFirstDotOnly guards the uuid-never-contains-
// a-dot assumption ParseCursor relies on: a well-formed cursor parses
// even though its uuid tail has hyphens.
func TestCursor_ParseSplitsOnFirstDotOnly(t *testing.T) {
	id := uuid.New()
	s := "1000." + id.String()
	got, err := httpkit.ParseCursor(s)
	if err != nil {
		t.Fatalf("ParseCursor(%q): %v", s, err)
	}
	if got.ID != id {
		t.Fatalf("ID = %v, want %v", got.ID, id)
	}
}
