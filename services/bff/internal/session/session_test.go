package session

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testKey = "dmctY29sbGVjdC1kZXYtY29va2llLWtleS0wMDAwMDE=" // base64 of 32 ascii bytes

func mintTestJWT(t *testing.T, sub, jti string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub, "jti": jti, "exp": exp.Unix(), "iss": "test",
	})
	s, err := tok.SignedString([]byte("irrelevant"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseClaims(t *testing.T) {
	exp := time.Now().Add(5 * time.Minute).Truncate(time.Second)
	c, err := ParseClaims(mintTestJWT(t, "user-1", "jti-1", exp))
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "user-1" || c.JTI != "jti-1" || !c.Exp.Equal(exp) {
		t.Fatalf("claims = %+v", c)
	}
}

func TestParseClaimsRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "not-a-jwt", "a.b.c"} {
		if _, err := ParseClaims(in); err == nil {
			t.Fatalf("want error for %q", in)
		}
	}
}

func TestParseClaimsRejectsMissingFields(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "x"})
	s, _ := tok.SignedString([]byte("k"))
	if _, err := ParseClaims(s); err == nil {
		t.Fatal("want error for missing exp/jti")
	}
}

func TestCodecRoundTrip(t *testing.T) {
	codec, err := NewCodec(testKey, true)
	if err != nil {
		t.Fatal(err)
	}
	in := Session{AccessToken: "acc", RefreshToken: "ref"}
	sealed, err := codec.Seal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := codec.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("roundtrip = %+v", out)
	}
	// Two seals of the same session differ (random nonce).
	sealed2, _ := codec.Seal(in)
	if sealed == sealed2 {
		t.Fatal("seals should be nonce-randomized")
	}
}

func TestCodecRejectsTampering(t *testing.T) {
	codec, _ := NewCodec(testKey, true)
	sealed, _ := codec.Seal(Session{AccessToken: "a", RefreshToken: "r"})
	// Flip a byte in the middle of the ciphertext (past the 12-byte nonce,
	// well within the payload). Deterministic regardless of first-char case.
	mid := len(sealed) / 2
	flipped := sealed[:mid] + string(rune(sealed[mid]^1)) + sealed[mid+1:]
	for _, bad := range []string{
		"",
		"AAAA",
		sealed[:len(sealed)-2] + "zz",
		flipped,
	} {
		if _, err := codec.Open(bad); err == nil {
			t.Fatalf("want error for tampered value %q", bad[:min(8, len(bad))])
		}
	}
	other, _ := NewCodec("b3RoZXIta2V5LW90aGVyLWtleS1vdGhlci1rZXktMDE=", true) // different 32 bytes
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("want error opening with a different key")
	}
}

func TestNewCodecValidatesKey(t *testing.T) {
	if _, err := NewCodec("dG9vLXNob3J0", true); err == nil { // "too-short"
		t.Fatal("want error for short key")
	}
	if _, err := NewCodec("%%%not-base64%%%", true); err == nil {
		t.Fatal("want error for bad base64")
	}
}

func TestCookieAttributes(t *testing.T) {
	codec, _ := NewCodec(testKey, true)
	ck := codec.Cookie("sealed-value", 3600)
	if ck.Name != CookieName || ck.Value != "sealed-value" || ck.MaxAge != 3600 {
		t.Fatalf("cookie = %+v", ck)
	}
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" {
		t.Fatalf("cookie attributes = %+v", ck)
	}
	cleared := codec.ClearCookie()
	if cleared.MaxAge != -1 || cleared.Value != "" || !cleared.HttpOnly {
		t.Fatalf("clear cookie = %+v", cleared)
	}

	insecure, _ := NewCodec(testKey, false)
	if insecure.Cookie("v", 1).Secure {
		t.Fatal("secure flag should follow the codec setting")
	}
}

func TestRefreshKeyIsStableAndOpaque(t *testing.T) {
	a, b := RefreshKey("token-a"), RefreshKey("token-a")
	if a != b {
		t.Fatal("RefreshKey must be deterministic")
	}
	if a == "token-a" || len(a) != 64 {
		t.Fatalf("RefreshKey should be a hex sha256, got %q", a)
	}
	if RefreshKey("token-b") == a {
		t.Fatal("distinct tokens must map to distinct keys")
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, _, ok := FromContext(ctx); ok {
		t.Fatal("empty context must not contain a session")
	}
	s := Session{AccessToken: "a", RefreshToken: "r"}
	c := Claims{Sub: "u", JTI: "j", Exp: time.Now()}
	got, gotC, ok := FromContext(NewContext(ctx, s, c))
	if !ok || got != s || gotC.Sub != c.Sub || gotC.JTI != c.JTI {
		t.Fatalf("got %+v %+v ok=%v", got, gotC, ok)
	}
}

// TestOpenRejectsNonCanonicalBase64 confirms that Open uses Strict() decoding:
// re-encoding the sealed bytes with the padded URLEncoding yields a different
// string that a non-strict decoder would accept but Open must reject.
func TestOpenRejectsNonCanonicalBase64(t *testing.T) {
	codec, _ := NewCodec(testKey, true)
	sealed, _ := codec.Seal(Session{AccessToken: "a", RefreshToken: "r"})
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatal(err)
	}
	// Re-encode with standard (padded) alphabet: a different string that
	// would decode to the same bytes under a lax decoder. Strict raw-url
	// Open must reject it.
	padded := base64.URLEncoding.EncodeToString(raw)
	if padded != sealed {
		if _, err := codec.Open(padded); err == nil {
			t.Fatal("Open accepted a padded base64 variant; decode must be strict/raw")
		}
	}
}

// TestCodecUsesFullKeyWidth asserts AES-256 (not AES-128) keying: two keys
// sharing the first 16 bytes but differing in the last 16 must not interop.
// An accidental first-16-byte truncation would let them seal/open each other.
func TestCodecUsesFullKeyWidth(t *testing.T) {
	// 32-byte keys sharing the first half: AES-256 keys these differently,
	// so a seal from one must not open with the other. (An accidental
	// truncation to the first 16 bytes would let them interoperate.)
	keyA := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789ABCDEF"))
	keyB := base64.StdEncoding.EncodeToString([]byte("0123456789abcdefFEDCBA9876543210"))
	ca, err := NewCodec(keyA, true)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := NewCodec(keyB, true)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := ca.Seal(Session{AccessToken: "a", RefreshToken: "r"})
	if _, err := cb.Open(sealed); err == nil {
		t.Fatal("codec ignored the tail of the key (AES-128 downgrade?)")
	}
}

// TestParseClaimsExpEdges pins current behavior for edge-case exp values:
// zero, absent, and string exp are rejected; a negative exp parses to a past
// instant (already-expired token) without erroring.
func TestParseClaimsExpEdges(t *testing.T) {
	mk := func(exp any) string {
		claims := jwt.MapClaims{"sub": "u", "jti": "j"}
		if exp != nil {
			claims["exp"] = exp
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		s, _ := tok.SignedString([]byte("k"))
		return s
	}
	// zero, absent, and string exp are rejected (no usable expiry).
	for _, bad := range []any{nil, 0, "9999"} {
		if _, err := ParseClaims(mk(bad)); err == nil {
			t.Fatalf("want error for exp=%v", bad)
		}
	}
	// a negative exp parses to a past instant (already-expired token).
	c, err := ParseClaims(mk(-1))
	if err != nil {
		t.Fatalf("negative exp should parse: %v", err)
	}
	if !c.Exp.Before(time.Now()) {
		t.Fatal("negative exp should be in the past")
	}
}
