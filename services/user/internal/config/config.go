// Package config declares the user service's environment contract.
package config

import libconfig "github.com/levonn-dev/vg-collect/libs/go/config"

// Config holds all environment-sourced configuration for the user service.
type Config struct {
	HTTPAddr    string `env:"HTTP_ADDR"           envDefault:":8080"`
	DatabaseURL string `env:"DATABASE_URL,required"`
	JWKSURL     string `env:"JWKS_URL,required"`
	JWTIssuer   string `env:"JWT_ISSUER"          envDefault:"vg-collect-auth"`
	JWTAudience string `env:"JWT_AUDIENCE"        envDefault:"vg-collect"`
	Version     string `env:"SERVICE_VERSION"     envDefault:"dev"`
}

// Load parses environment variables into a Config using struct tags.
func Load() (Config, error) { return libconfig.Load[Config]() }
