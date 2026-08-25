package config_test

import (
	"os"
	"testing"

	"github.com/levonn-dev/vgkeep/services/user/internal/config"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWKS_URL", "http://auth/jwks")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.JWTIssuer != "vgkeep-auth" || cfg.JWTAudience != "vgkeep" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	// t.Setenv can't represent "unset", so this Unsets each var after to
	// test required's absent-only mode (notEmpty separately catches
	// present-but-empty). t.Setenv's cleanup still restores the env at test end.
	for _, k := range []string{"DATABASE_URL", "JWKS_URL"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetenv %s: %v", k, err)
		}
	}
	if _, err := config.Load(); err == nil {
		t.Fatal("want error without DATABASE_URL/JWKS_URL")
	}
}
