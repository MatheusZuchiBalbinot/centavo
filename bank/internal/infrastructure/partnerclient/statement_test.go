package partnerclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The reconciler's failpoints match the PIX's correlation id, which only the
// statement brings.
func TestStatement_ReadsEachEntrysCorrelationID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"entries":[{"seq":7,"kind":"settlement","end_to_end_id":"E1","amount_cents":500,"correlation_id":"r0001-p01-000001"}]}`))
	}))
	defer server.Close()

	entries, err := New(server.URL, "centavo-robust").Statement(t.Context(), 0, 10)

	isRead := err == nil && len(entries) == 1 && entries[0].Seq == 7 && entries[0].CorrelationID == "r0001-p01-000001"
	if !isRead {
		t.Fatalf("entries %+v, %v", entries, err)
	}
}
