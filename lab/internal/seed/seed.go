// Package seed derives the independent random streams of a round from one
// 64-bit seed (docs/chaos-bot.md, section 15.1).
package seed

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	mathrand "math/rand/v2"
	"strconv"
	"strings"
)

// Seed is the 64-bit seed of a round.
type Seed uint64

// ErrInvalidSeed means the text is not a 64-bit number.
var ErrInvalidSeed = errors.New("seed must be a 64-bit number, such as 0x5eed0142")

// Parse reads a seed in hexadecimal (0x...) or decimal form.
func Parse(raw string) (Seed, error) {
	digits, isHex := strings.CutPrefix(strings.ToLower(strings.TrimSpace(raw)), "0x")

	base := 10
	if isHex {
		base = 16
	}

	value, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: got %q", ErrInvalidSeed, raw)
	}

	return Seed(value), nil
}

// Random draws a fresh seed for a round started without one.
func Random() (Seed, error) {
	var buffer [8]byte

	_, err := rand.Read(buffer[:])
	if err != nil {
		return 0, fmt.Errorf("drawing a seed: %w", err)
	}

	return Seed(binary.LittleEndian.Uint64(buffer[:])), nil
}

// String prints the seed in the form Parse accepts and reports show.
func (s Seed) String() string {
	return fmt.Sprintf("%#x", uint64(s))
}

// Stream returns the random stream named name. Streams are independent: adding
// a new one never changes the sequence of the others.
func (s Seed) Stream(name string) *mathrand.Rand {
	derived := splitmix64(uint64(s) ^ hashName(name))

	// A deterministic generator is the point: the same seed must replay the same round.
	return mathrand.New(mathrand.NewPCG(derived, splitmix64(derived))) //nolint:gosec // G404: reproducibility, not secrecy.
}

func hashName(name string) uint64 {
	hasher := fnv.New64a()

	// Writing to an in-memory hash never fails.
	_, _ = hasher.Write([]byte(name))

	return hasher.Sum64()
}

// splitmix64 spreads nearby inputs over the whole 64-bit range.
func splitmix64(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb

	return value ^ (value >> 31)
}
