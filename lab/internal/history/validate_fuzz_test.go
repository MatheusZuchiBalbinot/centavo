package history_test

import (
	"bytes"
	"testing"

	"centavo/lab/internal/history"
)

// FuzzValidate: a history file is whatever the disk holds after a crash, so
// the validator must refuse anything malformed without panicking.
func FuzzValidate(f *testing.F) {
	f.Add([]byte(`{"index":1,"round":"r0001","time_ns":1,"process":1,"type":"invoke","f":"deposit","op":"r0001-p01-000001","value":{"account_id":"a","amount_cents":100}}` + "\n"))
	f.Add([]byte("{\"index\":1}\n{not json\n"))
	f.Add([]byte{})

	f.Fuzz(func(_ *testing.T, content []byte) {
		_, _ = history.Validate(bytes.NewReader(content))
	})
}
