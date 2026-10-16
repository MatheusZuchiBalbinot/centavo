// Package idempotency holds the identity of a client's logical operation.
package idempotency

import (
	"crypto/sha256"
	"errors"
	"fmt"
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

// Status is where a key stands (docs/domain.md, section 13.4).
type Status string

const (
	// StatusPending exists only inside the transaction that owns the key.
	StatusPending   Status = "pending"
	StatusCompleted Status = "completed"
	// StatusVoided is a tombstone left by resolve: the key never applies.
	StatusVoided Status = "voided"
)

// Outcome is what resolve says about an operation whose result the client
// does not know.
type Outcome string

const (
	OutcomeApplied Outcome = "applied"
	OutcomeVoided  Outcome = "voided"
)

var (
	ErrKeyMismatch       = errors.New("the Idempotency-Key was already used with another request")
	ErrOperationVoided   = errors.New("the operation was voided by resolve; nothing will be applied for this key")
	ErrRequestInProgress = errors.New("another request with the same Idempotency-Key is still running")
)

// RequestHash binds a key to the exact request it was first used with.
type RequestHash [sha256.Size]byte

// HashRequest hashes the endpoint and the raw body; a retry sends the same bytes.
func HashRequest(endpoint string, body []byte) RequestHash {
	hasher := sha256.New()

	// Writing to an in-memory hash never fails.
	_, _ = hasher.Write([]byte(endpoint))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(body)

	var hash RequestHash
	copy(hash[:], hasher.Sum(nil))

	return hash
}

// StoredResponse is the answer a completed key gives to every retry.
type StoredResponse struct {
	Status int
	Body   []byte
}

// Record is a key as stored.
type Record struct {
	Key         Key
	Endpoint    string
	RequestHash RequestHash
	Status      Status
	Response    StoredResponse
}

// Replay decides what a request gets when its key already exists.
func (r Record) Replay(endpoint string, hash RequestHash) (StoredResponse, error) {
	switch r.Status {
	case StatusVoided:
		return StoredResponse{}, ErrOperationVoided
	case StatusPending:
		return StoredResponse{}, ErrRequestInProgress
	case StatusCompleted:
		isSameRequest := r.Endpoint == endpoint && r.RequestHash == hash
		if !isSameRequest {
			return StoredResponse{}, ErrKeyMismatch
		}

		return r.Response, nil
	default:
		return StoredResponse{}, fmt.Errorf("idempotency key %s has unknown status %q", r.Key, r.Status)
	}
}
