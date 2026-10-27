package migrations

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
)

//go:embed base/*.sql defenses/*/*.sql
var files embed.FS

const defensesDir = "defenses"

func Base() (fs.FS, error) {
	return fs.Sub(files, "base")
}

func Defense(name string) (fs.FS, bool, error) {
	dir := defensesDir + "/" + name

	_, err := fs.Stat(files, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("reading the %s migrations: %w", name, err)
	}

	defenseFiles, err := fs.Sub(files, dir)

	return defenseFiles, true, err
}
