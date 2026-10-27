//go:build !chaoslab

// Package chaosbuild tells whether the binary has the chaoslab build tag,
// the only way chaos code ends up in a binary.
package chaosbuild

func IsEnabled() bool {
	return false
}

func Sentinel() string {
	return ""
}
