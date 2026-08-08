package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/services/auth/internal/config"
)

var required = []string{"DATABASE_URL", "JWT_SIGNING_KEY", "USER_SERVICE_URL", "INTERNAL_SERVICE_SECRETS"}

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "c2VlZA==")
	t.Setenv("USER_SERVICE_URL", "http://user:8080")
	t.Setenv("INTERNAL_SERVICE_SECRETS", "dev-service-token")
	// Neutralize any provider credentials in the developer's shell so
	// the enablement assertions are hermetic (empty means disabled).
	for _, k := range []string{
		"OAUTH_REDIRECT_URL", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
		"TWITCH_CLIENT_ID", "TWITCH_CLIENT_SECRET",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("DEV_PROVIDER_ENABLED", "false")
}

func TestLoad_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.JWTIssuer != "vgkeep-auth" || cfg.JWTAudience != "vgkeep" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.JWKSURL != "http://localhost:8080/.well-known/jwks.json" {
		t.Fatalf("jwks url default wrong: %+v", cfg)
	}
	if cfg.AccessTokenTTL != 5*time.Minute || cfg.RefreshTokenTTL != 720*time.Hour {
		t.Fatalf("ttl defaults wrong: %+v", cfg)
	}
	if cfg.GoogleIssuerURL != "https://accounts.google.com" ||
		cfg.TwitchIssuerURL != "https://id.twitch.tv/oauth2" {
		t.Fatalf("issuer defaults wrong: %+v", cfg)
	}
	if cfg.DevProviderEnabled || cfg.GoogleEnabled() || cfg.TwitchEnabled() {
		t.Fatalf("nothing should be enabled by default: %+v", cfg)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	prev := map[string]string{}
	for _, k := range required {
		if v, ok := os.LookupEnv(k); ok {
			prev[k] = v
		}
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetenv %s: %v", k, err)
		}
	}
	t.Cleanup(func() {
		for _, k := range required {
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
		t.Fatal("want error when required variables are unset")
	}
}

func TestLoad_RealProviderRequiresRedirectURL(t *testing.T) {
	setRequired(t)
	t.Setenv("GOOGLE_CLIENT_ID", "gid")
	t.Setenv("GOOGLE_CLIENT_SECRET", "gsecret")
	t.Setenv("OAUTH_REDIRECT_URL", "")

	if _, err := config.Load(); err == nil {
		t.Fatal("want error: google enabled without OAUTH_REDIRECT_URL")
	}

	t.Setenv("OAUTH_REDIRECT_URL", "https://app.example/api/auth/callback")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GoogleEnabled() || cfg.TwitchEnabled() {
		t.Fatalf("enablement wrong: %+v", cfg)
	}
}

func TestLoad_HalfConfiguredProviderIsDisabled(t *testing.T) {
	setRequired(t)
	t.Setenv("TWITCH_CLIENT_ID", "tid")
	t.Setenv("TWITCH_CLIENT_SECRET", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TwitchEnabled() {
		t.Fatal("twitch must require both id and secret")
	}
}

func TestLoad_InternalServiceSecrets(t *testing.T) {
	setRequired(t)
	t.Setenv("INTERNAL_SERVICE_SECRETS", "current-token,previous-token")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("A/B pair must parse: %v", err)
	}
	if len(cfg.InternalServiceSecrets) != 2 {
		t.Fatalf("A/B pair must parse: %+v", cfg.InternalServiceSecrets)
	}
	t.Setenv("INTERNAL_SERVICE_SECRETS", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("want error for missing INTERNAL_SERVICE_SECRETS")
	}
	t.Setenv("INTERNAL_SERVICE_SECRETS", "good,,")
	if _, err := config.Load(); err == nil {
		t.Fatal("want error for empty entries in the accepted set")
	}
}
