package defenses

import (
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoNaiveVariant means the binary has no naive variant of a defense: the
// production build never does (lock 1 of docs/chaos-bot.md, section 16).
var ErrNoNaiveVariant = errors.New("this binary has no naive variant of the defense")

// Dependencies are what a naive variant is built from.
type Dependencies struct {
	Pool *pgxpool.Pool
}

// naiveVariants is filled by platform/naive's init, in chaoslab builds only.
var naiveVariants = struct {
	mu     sync.Mutex
	builds map[Defense]func(Dependencies) any
}{builds: map[Defense]func(Dependencies) any{}}

// RegisterNaive makes the naive variant of a defense available.
func RegisterNaive(defense Defense, build func(Dependencies) any) {
	naiveVariants.mu.Lock()
	defer naiveVariants.mu.Unlock()

	naiveVariants.builds[defense] = build
}

// Naive builds the naive variant of a defense as T.
func Naive[T any](defense Defense, deps Dependencies) (T, error) {
	var zero T

	naiveVariants.mu.Lock()
	build, isRegistered := naiveVariants.builds[defense]
	naiveVariants.mu.Unlock()

	if !isRegistered {
		return zero, fmt.Errorf("%w: %s", ErrNoNaiveVariant, defense)
	}

	variant, isRightType := build(deps).(T)
	if !isRightType {
		return zero, fmt.Errorf("naive variant of %s does not implement %T", defense, zero)
	}

	return variant, nil
}
