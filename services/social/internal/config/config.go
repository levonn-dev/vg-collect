// Package config declares the social service's environment contract.
package config

import libconfig "github.com/levonn-dev/vgkeep/libs/go/config"

// Config holds all environment-sourced configuration for the social
// service.
type Config struct {
	HTTPAddr    string `env:"HTTP_ADDR" envDefault:":8080"`
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`

	JWKSURL     string `env:"JWKS_URL,required,notEmpty"`
	JWTIssuer   string `env:"JWT_ISSUER"   envDefault:"vgkeep-auth"`
	JWTAudience string `env:"JWT_AUDIENCE" envDefault:"vgkeep"`

	CollectionServiceURL string `env:"COLLECTION_SERVICE_URL,required,notEmpty"`
	UserServiceURL       string `env:"USER_SERVICE_URL,required,notEmpty"`

	// Community-size dials: rolling-24h caps per user. Comments count
	// tombstones (delete-repost spam still burns the cap).
	CapComments24h int `env:"SOCIAL_CAP_COMMENTS_24H" envDefault:"50"`
	CapFollows24h  int `env:"SOCIAL_CAP_FOLLOWS_24H"  envDefault:"100"`
	CapLikes24h    int `env:"SOCIAL_CAP_LIKES_24H"    envDefault:"200"`

	Version string `env:"SERVICE_VERSION" envDefault:"dev"`
}

// Load parses environment variables into a Config using struct tags.
func Load() (Config, error) { return libconfig.Load[Config]() }
