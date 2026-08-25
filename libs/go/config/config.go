// Package config loads service configuration from environment variables
// using struct tags (env, envDefault, required).
package config

import (
	"fmt"

	env "github.com/caarlos0/env/v11"
)

// Load parses environment variables into a new T using its env struct tags.
func Load[T any]() (T, error) {
	var cfg T
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
