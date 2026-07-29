// Package config declares the collection service's environment
// contract.
package config

import (
	"errors"
	"strings"
	"time"

	libconfig "github.com/levonn-dev/vgkeep/libs/go/config"
)

// Config holds all environment-sourced configuration for the
// collection service.
type Config struct {
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`

	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`

	ValkeyURL string `env:"VALKEY_URL,required,notEmpty"`
	// CA bundle for rediss:// against the in-cluster CA-issued cert.
	ValkeyCAFile string `env:"VALKEY_CA_FILE"`

	JWKSURL     string `env:"JWKS_URL,required,notEmpty"`
	JWTIssuer   string `env:"JWT_ISSUER"   envDefault:"vgkeep-auth"`
	JWTAudience string `env:"JWT_AUDIENCE" envDefault:"vgkeep"`

	// The enrichment service base URL (product snapshots at entry
	// creation, batch prices for value composition).
	EnrichmentServiceURL string `env:"ENRICHMENT_SERVICE_URL,required,notEmpty"`

	// How long a composed dashboard stays cached (the owner's own
	// mutations invalidate it early).
	DashboardCacheTTL time.Duration `env:"DASHBOARD_CACHE_TTL" envDefault:"5m"`

	Version string `env:"SERVICE_VERSION" envDefault:"dev"`
}

// Load parses environment variables into a Config and enforces the
// cross-field rules the tags cannot express.
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
