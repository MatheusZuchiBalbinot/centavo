// Package fsmode holds the permissions of everything the lab writes: rounds
// carry histories and ledger exports, so only the lab's own user reads them.
package fsmode

import "io/fs"

// The permissions of the lab's files and directories.
const (
	PrivateFile fs.FileMode = 0o600
	PrivateDir  fs.FileMode = 0o750
)
