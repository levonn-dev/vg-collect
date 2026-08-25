// Package migrations embeds this service's schema migrations (JSON
// runCommand arrays) for mongokit.Migrate, run via the `migrate` subcommand.
package migrations

import "embed"

//go:embed *.json
var FS embed.FS
