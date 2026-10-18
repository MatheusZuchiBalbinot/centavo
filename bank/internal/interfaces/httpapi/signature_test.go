package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func sign(secret []byte, at time.Time, body []byte) string {
	timestamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)

	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"event_id":"evt_1","type":"transfer.settled"}`)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name         string
		header       string
		body         []byte
		secret       []byte
		shouldVerify bool
	}{
		{name: "valid", header: sign(secret, now, body), body: body, secret: secret, shouldVerify: true},
		{name: "four minutes old", header: sign(secret, now.Add(-4*time.Minute), body), body: body, secret: secret, shouldVerify: true},
		{name: "six minutes old", header: sign(secret, now.Add(-6*time.Minute), body), body: body, secret: secret},
		{name: "from the future", header: sign(secret, now.Add(6*time.Minute), body), body: body, secret: secret},
		{name: "another body", header: sign(secret, now, body), body: []byte(`{"event_id":"evt_2"}`), secret: secret},
		{name: "another secret", header: sign([]byte("another secret, also 32 chars.."), now, body), body: body, secret: secret},
		{name: "missing", header: "", body: body, secret: secret},
		{name: "garbage", header: "t=abc,v1=zz", body: body, secret: secret},
		{name: "no secret configured", header: sign(nil, now, body), body: body, secret: nil},
	}

	for _, tc := range cases {
		got := verifySignature(tc.header, tc.body, tc.secret, now)
		if got != tc.shouldVerify {
			t.Errorf("%s: got %t, want %t", tc.name, got, tc.shouldVerify)
		}
	}
}

// FuzzVerifySignature: whatever the header and body, the check never panics;
// a header the partner signed always verifies, and any other body never does.
func FuzzVerifySignature(f *testing.F) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	f.Add("", []byte(`{}`))
	f.Add("t=abc,v1=zz", []byte(`{"event_id":"evt_1"}`))
	f.Add(sign(secret, now, []byte(`{"event_id":"evt_1"}`)), []byte(`{"event_id":"evt_1"}`))
	f.Add("1760616000,v1=00", []byte("x"))

	f.Fuzz(func(t *testing.T, header string, body []byte) {
		_ = verifySignature(header, body, secret, now)

		signed := sign(secret, now, body)
		if !verifySignature(signed, body, secret, now) {
			t.Fatalf("a header signed for the body did not verify: %q", signed)
		}

		other := append([]byte{'!'}, body...)
		if verifySignature(signed, other, secret, now) {
			t.Fatalf("the signature of %q verified another body", body)
		}
	})
}
