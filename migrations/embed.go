// Package migrations holds the SQL schema migrations, embedded into the binary
// so a deployed image always carries the schema it expects.
package migrations

import "embed"

// FS contains every *.sql migration in this directory, applied in version order by goose.
//
//go:embed *.sql
var FS embed.FS
