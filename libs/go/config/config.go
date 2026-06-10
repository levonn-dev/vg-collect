// Package config loads service configuration from environment variables
// using struct tags (env, envDefault, required).
package config

import env "github.com/caarlos0/env/v11"

// Load parses environment variables into a new T using its env struct tags.
func Load[T any]() (T, error) {
	var cfg T
	err := env.Parse(&cfg)
	return cfg, err
}
