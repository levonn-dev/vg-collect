package config_test

import (
	"os"
	"testing"

	"github.com/levonn-dev/vgkeep/services/bff/internal/config"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("COOKIE_KEY", "dmctY29sbGVjdC1kZXYtY29va2llLWtleS0wMDAwMDE=")
	t.Setenv("PUBLIC_ORIGINS", "http://localhost:8090,http://localhost:5173")
	t.Setenv("AUTH_SERVICE_URL", "http://auth:8080")
	t.Setenv("USER_SERVICE_URL", "http://user:8080")
	t.Setenv("ENRICHMENT_SERVICE_URL", "http://enrichment:8080")
	t.Setenv("COLLECTION_SERVICE_URL", "http://collection:8080")
	t.Setenv("SOCIAL_SERVICE_URL", "http://social:8080")
	t.Setenv("VALKEY_URL", "redis://localhost:6379/0")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if len(cfg.PublicOrigins) != 2 || cfg.PublicOrigins[1] != "http://localhost:5173" {
		t.Errorf("PublicOrigins = %v", cfg.PublicOrigins)
	}
	if !cfg.CookieSecure || cfg.ServeStatic {
		t.Errorf("flag defaults wrong: secure=%v static=%v", cfg.CookieSecure, cfg.ServeStatic)
	}
	if cfg.AccessTokenTTL.Minutes() != 5 || cfg.RefreshWindow.Seconds() != 30 || cfg.MeCacheTTL.Seconds() != 45 {
		t.Errorf("duration defaults wrong: %v %v %v", cfg.AccessTokenTTL, cfg.RefreshWindow, cfg.MeCacheTTL)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	// t.Setenv registers cleanup before the explicit Unsetenv below, so this
	// tests required's absent-only failure mode (notEmpty covers present-but-empty separately).
	for _, name := range []string{"COOKIE_KEY", "PUBLIC_ORIGINS", "AUTH_SERVICE_URL", "USER_SERVICE_URL", "ENRICHMENT_SERVICE_URL", "COLLECTION_SERVICE_URL", "SOCIAL_SERVICE_URL", "VALKEY_URL"} {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatalf("unsetenv %s: %v", name, err)
			}
			if _, err := config.Load(); err == nil {
				t.Fatalf("want error when %s is missing", name)
			}
		})
	}
}

func TestLoadRejectsEmptyPublicOrigins(t *testing.T) {
	// required alone accepts present-but-empty; the env library skips slice
	// population on empty strings - an empty PUBLIC_ORIGINS must fail startup, not silently reject every request.
	setRequired(t)
	t.Setenv("PUBLIC_ORIGINS", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("want error when PUBLIC_ORIGINS is set but empty")
	}
}

func TestLoadRejectsRedissWithoutCA(t *testing.T) {
	setRequired(t)
	t.Setenv("VALKEY_URL", "rediss://bff-valkey:6379/0")
	if _, err := config.Load(); err == nil {
		t.Fatal("want error: rediss requires VALKEY_CA_FILE")
	}
	t.Setenv("VALKEY_CA_FILE", "/etc/vg/valkey-ca/ca.crt")
	if _, err := config.Load(); err != nil {
		t.Fatalf("unexpected error with CA set: %v", err)
	}
}
