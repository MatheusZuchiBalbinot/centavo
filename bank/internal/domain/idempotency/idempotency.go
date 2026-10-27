package idempotency

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
)

// keyFormat matches the idempotency_key_format constraint. Short keys or odd
// characters usually mean a buggy client.
var keyFormat = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

var (
	ErrKeyMissing = errors.New("the Idempotency-Key header is required")
	ErrKeyInvalid = errors.New("the Idempotency-Key header must have 8 to 128 characters among letters, digits and . _ : -")
)

const (
	// StatusPending exists only inside the transaction that owns the key.
	StatusPending   Status = "pending"
	StatusCompleted Status = "completed"
	// StatusVoided is a tombstone left by resolve: the key never applies.
	StatusVoided Status = "voided"
)

const (
	OutcomeApplied Outcome = "applied"
	OutcomeVoided  Outcome = "voided"
	// OutcomeSuperseded is a key another request took: the one asking was
	// never applied, and never will be, since the key is no longer free.
	OutcomeSuperseded Outcome = "superseded"
)

var (
	ErrKeyMismatch       = errors.New("the Idempotency-Key was already used with another request")
	ErrOperationVoided   = errors.New("the operation was voided by resolve; nothing will be applied for this key")
	ErrRequestInProgress = errors.New("another request with the same Idempotency-Key is still running")
)

type Key string

type Status string

type Outcome string

// Binds a key to the exact request it was first used with.
type RequestHash [sha256.Size]byte

type StoredResponse struct {
	Status int
	Body   []byte
}

type Record struct {
	Key         Key
	Endpoint    string
	RequestHash RequestHash
	Status      Status
	Response    StoredResponse
}

func ParseKey(raw string) (Key, error) {
	if raw == "" {
		return "", ErrKeyMissing
	}

	if !keyFormat.MatchString(raw) {
		return "", ErrKeyInvalid
	}

	return Key(raw), nil
}

// A NUL separates the endpoint from the body, which is unambiguous because
// endpoints are the server's own paths and never contain one; the client
// only controls the body.
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
