// Package idempotency holds the identity of a client's logical operation.
package idempotency

import (
	"errors"
	"regexp"
)

// Key is the client-generated identity of one logical operation; retries
// reuse it (BR06).
type Key string

// keyFormat matches the idempotency_key_format constraint of docs/domain.md,
// section 13.4. Short keys or odd characters usually mean a buggy client.
var keyFormat = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

var (
	ErrKeyMissing = errors.New("the Idempotency-Key header is required")
	ErrKeyInvalid = errors.New("the Idempotency-Key header must have 8 to 128 characters among letters, digits and . _ : -")
)

// ParseKey validates a key received from a client.
func ParseKey(raw string) (Key, error) {
	if raw == "" {
		return "", ErrKeyMissing
	}

	if !keyFormat.MatchString(raw) {
		return "", ErrKeyInvalid
	}

	return Key(raw), nil
}
