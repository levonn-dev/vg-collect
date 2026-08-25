package config

import (
	"os"
	"strings"
	"testing"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("MONGO_URL", "mongodb://u:p@localhost:27017/enrichment")
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("JWKS_URL", "http://auth:8080/.well-known/jwks.json")
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.MongoDB != "enrichment" || cfg.IGDBMode != "stub" ||
		cfg.PriceChartingMode != "stub" || cfg.SearchCacheTTL.Hours() != 24 ||
		cfg.ProductCacheTTL.Minutes() != 5 || cfg.IGDBRefreshAfter.Hours() != 720 {
		t.Fatalf("defaults: %+v", cfg)
	}
}

// Pins that a genuinely absent MONGO_URL fails loading. The required
// tag fires only on an absent key, not present-but-empty, so this
// unsets rather than sets "" (t.Setenv can't represent "absent").
func TestLoad_MissingRequired(t *testing.T) {
	prev, had := os.LookupEnv("MONGO_URL")
	if err := os.Unsetenv("MONGO_URL"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("MONGO_URL", prev)
		}
	})
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("JWKS_URL", "http://auth:8080/.well-known/jwks.json")
	if _, err := Load(); err == nil {
		t.Fatal("want error for missing MONGO_URL")
	}
}

func TestLoad_MongoCredentialPairBothOrNeither(t *testing.T) {
	setBase(t)
	if _, err := Load(); err != nil {
		t.Fatalf("credential-less pair should load: %v", err)
	}

	t.Setenv("MONGO_USERNAME", "enrichment")
	if _, err := Load(); err == nil {
		t.Fatal("want error for MONGO_USERNAME without MONGO_PASSWORD")
	}

	t.Setenv("MONGO_USERNAME", "")
	t.Setenv("MONGO_PASSWORD", "s3cret")
	if _, err := Load(); err == nil {
		t.Fatal("want error for MONGO_PASSWORD without MONGO_USERNAME")
	}

	t.Setenv("MONGO_USERNAME", "enrichment")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("full credential pair should load: %v", err)
	}
	if cfg.MongoUsername != "enrichment" || cfg.MongoPassword != "s3cret" {
		t.Fatalf("credential pair not parsed: %+v", cfg)
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
