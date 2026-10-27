// Rounds carry histories and ledger exports, so only the lab's own user
// reads them.
package fsmode

import "io/fs"

const (
	PrivateFile fs.FileMode = 0o600
	PrivateDir  fs.FileMode = 0o750
)
