package config

import (
	"strings"
	"testing"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("MONGO_URL", "mongodb://u:p@localhost:27017/enrichment")
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("JWKS_URL", "http://auth:8080/.well-known/jwks.json")
	t.Setenv("INTERNAL_REFRESH_SECRETS", "dev-refresh-token")
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

func TestLoad_MissingRequired(t *testing.T) {
	t.Setenv("MONGO_URL", "")
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
	t.Setenv("JWKS_URL", "http://auth:8080/.well-known/jwks.json")
	if _, err := Load(); err == nil {
		t.Fatal("want error for missing MONGO_URL")
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

func TestLoad_InternalRefreshSecrets(t *testing.T) {
	setBase(t)
	t.Setenv("INTERNAL_REFRESH_SECRETS", "current-token,previous-token")
	cfg, err := Load()
	if err != nil || len(cfg.InternalRefreshSecrets) != 2 {
		t.Fatalf("A/B pair must parse: %+v, %v", cfg.InternalRefreshSecrets, err)
	}
	t.Setenv("INTERNAL_REFRESH_SECRETS", "")
	if _, err := Load(); err == nil {
		t.Fatal("want error for missing INTERNAL_REFRESH_SECRETS")
	}
	t.Setenv("INTERNAL_REFRESH_SECRETS", "good,,")
	if _, err := Load(); err == nil {
		t.Fatal("want error for empty entries in the accepted set")
	}
}
