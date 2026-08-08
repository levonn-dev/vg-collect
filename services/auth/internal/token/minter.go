// Package token signs vgkeep access JWTs and generates opaque
// refresh tokens. This is the only minting code in the system; every
// other service validates via the shared jwtauth library.
package token

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
)

// Minter signs access JWTs (EdDSA) with a kid header derived from the
// public key, so JWKS consumers can pick the right key without
// coordination.
type Minter struct {
	key      ed25519.PrivateKey
	kid      string
	issuer   string
	audience string
	ttl      time.Duration
}

// NewMinter builds a Minter from a base64 (std) encoded 32-byte Ed25519
// seed, the format the JWT_SIGNING_KEY secret uses.
func NewMinter(seedB64, issuer, audience string, ttl time.Duration) (*Minter, error) {
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil {
		return nil, fmt.Errorf("token: decode signing seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("token: signing seed must be 32 bytes")
	}
	key := ed25519.NewKeyFromSeed(seed)
	return &Minter{
		key:      key,
		kid:      KidFor(key.Public().(ed25519.PublicKey)),
		issuer:   issuer,
		audience: audience,
		ttl:      ttl,
	}, nil
}

// KidFor derives a stable key id from a public key: the first 16 hex
// chars of its SHA-256. Every replica derives the same kid for the
// same key, so key registration is naturally idempotent.
func KidFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

func (m *Minter) Kid() string                  { return m.kid }
func (m *Minter) PublicKey() ed25519.PublicKey { return m.key.Public().(ed25519.PublicKey) }
func (m *Minter) TTL() time.Duration           { return m.ttl }

// Mint signs an access JWT for sub carrying roles and the given jti.
func (m *Minter) Mint(sub string, roles []string, jti string) (string, error) {
	now := time.Now()
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss":   m.issuer,
		"aud":   m.audience,
		"sub":   sub,
		"jti":   jti,
		"iat":   now.Unix(),
		"exp":   now.Add(m.ttl).Unix(),
		"roles": roles,
	})
	t.Header["kid"] = m.kid
	return t.SignedString(m.key)
}

// ServiceToken mints the short-lived token this service presents when
// calling other vgkeep services (role "service").
func (m *Minter) ServiceToken() (string, error) {
	return m.Mint("svc:auth", []string{"service"}, uuid.NewString())
}

// MintService signs a short-lived machine access JWT for the internal
// service-token endpoint: no roles (a service is not a user with
// grantable roles) and token_use=service, the claim that marks it
// distinguishable from any user's own access token. ttl overrides the
// minter's configured default: a service token's lifetime (900s for
// every consumer today) is independent of ACCESS_TOKEN_TTL, the login
// flow's.
func (m *Minter) MintService(sub string, ttl time.Duration) (string, error) {
	now := time.Now()
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss":       m.issuer,
		"aud":       m.audience,
		"sub":       sub,
		"jti":       uuid.NewString(),
		"iat":       now.Unix(),
		"exp":       now.Add(ttl).Unix(),
		"token_use": jwtauth.TokenUseService,
	})
	t.Header["kid"] = m.kid
	return t.SignedString(m.key)
}
