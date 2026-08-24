// Package config declares the enrichment service's environment
// contract.
package config

import (
	"errors"
	"fmt"
	"time"

	libconfig "github.com/levonn-dev/vgkeep/libs/go/config"
)

// Config holds all environment-sourced configuration for the
// enrichment service.
type Config struct {
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`

	// Mongo connection URL (TLS via tls=true&tlsCAFile=... params) and
	// database name.
	MongoURL string `env:"MONGO_URL,required,notEmpty"`
	MongoDB  string `env:"MONGO_DB"  envDefault:"enrichment"`
	// Optional credential pair composed into MongoURL's userinfo (see
	// mongokit.ComposeURL) rather than arriving pre-embedded. Must be
	// both set or both empty; when empty, MongoURL is used exactly as
	// given, which may itself already carry inline credentials.
	MongoUsername string `env:"MONGO_USERNAME"`
	MongoPassword string `env:"MONGO_PASSWORD"`

	ValkeyURL string `env:"VALKEY_URL,required,notEmpty"`
	// CA bundle for rediss:// against the in-cluster CA-issued cert.
	ValkeyCAFile string `env:"VALKEY_CA_FILE"`

	JWKSURL     string `env:"JWKS_URL,required,notEmpty"`
	JWTIssuer   string `env:"JWT_ISSUER"   envDefault:"vgkeep-auth"`
	JWTAudience string `env:"JWT_AUDIENCE" envDefault:"vgkeep"`

	// Provider switches: stub serves embedded fixtures (credential-less
	// dev/e2e); real needs the credentials below.
	IGDBMode            string `env:"IGDB_MODE"            envDefault:"stub"`
	IGDBClientID        string `env:"IGDB_CLIENT_ID"`
	IGDBClientSecret    string `env:"IGDB_CLIENT_SECRET"`
	PriceChartingMode   string `env:"PRICECHARTING_MODE"   envDefault:"stub"`
	PriceChartingAPIKey string `env:"PRICECHARTING_API_KEY"`
	FXMode              string `env:"FX_MODE"              envDefault:"stub"`

	// Read-pattern tunables: search query cache, product read cache,
	// and the IGDB projection staleness horizon.
	SearchCacheTTL   time.Duration `env:"SEARCH_CACHE_TTL"   envDefault:"24h"`
	ProductCacheTTL  time.Duration `env:"PRODUCT_CACHE_TTL"  envDefault:"5m"`
	IGDBRefreshAfter time.Duration `env:"IGDB_REFRESH_AFTER" envDefault:"720h"`

	Version string `env:"SERVICE_VERSION" envDefault:"dev"`
}

// Load parses environment variables into a Config and enforces the
// cross-field rules the tags cannot express.
func Load() (Config, error) {
	cfg, err := libconfig.Load[Config]()
	if err != nil {
		return Config{}, err
	}
	if (cfg.MongoUsername == "") != (cfg.MongoPassword == "") {
		return Config{}, errors.New("config: MONGO_USERNAME and MONGO_PASSWORD must both be set or both be empty")
	}
	switch cfg.IGDBMode {
	case "stub":
	case "real":
		if cfg.IGDBClientID == "" || cfg.IGDBClientSecret == "" {
			return Config{}, errors.New("config: IGDB_MODE=real requires IGDB_CLIENT_ID and IGDB_CLIENT_SECRET")
		}
	default:
		return Config{}, fmt.Errorf("config: IGDB_MODE must be stub or real, got %q", cfg.IGDBMode)
	}
	switch cfg.PriceChartingMode {
	case "stub":
	case "real":
		if cfg.PriceChartingAPIKey == "" {
			return Config{}, errors.New("config: PRICECHARTING_MODE=real requires PRICECHARTING_API_KEY")
		}
	default:
		return Config{}, fmt.Errorf("config: PRICECHARTING_MODE must be stub or real, got %q", cfg.PriceChartingMode)
	}
	switch cfg.FXMode {
	case "stub", "real":
	default:
		return Config{}, fmt.Errorf("config: FX_MODE must be stub or real, got %q", cfg.FXMode)
	}
	if err := libconfig.RequireCAForRediss(cfg.ValkeyURL, cfg.ValkeyCAFile); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
