package oidc

import "regexp"

var devFamilyPattern = regexp.MustCompile(`^e2e-[a-z0-9-]{1,60}$`)

// DevClaims maps a dev handle to a fixed identity (alice, bob, admin) or a derived
// e2e-* identity. The map is local to the function, so dev tooling can never mint a real account's identity.
func DevClaims(user string) (IDClaims, bool) {
	c, ok := map[string]IDClaims{
		"alice": {Subject: "dev-alice", Email: "alice@example.com", EmailVerified: true, DisplayName: "Alice Fixture"},
		"bob":   {Subject: "dev-bob", Email: "bob@example.com", EmailVerified: true, DisplayName: "Bob Fixture"},
		"admin": {Subject: "dev-admin", Email: "admin@example.com", EmailVerified: true, DisplayName: "Admin Fixture"},
	}[user]
	if ok {
		return c, true
	}
	if devFamilyPattern.MatchString(user) {
		return IDClaims{Subject: "dev-" + user, Email: user + "@example.com", EmailVerified: true, DisplayName: user}, true
	}
	return IDClaims{}, false
}
