//go:build !chaoslab

// Package chaosbuild tells whether the binary was compiled with the chaoslab
// build tag, which is the only way chaos code ends up in a binary.
package chaosbuild

// IsEnabled reports whether this is a chaoslab build.
func IsEnabled() bool {
	return false
}

// Sentinel returns the marker string compiled only into chaoslab builds.
func Sentinel() string {
	return ""
}
