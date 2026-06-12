package oidc

import "net/url"

// NewTwitch returns the Twitch relying party. Twitch quirks: the
// user:read:email scope authorizes email release, but the fields only
// appear in the ID token when requested via the OIDC claims parameter.
// issuerURL is the service config's concern (defaulting to the real
// Twitch issuer there) so tests and local fakes can stand in for it.
func NewTwitch(clientID, clientSecret, redirectURL, issuerURL string) *RP {
	claims := `{"id_token":{"email":null,"email_verified":null,"preferred_username":null,"picture":null}}`
	return NewRP(RPConfig{
		Name:            "twitch",
		IssuerURL:       issuerURL,
		ClientID:        clientID,
		ClientSecret:    clientSecret,
		RedirectURL:     redirectURL,
		Scopes:          []string{"openid", "user:read:email"},
		ExtraAuthParams: url.Values{"claims": {claims}},
	}, nil)
}
