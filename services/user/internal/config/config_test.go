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
	// caarlos0/env v11 `required` only fails when the variable is **unset**
	// (os.LookupEnv returns exists=false). Setting it to "" passes required
	// validation. We therefore unset the variables and restore via t.Cleanup
	// to guarantee hermeticity regardless of the caller's environment.
	prev := map[string]string{}
	for _, k := range []string{"DATABASE_URL", "JWKS_URL"} {
		if v, ok := os.LookupEnv(k); ok {
			prev[k] = v
		}
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetenv %s: %v", k, err)
		}
	}
	t.Cleanup(func() {
		for _, k := range []string{"DATABASE_URL", "JWKS_URL"} {
			if v, ok := prev[k]; ok {
				if err := os.Setenv(k, v); err != nil {
					t.Errorf("restore setenv %s: %v", k, err)
				}
			} else {
				if err := os.Unsetenv(k); err != nil {
					t.Errorf("restore unsetenv %s: %v", k, err)
				}
			}
		}
	})

	if _, err := config.Load(); err == nil {
		t.Fatal("want error without DATABASE_URL/JWKS_URL")
	}
}
