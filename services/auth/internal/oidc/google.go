package oidc

// NewGoogle returns the Google relying party: email/profile scopes yield email,
// email_verified, name, and picture claims. issuerURL is caller-supplied for test fakes.
func NewGoogle(clientID, clientSecret, redirectURL, issuerURL string) *RP {
	return NewRP(RPConfig{
		Name:         "google",
		IssuerURL:    issuerURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile"},
	}, nil)
}
