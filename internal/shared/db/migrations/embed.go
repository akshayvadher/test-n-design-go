// Package migrations embeds the Bun-flavored SQL migration files so the binary
// carries its own schema. This is the in-process alternative to the atlas CLI:
// no external binary, no separate image, no shell — the distroless runtime can
// migrate itself.
//
// File naming follows bun's discovery rules: `<version>_<comment>.tx.up.sql`.
// The `.tx` infix tells bun to wrap the whole file in a transaction; `--bun:split`
// lines inside a file separate individual statements.
package migrations

import "embed"

// FS is the embedded migration set, handed to migrate.Migrations.Discover.
//
//go:embed *.up.sql
var FS embed.FS
