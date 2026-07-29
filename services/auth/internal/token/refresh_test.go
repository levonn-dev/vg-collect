package token_test

import (
	"regexp"
	"testing"

	"github.com/levonn-dev/vgkeep/services/auth/internal/token"
)

func TestNewRefreshToken(t *testing.T) {
	raw, hash := token.NewRefreshToken()
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(raw) {
		t.Fatalf("raw = %q, want 43 url-safe base64 chars", raw)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Fatalf("hash = %q, want 64 hex chars", hash)
	}
	if token.HashRefreshToken(raw) != hash {
		t.Fatal("HashRefreshToken(raw) != hash")
	}
	raw2, hash2 := token.NewRefreshToken()
	if raw == raw2 || hash == hash2 {
		t.Fatal("two tokens collided")
	}
}
