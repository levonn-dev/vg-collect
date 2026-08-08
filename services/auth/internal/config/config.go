// Package config declares the auth service's environment contract.
package config

import (
	"errors"
	"strings"
	"time"

	libconfig "github.com/levonn-dev/vgkeep/libs/go/config"
)

// Config holds all environment-sourced configuration for the auth
// service. Provider credentials default to empty: an empty pair keeps
// that provider disabled, so the stack runs with zero real secrets.
type Config struct {
	HTTPAddr    string `env:"HTTP_ADDR"            envDefault:":8080"`
	DatabaseURL string `env:"DATABASE_URL,required"`

	JWTSigningKey   string        `env:"JWT_SIGNING_KEY,required"` // base64 (std) 32-byte Ed25519 seed
	JWTIssuer       string        `env:"JWT_ISSUER"           envDefault:"vgkeep-auth"`
	JWTAudience     string        `env:"JWT_AUDIENCE"         envDefault:"vgkeep"`
	AccessTokenTTL  time.Duration `env:"ACCESS_TOKEN_TTL"     envDefault:"5m"`
	RefreshTokenTTL time.Duration `env:"REFRESH_TOKEN_TTL"    envDefault:"720h"`

	UserServiceURL string `env:"USER_SERVICE_URL,required"`

	// Accepted internal-caller tokens for POST /internal/service-token
	// (the catalog-refresh and entry-rematch CronJobs' bootstrap
	// trigger, which has no JWT source). One or two entries: an A/B
	// pair makes rotation zero-downtime.
	InternalServiceSecrets []string `env:"INTERNAL_SERVICE_SECRETS,required,notEmpty" envSeparator:","`

	// JWKSURL feeds the bearer validator on the self-service endpoints
	// (identity list/unlink, link start, account wipe). The service
	// verifies its own tokens against its own JWKS; the default matches
	// HTTP_ADDR's default port inside the pod.
	JWKSURL string `env:"JWKS_URL" envDefault:"http://localhost:8080/.well-known/jwks.json"`

	// The public callback URL registered with real providers (the BFF
	// route once the edge exists). Required only when a real provider
	// is configured.
	OAuthRedirectURL string `env:"OAUTH_REDIRECT_URL"`

	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	GoogleIssuerURL    string `env:"GOOGLE_ISSUER_URL"    envDefault:"https://accounts.google.com"`

	TwitchClientID     string `env:"TWITCH_CLIENT_ID"`
	TwitchClientSecret string `env:"TWITCH_CLIENT_SECRET"`
	TwitchIssuerURL    string `env:"TWITCH_ISSUER_URL"    envDefault:"https://id.twitch.tv/oauth2"`

	// The dev provider mints sessions for fixture users only; never
	// enable it in a deployment reachable by anyone you do not trust.
	DevProviderEnabled bool `env:"DEV_PROVIDER_ENABLED" envDefault:"false"`

	Version string `env:"SERVICE_VERSION" envDefault:"dev"`
}

func (c Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func (c Config) TwitchEnabled() bool {
	return c.TwitchClientID != "" && c.TwitchClientSecret != ""
}

// Load parses environment variables and enforces cross-field rules.
func Load() (Config, error) {
	cfg, err := libconfig.Load[Config]()
	if err != nil {
		return Config{}, err
	}
	if (cfg.GoogleEnabled() || cfg.TwitchEnabled()) && cfg.OAuthRedirectURL == "" {
		return Config{}, errors.New("config: OAUTH_REDIRECT_URL is required when a real OAuth provider is configured")
	}
	for _, s := range cfg.InternalServiceSecrets {
		if strings.TrimSpace(s) == "" {
			return Config{}, errors.New("config: INTERNAL_SERVICE_SECRETS must not contain empty entries")
		}
	}
	return cfg, nil
}
