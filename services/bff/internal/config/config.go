// Package config declares the bff service's environment contract.
package config

import (
	"errors"
	"strings"
	"time"

	libconfig "github.com/levonn-dev/vg-collect/libs/go/config"
)

// Config holds all environment-sourced configuration for the bff.
type Config struct {
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`

	// base64 (std) 32-byte AES-256 key sealing the session cookie.
	CookieKey string `env:"COOKIE_KEY,required"`
	// Secure stays on even in dev: browsers treat http://localhost as a
	// trustworthy origin, so Secure cookies work over the port-forward.
	CookieSecure bool `env:"COOKIE_SECURE" envDefault:"true"`

	// Origins allowed to send mutating requests (the gateway origin and
	// the Vite dev server origin in dev).
	PublicOrigins []string `env:"PUBLIC_ORIGINS,required,notEmpty" envSeparator:","`

	AuthServiceURL       string `env:"AUTH_SERVICE_URL,required"`
	UserServiceURL       string `env:"USER_SERVICE_URL,required"`
	EnrichmentServiceURL string `env:"ENRICHMENT_SERVICE_URL,required"`
	CollectionServiceURL string `env:"COLLECTION_SERVICE_URL,required"`
	SocialServiceURL     string `env:"SOCIAL_SERVICE_URL,required"`

	// OTLP/HTTP base URL of the collector agent for relayed browser
	// telemetry. Empty disables the relay (payloads are accepted and
	// dropped): telemetry must never break the app.
	OTLPProxyURL string `env:"OTLP_PROXY_URL"`

	ValkeyURL string `env:"VALKEY_URL,required"`
	// CA bundle for rediss:// against the in-cluster CA-issued cert.
	ValkeyCAFile string `env:"VALKEY_CA_FILE"`

	// Must match the auth service's ACCESS_TOKEN_TTL: it bounds how long
	// a revoked-chain jti can stay alive, i.e. the denylist entry TTL.
	AccessTokenTTL time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"5m"`
	// Refresh starts when the access token has less than this left.
	RefreshWindow time.Duration `env:"REFRESH_WINDOW" envDefault:"30s"`
	MeCacheTTL    time.Duration `env:"ME_CACHE_TTL" envDefault:"45s"`
	RecsCacheTTL  time.Duration `env:"RECS_CACHE_TTL" envDefault:"1h"`

	// Serve the embedded SPA bundle (on in-cluster; off when the Vite
	// dev server owns the frontend).
	ServeStatic bool `env:"SERVE_STATIC" envDefault:"false"`

	Version string `env:"SERVICE_VERSION" envDefault:"dev"`
}

// Load parses environment variables and enforces cross-field rules.
func Load() (Config, error) {
	cfg, err := libconfig.Load[Config]()
	if err != nil {
		return Config{}, err
	}
	if strings.HasPrefix(cfg.ValkeyURL, "rediss://") && cfg.ValkeyCAFile == "" {
		return Config{}, errors.New("config: VALKEY_CA_FILE is required for a rediss:// VALKEY_URL")
	}
	return cfg, nil
}
