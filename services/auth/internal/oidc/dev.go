package oidc

import "regexp"

var devFamilyPattern = regexp.MustCompile(`^e2e-[a-z0-9-]{1,60}$`)

// DevClaims resolves a dev fixture handle. The literal below holds the
// ONLY fixed identities the dev provider can authenticate; it lives
// inside the function so no code path can grow or mutate it at
// runtime. Dev tooling therefore cannot mint tokens that impersonate a
// real account: real users always log in through a real provider.
//
// The admin fixture starts with the default user role like everyone
// else; granting the admin role is a deliberate manual step (see
// bruno/README.md).
//
// Besides the fixed trio, any e2e-* name mints a derived fixture
// identity: browser test runs create throwaway users this way so
// tests never share accounts. These are synthetic fixtures like alice
// and bob; the dev provider stays disabled outside dev stacks.
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
