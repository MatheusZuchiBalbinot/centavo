//go:build !chaoslab

// Package chaosbuild tells whether the binary was compiled with the chaoslab
// build tag, which is the only way chaos code ends up in a binary.
package chaosbuild

func IsEnabled() bool {
	return false
}

func Sentinel() string {
	return ""
}
