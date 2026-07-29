package token_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/levonn-dev/vgkeep/services/auth/internal/token"
)

// testSeed is a base64 (std) encoded 32-byte seed, the JWT_SIGNING_KEY
// wire format; built rather than hardcoded so the length is right by
// construction.
var testSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

func newMinter(t *testing.T) *token.Minter {
	t.Helper()
	m, err := token.NewMinter(testSeed, "vgkeep-auth", "vgkeep", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func parse(t *testing.T, m *token.Minter, raw string) (jwt.MapClaims, map[string]any) {
	t.Helper()
	mc := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(raw, mc, func(tk *jwt.Token) (any, error) {
		return m.PublicKey(), nil
	}, jwt.WithValidMethods([]string{"EdDSA"}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return mc, tok.Header
}

func TestMint_ClaimsAndHeader(t *testing.T) {
	m := newMinter(t)
	raw, err := m.Mint("user-123", []string{"user", "admin"}, "jti-1")
	if err != nil {
		t.Fatal(err)
	}
	mc, header := parse(t, m, raw)
	if header["kid"] != m.Kid() {
		t.Fatalf("kid = %v, want %s", header["kid"], m.Kid())
	}
	if mc["iss"] != "vgkeep-auth" || mc["aud"] != "vgkeep" ||
		mc["sub"] != "user-123" || mc["jti"] != "jti-1" {
		t.Fatalf("claims = %v", mc)
	}
	roles, _ := mc["roles"].([]any)
	if len(roles) != 2 || roles[0] != "user" || roles[1] != "admin" {
		t.Fatalf("roles = %v", mc["roles"])
	}
	exp, _ := mc.GetExpirationTime()
	iat, _ := mc.GetIssuedAt()
	if d := exp.Sub(iat.Time); d != 5*time.Minute {
		t.Fatalf("ttl = %v, want 5m", d)
	}
}

func TestKid_DeterministicFromPublicKey(t *testing.T) {
	a, b := newMinter(t), newMinter(t)
	if a.Kid() != b.Kid() || len(a.Kid()) != 16 {
		t.Fatalf("kid not deterministic: %s vs %s", a.Kid(), b.Kid())
	}
	if a.Kid() != token.KidFor(a.PublicKey()) {
		t.Fatal("Kid() disagrees with KidFor()")
	}
}

func TestNewMinter_RejectsBadSeeds(t *testing.T) {
	if _, err := token.NewMinter("not-base64!!!", "i", "a", time.Minute); err == nil {
		t.Fatal("want error for invalid base64")
	}
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := token.NewMinter(short, "i", "a", time.Minute); err == nil {
		t.Fatal("want error for wrong seed length")
	}
}

func TestServiceToken(t *testing.T) {
	m := newMinter(t)
	raw, err := m.ServiceToken()
	if err != nil {
		t.Fatal(err)
	}
	mc, _ := parse(t, m, raw)
	if mc["sub"] != "svc:auth" {
		t.Fatalf("sub = %v", mc["sub"])
	}
	roles, _ := mc["roles"].([]any)
	if len(roles) != 1 || roles[0] != "service" {
		t.Fatalf("roles = %v", mc["roles"])
	}
	if jti, _ := mc["jti"].(string); jti == "" {
		t.Fatal("service token missing jti")
	}
}

func TestPublicKeySize(t *testing.T) {
	if len(newMinter(t).PublicKey()) != ed25519.PublicKeySize {
		t.Fatal("unexpected public key size")
	}
}

func TestMint_TamperedTokenFailsVerification(t *testing.T) {
	m := newMinter(t)
	raw, _ := m.Mint("u", []string{"user"}, "j")
	parts := strings.Split(raw, ".")
	tampered := parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2]))
	mc := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tampered, mc, func(tk *jwt.Token) (any, error) {
		return m.PublicKey(), nil
	}, jwt.WithValidMethods([]string{"EdDSA"}))
	if err == nil {
		t.Fatal("tampered token verified")
	}
}
