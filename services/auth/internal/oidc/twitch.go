package oidc

import "net/url"

// NewTwitch returns the Twitch relying party: user:read:email authorizes email release, but
// fields only land in the ID token via the claims param below. issuerURL is caller-supplied for test fakes.
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
