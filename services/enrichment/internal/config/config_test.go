package config

import (
	"os"
	"strings"
	"testing"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/enrichment")
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("JWKS_URL", "http://auth:8080/.well-known/jwks.json")
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.IGDBMode != "stub" ||
		cfg.PriceChartingMode != "stub" || cfg.SearchCacheTTL.Hours() != 24 ||
		cfg.ProductCacheTTL.Minutes() != 5 || cfg.IGDBRefreshAfter.Hours() != 720 {
		t.Fatalf("defaults: %+v", cfg)
	}
}

// Pins that a genuinely absent DATABASE_URL fails loading. The required
// tag fires only on an absent key, not present-but-empty, so this
// unsets rather than sets "" (t.Setenv can't represent "absent").
func TestLoad_MissingRequired(t *testing.T) {
	prev, had := os.LookupEnv("DATABASE_URL")
	if err := os.Unsetenv("DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("DATABASE_URL", prev)
		}
	})
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("JWKS_URL", "http://auth:8080/.well-known/jwks.json")
	if _, err := Load(); err == nil {
		t.Fatal("want error for missing DATABASE_URL")
	}
}

func TestLoad_RealModesRequireCredentials(t *testing.T) {
	setBase(t)
	t.Setenv("IGDB_MODE", "real")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "IGDB_CLIENT_ID") {
		t.Fatalf("want igdb credential error, got %v", err)
	}
	t.Setenv("IGDB_CLIENT_ID", "id")
	t.Setenv("IGDB_CLIENT_SECRET", "secret")
	if _, err := Load(); err != nil {
		t.Fatalf("full igdb credentials should load: %v", err)
	}
	t.Setenv("PRICECHARTING_MODE", "real")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PRICECHARTING_API_KEY") {
		t.Fatalf("want pricecharting credential error, got %v", err)
	}
	t.Setenv("PRICECHARTING_MODE", "sideways")
	if _, err := Load(); err == nil {
		t.Fatal("want error for an unknown mode")
	}
}

func TestLoad_RedissRequiresCA(t *testing.T) {
	setBase(t)
	t.Setenv("VALKEY_URL", "rediss://enrichment-valkey:6379/0")
	if _, err := Load(); err == nil {
		t.Fatal("want error for rediss without CA")
	}
	t.Setenv("VALKEY_CA_FILE", "/etc/vg/valkey-ca/ca.crt")
	if _, err := Load(); err != nil {
		t.Fatalf("rediss with CA should load: %v", err)
	}
}

func TestLoadFXModeValidation(t *testing.T) {
	setBase(t)

	t.Setenv("FX_MODE", "bogus")
	if _, err := Load(); err == nil {
		t.Fatal("FX_MODE=bogus must fail validation")
	}
	t.Setenv("FX_MODE", "real")
	if _, err := Load(); err != nil {
		t.Fatalf("FX_MODE=real (credential-less): %v", err)
	}
}
