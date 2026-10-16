// Package migrations embeds the SQL migrations into the bank binary.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed base/*.sql defenses/*/*.sql
var files embed.FS

const defensesDir = "defenses"

// Base returns the base schema migrations, shared by every defense profile.
func Base() (fs.FS, error) {
	return fs.Sub(files, "base")
}

// Defense returns the migrations that belong to one defense, and false when
// the defense has no constraints of its own.
func Defense(name string) (fs.FS, bool, error) {
	dir := defensesDir + "/" + name

	_, err := fs.Stat(files, dir)
	if err != nil {
		return nil, false, nil
	}

	defenseFiles, err := fs.Sub(files, dir)

	return defenseFiles, true, err
}
