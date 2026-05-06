// Package migrations embeds the SQL migration files at the repo
// root. Code that drives golang-migrate (the migrate subcommand,
// the schema-version check on serve, the integration test) imports
// this FS rather than reading from disk.
package migrations

import "embed"

// FS embeds every entry under migrations/. The `all:*` pattern keeps
// the .gitkeep placeholder discoverable so the embed compiles even
// when there are no SQL files yet (before bd #1 lands); golang-
// migrate ignores non-NNNN_*.{up,down}.sql entries.
//
//go:embed all:*
var FS embed.FS
