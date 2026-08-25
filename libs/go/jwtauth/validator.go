package jwtauth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenUseService is the token_use claim marking a JWT as a short-lived machine service
// credential (minted only by auth's internal service-token endpoint), not a user's own access
// token. A dedicated claim, not a role: it identifies the KIND of principal, orthogonal to
// what a user principal is allowed to do.
const TokenUseService = "service"

// Claims holds the validated, deserialized fields from a vgkeep JWT.
type Claims struct {
	Subject string
	Roles   []string
	JTI     string
	// TokenUse is the token_use claim, present only on service tokens (TokenUseService);
	// empty on ordinary user access tokens.
	TokenUse string
}

// HasRole reports whether the Claims include the named role.
func (c Claims) HasRole(role string) bool {
	return slices.Contains(c.Roles, role)
}

// IsService reports whether the Claims identify a machine service
// token (token_use=service) rather than a user's own access token.
func (c Claims) IsService() bool {
	return c.TokenUse == TokenUseService
}

// Validator validates vgkeep access JWTs against a JWKS endpoint.
type Validator struct {
	cache    *keyCache
	issuer   string
	audience string
}

// NewValidator returns a Validator that refetches the JWKS at most once
// per 30 seconds when a kid is not found in cache.
func NewValidator(jwksURL, issuer, audience string) *Validator {
	return NewValidatorWithRefetchInterval(jwksURL, issuer, audience, 30*time.Second)
}

// NewValidatorWithRefetchInterval returns a Validator with a custom
// minimum refetch interval. Pass 0 to always refetch on unknown kid.
func NewValidatorWithRefetchInterval(jwksURL, issuer, audience string, minRefetch time.Duration) *Validator {
	return &Validator{cache: newKeyCache(jwksURL, minRefetch), issuer: issuer, audience: audience}
}

// Validate parses and fully verifies raw (a compact-serialised JWT),
// returning the extracted Claims on success.
func (v *Validator) Validate(ctx context.Context, raw string) (Claims, error) {
	mc := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, mc, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid header")
		}
		return v.cache.get(ctx, kid)
	},
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second), // inter-pod clock skew vs 5-min TTLs
	)
	if err != nil {
		return Claims{}, fmt.Errorf("jwtauth: %w", err)
	}
	sub, err := mc.GetSubject()
	if err != nil || sub == "" {
		return Claims{}, errors.New("jwtauth: missing sub")
	}
	out := Claims{Subject: sub}
	if jti, ok := mc["jti"].(string); ok {
		out.JTI = jti
	}
	if rs, ok := mc["roles"].([]any); ok {
		for _, r := range rs {
			if s, ok := r.(string); ok {
				out.Roles = append(out.Roles, s)
			}
		}
	}
	if tu, ok := mc["token_use"].(string); ok {
		out.TokenUse = tu
	}
	return out, nil
}
