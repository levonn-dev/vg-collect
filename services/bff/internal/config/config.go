// Package config declares the bff service's environment contract.
package config

import (
	"time"

	libconfig "github.com/levonn-dev/vgkeep/libs/go/config"
)

// Config holds all environment-sourced configuration for the bff.
type Config struct {
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`

	// base64 (std) 32-byte AES-256 key sealing the session cookie.
	CookieKey string `env:"COOKIE_KEY,required,notEmpty"`
	// Secure stays on even in dev: localhost is a trustworthy origin for Secure cookies.
	CookieSecure bool `env:"COOKIE_SECURE" envDefault:"true"`

	// Origins allowed to send mutating requests (gateway origin, plus Vite dev server in dev).
	PublicOrigins []string `env:"PUBLIC_ORIGINS,required,notEmpty" envSeparator:","`

	AuthServiceURL       string `env:"AUTH_SERVICE_URL,required,notEmpty"`
	UserServiceURL       string `env:"USER_SERVICE_URL,required,notEmpty"`
	EnrichmentServiceURL string `env:"ENRICHMENT_SERVICE_URL,required,notEmpty"`
	CollectionServiceURL string `env:"COLLECTION_SERVICE_URL,required,notEmpty"`
	SocialServiceURL     string `env:"SOCIAL_SERVICE_URL,required,notEmpty"`

	// OTLP/HTTP base URL of the collector agent for relayed browser telemetry.
	// Empty disables the relay (accepted and dropped): telemetry must never break the app.
	OTLPProxyURL string `env:"OTLP_PROXY_URL"`

	ValkeyURL string `env:"VALKEY_URL,required,notEmpty"`
	// CA bundle for rediss:// against the in-cluster CA-issued cert.
	ValkeyCAFile string `env:"VALKEY_CA_FILE"`

	// Must match auth's ACCESS_TOKEN_TTL: bounds the denylist entry TTL for a revoked-chain jti.
	AccessTokenTTL time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"5m"`
	// Refresh starts when the access token has less than this left.
	RefreshWindow time.Duration `env:"REFRESH_WINDOW" envDefault:"30s"`
	MeCacheTTL    time.Duration `env:"ME_CACHE_TTL" envDefault:"45s"`
	RecsCacheTTL  time.Duration `env:"RECS_CACHE_TTL" envDefault:"1h"`

	// Serve the embedded SPA bundle (on in-cluster; off when Vite dev server owns the frontend).
	ServeStatic bool `env:"SERVE_STATIC" envDefault:"false"`

	Version string `env:"SERVICE_VERSION" envDefault:"dev"`
}

// Load parses environment variables and enforces cross-field rules.
func Load() (Config, error) {
	cfg, err := libconfig.Load[Config]()
	if err != nil {
		return Config{}, err
	}
	if err := libconfig.RequireCAForRediss(cfg.ValkeyURL, cfg.ValkeyCAFile); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
