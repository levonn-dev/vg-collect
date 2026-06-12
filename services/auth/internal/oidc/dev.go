package oidc

// DevClaims resolves a dev fixture handle. The literal below holds the
// ONLY identities the dev provider can authenticate; it lives inside
// the function so no code path can grow or mutate it at runtime. Dev
// tooling therefore cannot mint tokens that impersonate a real account:
// real users always log in through a real provider.
//
// The admin fixture starts with the default user role like everyone
// else; granting the admin role is a deliberate manual step (see
// bruno/README.md).
func DevClaims(user string) (IDClaims, bool) {
	c, ok := map[string]IDClaims{
		"alice": {Subject: "dev-alice", Email: "alice@example.com", EmailVerified: true, DisplayName: "Alice Fixture"},
		"bob":   {Subject: "dev-bob", Email: "bob@example.com", EmailVerified: true, DisplayName: "Bob Fixture"},
		"admin": {Subject: "dev-admin", Email: "admin@example.com", EmailVerified: true, DisplayName: "Admin Fixture"},
	}[user]
	return c, ok
}
