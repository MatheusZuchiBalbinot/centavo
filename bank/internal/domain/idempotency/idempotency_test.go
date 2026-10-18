package idempotency_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"centavo/bank/internal/domain/idempotency"
)

func TestParseKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want error
	}{
		{name: "operation id from the lab", raw: "r0142-p07-000318", want: nil},
		{name: "all allowed characters", raw: "Aa0._:-z", want: nil},
		{name: "at the upper limit", raw: strings.Repeat("k", 128), want: nil},
		{name: "missing", raw: "", want: idempotency.ErrKeyMissing},
		{name: "too short", raw: "short", want: idempotency.ErrKeyInvalid},
		{name: "too long", raw: strings.Repeat("k", 129), want: idempotency.ErrKeyInvalid},
		{name: "space", raw: "has space in it", want: idempotency.ErrKeyInvalid},
		{name: "newline injection", raw: "r0142-p07\n000318", want: idempotency.ErrKeyInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := idempotency.ParseKey(tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}

			isAccepted := tc.want == nil
			if isAccepted && string(key) != tc.raw {
				t.Fatalf("key %q came back as %q", tc.raw, key)
			}
		})
	}
}

func TestReplay(t *testing.T) {
	t.Parallel()

	hash := idempotency.HashRequest("/v1/transfers", []byte(`{"amount_cents": 10}`))
	completed := idempotency.Record{Key: "r0001-p01-000001", Endpoint: "/v1/transfers", RequestHash: hash, Status: idempotency.StatusCompleted, Response: idempotency.StoredResponse{Status: 201, Body: []byte(`{"ok":1}`)}}

	response, err := completed.Replay("/v1/transfers", hash)
	if err != nil || response.Status != 201 || string(response.Body) != `{"ok":1}` {
		t.Fatalf("same request: %+v, %v", response, err)
	}

	otherBody := idempotency.HashRequest("/v1/transfers", []byte(`{"amount_cents": 11}`))

	cases := []struct {
		name     string
		record   idempotency.Record
		endpoint string
		hash     idempotency.RequestHash
		want     error
	}{
		{name: "other body", record: completed, endpoint: "/v1/transfers", hash: otherBody, want: idempotency.ErrKeyMismatch},
		{name: "other endpoint", record: completed, endpoint: "/v1/deposits", hash: hash, want: idempotency.ErrKeyMismatch},
		{name: "tombstone", record: idempotency.Record{Status: idempotency.StatusVoided}, endpoint: "/v1/transfers", hash: hash, want: idempotency.ErrOperationVoided},
		{name: "pending", record: idempotency.Record{Status: idempotency.StatusPending}, endpoint: "/v1/transfers", hash: hash, want: idempotency.ErrRequestInProgress},
	}

	for _, tc := range cases {
		_, err := tc.record.Replay(tc.endpoint, tc.hash)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestHashRequest_SeparatesEndpointFromBody(t *testing.T) {
	t.Parallel()

	first := idempotency.HashRequest("/v1/a", []byte("b/c"))
	second := idempotency.HashRequest("/v1/ab", []byte("/c"))

	if first == second {
		t.Fatal("endpoint and body run together in the hash")
	}
}

// FuzzParseKey: an accepted key is the input unchanged, and anything refused
// gets one of the two documented errors.
func FuzzParseKey(f *testing.F) {
	for _, seed := range []string{"", "short", "r0001-p01-000001", "key with spaces", strings.Repeat("a", 129)} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		key, err := idempotency.ParseKey(raw)
		if err == nil {
			if string(key) != raw {
				t.Fatalf("accepted %q as %q", raw, key)
			}

			return
		}

		isDocumented := errors.Is(err, idempotency.ErrKeyMissing) || errors.Is(err, idempotency.ErrKeyInvalid)
		if !isDocumented {
			t.Fatalf("refused %q with %v", raw, err)
		}
	})
}

// FuzzHashRequest: the hash binds the endpoint and the body apart, so moving
// bytes from one to the other changes it. Endpoints are the server's paths
// and never hold a NUL, the separator; with one, the split is ambiguous.
func FuzzHashRequest(f *testing.F) {
	f.Add("/v1/deposits", []byte(`{"amount_cents":100}`))
	f.Add("", []byte{})

	f.Fuzz(func(t *testing.T, endpoint string, body []byte) {
		if strings.Contains(endpoint, "\x00") {
			return
		}

		first, again := idempotency.HashRequest(endpoint, body), idempotency.HashRequest(endpoint, slices.Clone(body))
		if first != again {
			t.Fatal("the same request hashed twice gave two hashes")
		}

		moved := append([]byte(endpoint[len(endpoint)/2:]), body...)
		isSameSplit := endpoint[len(endpoint)/2:] == ""
		if isSameSplit {
			return
		}

		if idempotency.HashRequest(endpoint[:len(endpoint)/2], moved) == idempotency.HashRequest(endpoint, body) {
			t.Fatalf("moving %q from the endpoint to the body kept the hash", endpoint[len(endpoint)/2:])
		}
	})
}
