// Package migrations embeds the SQL migrations into the bank binary.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed base/*.sql
var files embed.FS

// Base returns the base schema migrations, shared by every defense profile.
func Base() (fs.FS, error) {
	return fs.Sub(files, "base")
}
