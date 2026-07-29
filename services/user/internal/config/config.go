// Package config declares the user service's environment contract.
package config

import (
	"time"

	libconfig "github.com/levonn-dev/vgkeep/libs/go/config"
)

// Config holds all environment-sourced configuration for the user service.
type Config struct {
	HTTPAddr    string `env:"HTTP_ADDR"           envDefault:":8080"`
	DatabaseURL string `env:"DATABASE_URL,required"`
	JWKSURL     string `env:"JWKS_URL,required"`
	JWTIssuer   string `env:"JWT_ISSUER"          envDefault:"vgkeep-auth"`
	JWTAudience string `env:"JWT_AUDIENCE"        envDefault:"vgkeep"`
	Version     string `env:"SERVICE_VERSION"     envDefault:"dev"`

	// Minimum interval between handle changes; the Tilt dev stack
	// overrides this to 5s so e2e can exercise the 429 live.
	HandleChangeCooldown time.Duration `env:"HANDLE_CHANGE_COOLDOWN" envDefault:"24h"`
}

// Load parses environment variables into a Config using struct tags.
func Load() (Config, error) { return libconfig.Load[Config]() }
