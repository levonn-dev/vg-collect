package oidc

import (
	"strings"
	"testing"
)

func TestDevClaims(t *testing.T) {
	tests := []struct {
		name        string
		user        string
		wantOK      bool
		wantSubject string
		wantEmail   string
		wantDisplay string
	}{
		{"alice fixture", "alice", true, "dev-alice", "alice@example.com", "Alice Fixture"},
		{"bob fixture", "bob", true, "dev-bob", "bob@example.com", "Bob Fixture"},
		{"admin fixture", "admin", true, "dev-admin", "admin@example.com", "Admin Fixture"},
		{"family name", "e2e-w0-abc123", true, "dev-e2e-w0-abc123", "e2e-w0-abc123@example.com", "e2e-w0-abc123"},
		{"family single char suffix", "e2e-1", true, "dev-e2e-1", "e2e-1@example.com", "e2e-1"},
		{"unknown fixture", "carol", false, "", "", ""},
		{"empty", "", false, "", "", ""},
		{"bare prefix", "e2e-", false, "", "", ""},
		{"uppercase rejected", "e2e-Alice", false, "", "", ""},
		{"space rejected", "e2e-a b", false, "", "", ""},
		{"max length accepted", "e2e-" + strings.Repeat("a", 60), true, "dev-e2e-" + strings.Repeat("a", 60), "e2e-" + strings.Repeat("a", 60) + "@example.com", "e2e-" + strings.Repeat("a", 60)},
		{"over-length rejected", "e2e-" + strings.Repeat("a", 61), false, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := DevClaims(tt.user)
			if ok != tt.wantOK {
				t.Fatalf("DevClaims(%q) ok = %v, want %v", tt.user, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if c.Subject != tt.wantSubject || c.Email != tt.wantEmail || c.DisplayName != tt.wantDisplay || !c.EmailVerified {
				t.Fatalf("DevClaims(%q) = %+v", tt.user, c)
			}
		})
	}
}
