// Package oidc implements the OpenID Connect relying-party side of login: provider adapters
// that produce authorize URLs and exchange authorization codes for verified identity claims.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// IDClaims is what a completed provider login asserts about the person.
type IDClaims struct {
	Subject       string // provider-scoped stable id ("sub")
	Email         string
	EmailVerified bool
	DisplayName   string
	AvatarURL     string
}

// Provider is one configured "login with" target.
type Provider interface {
	Name() string
	// AuthorizeURL builds the provider redirect with state, nonce, and the PKCE S256 challenge.
	AuthorizeURL(ctx context.Context, state, nonce, challenge string) (string, error)
	// Exchange redeems the code (with PKCE verifier), verifies the ID token, and checks nonce.
	Exchange(ctx context.Context, code, verifier, nonce string) (IDClaims, error)
}

// RandomToken returns 32 bytes of CSPRNG output, base64url encoded, for state/nonce/PKCE.
// crypto/rand.Read is documented to never fail on supported platforms; a failure panics.
func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewPKCE returns a fresh code verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string) {
	verifier = RandomToken()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}
