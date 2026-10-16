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
		name   string
		header string
		body   []byte
		secret []byte
		want   bool
	}{
		{name: "valid", header: sign(secret, now, body), body: body, secret: secret, want: true},
		{name: "four minutes old", header: sign(secret, now.Add(-4*time.Minute), body), body: body, secret: secret, want: true},
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
		if got != tc.want {
			t.Errorf("%s: got %t, want %t", tc.name, got, tc.want)
		}
	}
}
