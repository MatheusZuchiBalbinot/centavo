//go:build chaoslab

package chaosbuild

// sentinel is searched for by scripts/check-production-binary.sh: it must
// appear in chaoslab binaries and never in production ones.
const sentinel = "centavo:chaoslab-build"

// IsEnabled reports whether this is a chaoslab build.
func IsEnabled() bool {
	return true
}

// Sentinel returns the marker string compiled only into chaoslab builds.
func Sentinel() string {
	return sentinel
}
