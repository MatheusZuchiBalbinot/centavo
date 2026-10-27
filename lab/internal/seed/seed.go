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

const (
	decimalBase = 10
	hexBase     = 16
)

const seedBytes = 8

// The constants of the splitmix64 finalizer (Steele, Lea and Flood, 2014):
// an odd increment and two multipliers with the shifts between them.
const (
	splitmixIncrement   = 0x9e3779b97f4a7c15
	splitmixMultiplier1 = 0xbf58476d1ce4e5b9
	splitmixMultiplier2 = 0x94d049bb133111eb
	splitmixShift1      = 30
	splitmixShift2      = 27
	splitmixShift3      = 31
)

var ErrInvalidSeed = errors.New("seed must be a 64-bit number, such as 0x5eed0142")

type Seed uint64

func Parse(raw string) (Seed, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	digits, isHex := strings.CutPrefix(normalized, "0x")

	base := decimalBase
	if isHex {
		base = hexBase
	}

	value, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: got %q", ErrInvalidSeed, raw)
	}

	return Seed(value), nil
}

func Random() (Seed, error) {
	var buffer [seedBytes]byte

	_, err := rand.Read(buffer[:])
	if err != nil {
		return 0, fmt.Errorf("drawing a seed: %w", err)
	}

	return Seed(binary.LittleEndian.Uint64(buffer[:])), nil
}

func (s Seed) String() string {
	return fmt.Sprintf("%#x", uint64(s))
}

// Streams are independent: adding a new one never changes the sequence of
// the others.
func (s Seed) Stream(name string) *mathrand.Rand {
	derived := splitmix64(uint64(s) ^ hashName(name))

	// A deterministic generator is the point: the same seed must replay the same round.
	source := mathrand.NewPCG(derived, splitmix64(derived))

	return mathrand.New(source) //nolint:gosec // G404: reproducibility, not secrecy.
}

func hashName(name string) uint64 {
	hasher := fnv.New64a()

	// Writing to an in-memory hash never fails.
	_, _ = hasher.Write([]byte(name))

	return hasher.Sum64()
}

func splitmix64(value uint64) uint64 {
	value += splitmixIncrement
	value = (value ^ (value >> splitmixShift1)) * splitmixMultiplier1
	value = (value ^ (value >> splitmixShift2)) * splitmixMultiplier2

	return value ^ (value >> splitmixShift3)
}
