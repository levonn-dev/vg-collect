package oidc

// NewGoogle returns the Google relying party. Google is vanilla OIDC:
// the email and profile scopes put email, email_verified, name, and
// picture straight into the ID token. issuerURL is the service config's
// concern (defaulting to the real Google issuer there) so tests and
// local fakes can stand in for it.
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
