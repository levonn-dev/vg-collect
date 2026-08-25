package config

import (
	"errors"
	"strings"
)

// RequireCAForRediss enforces that a rediss:// Valkey URL carries a CA bundle path, since
// env-tag validation alone cannot express a rule spanning two fields. Plain redis:// (dev, no
// TLS) passes regardless of caFile.
func RequireCAForRediss(valkeyURL, caFile string) error {
	if strings.HasPrefix(valkeyURL, "rediss://") && caFile == "" {
		return errors.New("config: VALKEY_CA_FILE is required for a rediss:// VALKEY_URL")
	}
	return nil
}
