// Package migrations embeds this service's schema migrations (JSON
// arrays of MongoDB runCommand documents) for db.Migrate (run via the
// `migrate` subcommand / init container).
package migrations

import "embed"

//go:embed *.json
var FS embed.FS
