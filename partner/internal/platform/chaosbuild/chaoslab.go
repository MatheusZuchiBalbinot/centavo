//go:build chaoslab

package chaosbuild

// sentinel is searched for by scripts/check-production-binary.sh: it must
// appear in chaoslab binaries and never in production ones.
const sentinel = "centavo:chaoslab-build"

func IsEnabled() bool {
	return true
}

func Sentinel() string {
	return sentinel
}
