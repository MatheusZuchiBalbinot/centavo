// Package buildinfo exposes the version stamped into the binary at build time.
package buildinfo

// version is set with -ldflags "-X centavo/partner/internal/platform/buildinfo.version=...".
var version = "dev"

// Version returns the build version.
func Version() string {
	return version
}
