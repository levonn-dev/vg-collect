// Package migrations embeds this service's schema migrations for
// pgkit.Migrate (run via the `migrate` subcommand / init container).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
