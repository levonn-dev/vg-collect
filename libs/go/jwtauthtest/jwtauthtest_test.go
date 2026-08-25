package jwtauthtest_test

import (
	"context"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauthtest"
)

// TestEnv_TokenValidatesEndToEnd pins that a Token-minted token validates end to end (real
// signature, JWKS fetch, issuer/audience match) and carries the claims Token set.
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

// TestEnv_TokenNoRolesIsValidAndRoleless covers the roleless call shape: still validates, carries no roles.
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

// TestEnv_ServiceTokenValidatesAndCarriesNoRoles pins that ServiceToken's output validates
// with token_use=service and no roles claim.
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

// TestEnv_IndependentAcrossCalls pins that each NewEnv boots its own key, so a token from one
// Env does not validate against another's Validator.
func TestEnv_IndependentAcrossCalls(t *testing.T) {
	a := jwtauthtest.NewEnv(t)
	b := jwtauthtest.NewEnv(t)
	tokFromA := a.Token(t, "u1", "user")

	if _, err := b.Validator.Validate(context.Background(), tokFromA); err == nil {
		t.Fatal("a token signed by one Env's key must not validate against a different Env's Validator")
	}
}

// TestEnv_GarbageTokenFailsValidation is the negative control: Validate rejects a non-JWT string.
func TestEnv_GarbageTokenFailsValidation(t *testing.T) {
	env := jwtauthtest.NewEnv(t)
	if _, err := env.Validator.Validate(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("want an error validating a non-JWT string")
	}
}
