package jwtauthtest_test

import (
	"context"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauthtest"
)

// TestEnv_TokenValidatesEndToEnd drives the whole point of the
// package: a token Token mints must validate against Env's own
// Validator - the real signature check, the real JWKS fetch, the real
// issuer/audience match - and come back with the claims Token put in.
func TestEnv_TokenValidatesEndToEnd(t *testing.T) {
	env := jwtauthtest.NewEnv(t)
	tok := env.Token(t, "11111111-1111-1111-1111-111111111111", "user", "admin")

	claims, err := env.Validator.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.Subject != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if !claims.HasRole("user") || !claims.HasRole("admin") {
		t.Fatalf("roles = %v, want user+admin", claims.Roles)
	}
	if claims.IsService() {
		t.Fatal("a plain user token must not carry token_use=service")
	}
}

// TestEnv_TokenNoRolesIsValidAndRoleless covers the roleless call
// shape (zero variadic args): the token must still validate and carry
// no roles - the shape user's "viewer"/self-only tests rely on.
func TestEnv_TokenNoRolesIsValidAndRoleless(t *testing.T) {
	env := jwtauthtest.NewEnv(t)
	tok := env.Token(t, "viewer")

	claims, err := env.Validator.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(claims.Roles) != 0 {
		t.Fatalf("roles = %v, want none", claims.Roles)
	}
}

// TestEnv_ServiceTokenValidatesAndCarriesNoRoles pins ServiceToken's
// contract: token_use=service, no roles claim, still a real signature
// the Validator accepts - the machine-credential shape collection and
// enrichment mint for their internal endpoints.
func TestEnv_ServiceTokenValidatesAndCarriesNoRoles(t *testing.T) {
	env := jwtauthtest.NewEnv(t)
	tok := env.ServiceToken(t, "svc:catalog-refresh")

	claims, err := env.Validator.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.Subject != "svc:catalog-refresh" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if !claims.IsService() {
		t.Fatal("want token_use=service")
	}
	if len(claims.Roles) != 0 {
		t.Fatalf("roles = %v, want none", claims.Roles)
	}
}

// TestEnv_IndependentAcrossCalls: unlike valkeytest's container, Env
// has no reason to be a singleton; each NewEnv boots its own
// in-process key and JWKS server, cheaply. A token from one Env must
// not validate against another Env's Validator (different key pairs),
// which is what this pins.
func TestEnv_IndependentAcrossCalls(t *testing.T) {
	a := jwtauthtest.NewEnv(t)
	b := jwtauthtest.NewEnv(t)
	tokFromA := a.Token(t, "u1", "user")

	if _, err := b.Validator.Validate(context.Background(), tokFromA); err == nil {
		t.Fatal("a token signed by one Env's key must not validate against a different Env's Validator")
	}
}

// TestEnv_GarbageTokenFailsValidation is the negative control for the
// happy-path tests above: Validate must actually reject something that
// is not a validly-signed JWT, not rubber-stamp everything it is
// handed.
func TestEnv_GarbageTokenFailsValidation(t *testing.T) {
	env := jwtauthtest.NewEnv(t)
	if _, err := env.Validator.Validate(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("want an error validating a non-JWT string")
	}
}
