// Package migrations carries the SQL files inside the Go binary, so the server needs no migration CLI.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
