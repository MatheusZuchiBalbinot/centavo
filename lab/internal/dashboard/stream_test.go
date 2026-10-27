package dashboard_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"centavo/lab/internal/audit"
	"centavo/lab/internal/dashboard"
)

// The stream reads the round once a second.
const messageWithin = 5 * time.Second

type streamMessage struct {
	name string
	data string
}

type streamedRound struct {
	Round   string `json:"round"`
	Verdict string `json:"verdict"`
	Events  []struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
	} `json:"events"`
}

func openStream(t *testing.T, serverURL, round string) <-chan streamMessage {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, serverURL+"/api/rounds/"+round+"/events", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	// readMessages closes the body when the stream ends; this covers a test
	// that stops reading first.
	t.Cleanup(func() { _ = response.Body.Close() })

	isStream := response.StatusCode == http.StatusOK && response.Header.Get("Content-Type") == "text/event-stream"
	if !isStream {
		t.Fatalf("stream answered %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}

	messages := make(chan streamMessage)

	go readMessages(response, messages)

	return messages
}

func readMessages(response *http.Response, messages chan<- streamMessage) {
	defer close(messages)
	defer response.Body.Close()

	scanner := bufio.NewScanner(response.Body)

	var message streamMessage

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			message.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			message.data = strings.TrimPrefix(line, "data: ")
		case line == "" && message.name != "":
			messages <- message
			message = streamMessage{}
		}
	}
}

func nextMessage(t *testing.T, messages <-chan streamMessage) streamMessage {
	t.Helper()

	select {
	case message, isOpen := <-messages:
		if !isOpen {
			t.Fatal("the stream ended early")
		}

		return message
	case <-time.After(messageWithin):
		t.Fatal("no message from the stream")
	}

	return streamMessage{}
}

func nextMessageNamed(t *testing.T, messages <-chan streamMessage, name string) streamMessage {
	t.Helper()

	for {
		message := nextMessage(t, messages)
		if message.name == name {
			return message
		}
	}
}

func TestEvents_StreamsTheRoundFromSnapshotToVerdict(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	writeLiveRound(t, roundsDir, "r0001", "")

	readAt := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	live := func() (audit.Reading, bool) { return audit.Reading{At: readAt, SumCents: 0}, true }
	server := httptest.NewServer(dashboard.NewHandler(dashboard.Config{Live: live, RoundsDir: roundsDir}))
	t.Cleanup(server.Close)

	messages := openStream(t, server.URL, "r0001")

	snapshot := nextMessage(t, messages)
	if snapshot.name != "snapshot" {
		t.Fatalf("first message %q, want snapshot", snapshot.name)
	}

	var round streamedRound

	err := json.Unmarshal([]byte(snapshot.data), &round)
	if err != nil {
		t.Fatal(err)
	}

	isSnapshot := round.Round == "r0001" && len(round.Events) == 1 && round.Events[0].Kind == "kill" && round.Events[0].Target == "bank-api"
	if !isSnapshot {
		t.Fatalf("snapshot: %+v", round)
	}

	auditor := nextMessage(t, messages)
	if auditor.name != "auditor" {
		t.Fatalf("second message %q, want the auditor reading", auditor.name)
	}

	dir := filepath.Join(roundsDir, "r0001")
	appendTo(t, filepath.Join(dir, "history.jsonl"), `{"index":3,"round":"r0001","time_ns":2100000000,"wall":"x","process":"nemesis","type":"info","f":"heal_start"}`+"\n")

	event := nextMessageNamed(t, messages, "event")
	if !strings.Contains(event.data, `"kind":"heal_start"`) {
		t.Fatalf("event: %s", event.data)
	}

	writeRound(t, roundsDir, "r0001", `{"verdict":"pass","invariants":[]}`)

	err = os.WriteFile(filepath.Join(dir, "round.json"), []byte(`{"round":"r0001","scenario":"smoke","seed":"0x5eed","verdict":"pass"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	finished := nextMessageNamed(t, messages, "finished")

	err = json.Unmarshal([]byte(finished.data), &round)
	if err != nil {
		t.Fatal(err)
	}

	if round.Verdict != "pass" {
		t.Fatalf("finished: %+v", round)
	}

	select {
	case _, isOpen := <-messages:
		if isOpen {
			t.Fatal("the stream sent more after finished")
		}
	case <-time.After(messageWithin):
		t.Fatal("the stream stayed open after finished")
	}
}

func TestEvents_ServesOnlyExistingRounds(t *testing.T) {
	t.Parallel()

	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: t.TempDir()})

	for _, round := range []string{"r0009", "..%2Fetc"} {
		code := get(t, handler, "/api/rounds/"+round+"/events", nil)
		if code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", round, code)
		}
	}
}
