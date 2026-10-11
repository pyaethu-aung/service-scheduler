// Package migrations embeds the goose SQL migrations so tests and the server
// can apply or inspect them without the goose CLI.
package migrations

import "embed"

// FS holds every migration file in version order.
//
//go:embed *.sql
var FS embed.FS
