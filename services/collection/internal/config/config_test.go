package config

import (
	"strings"
	"testing"
)

func setValid(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://c:p@localhost:5432/collection?sslmode=disable")
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("JWKS_URL", "http://auth:8080/.well-known/jwks.json")
	t.Setenv("ENRICHMENT_SERVICE_URL", "http://enrichment:8080")
}

func TestLoadDefaults(t *testing.T) {
	setValid(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.JWTIssuer != "vg-collect-auth" ||
		cfg.JWTAudience != "vg-collect" || cfg.DashboardCacheTTL.Minutes() != 5 || cfg.Version != "dev" {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestLoadRejectsMissingAndEmptyRequired(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "VALKEY_URL", "JWKS_URL", "ENRICHMENT_SERVICE_URL"} {
		t.Run(name+" empty", func(t *testing.T) {
			setValid(t)
			// Present but empty: `required` alone would NOT catch this;
			// the notEmpty tag is the guard under test.
			t.Setenv(name, "")
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("an empty %s must fail startup, got %v", name, err)
			}
		})
	}
}

func TestLoadRequiresCAForRediss(t *testing.T) {
	setValid(t)
	t.Setenv("VALKEY_URL", "rediss://collection-valkey:6379/0")
	if _, err := Load(); err == nil {
		t.Fatal("rediss without VALKEY_CA_FILE must fail")
	}
	t.Setenv("VALKEY_CA_FILE", "/etc/vg/valkey-ca/ca.crt")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}
