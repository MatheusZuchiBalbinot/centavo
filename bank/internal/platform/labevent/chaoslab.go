//go:build chaoslab

package labevent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"centavo/bank/internal/platform/correlation"
)

// Defense events go next to the failpoint events, in their own files: the
// lab reads failpoints from *.jsonl and defenses from *.defenses.ndjson.
const (
	fileSuffix = ".defenses.ndjson"
	fileMode   = 0o644
)

type Config struct {
	Dir      string
	Service  string
	Instance string
}

type event struct {
	TS            time.Time `json:"ts"`
	Service       string    `json:"service"`
	Instance      string    `json:"instance"`
	Defense       Defense   `json:"defense"`
	Detail        string    `json:"detail,omitempty"`
	Count         int       `json:"count"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}

var recorder struct {
	mu     sync.Mutex
	config Config
	file   *os.File
	// tokens is the last lease token this process recorded, per resource:
	// renewals keep the token, so only a new holding is told.
	tokens map[string]int64
}

// Until it is called none is recorded.
func Configure(config Config) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	recorder.config = config
}

func recordLease(ctx context.Context, resource string, token int64) {
	recorder.mu.Lock()

	if recorder.tokens == nil {
		recorder.tokens = map[string]int64{}
	}

	isRenewal := recorder.tokens[resource] == token
	recorder.tokens[resource] = token
	recorder.mu.Unlock()

	if !isRenewal {
		record(ctx, LeaseAcquired, resource, 1)
	}
}

// A failure to write is dropped: the defense acted either way, and the lab
// only loses the telling of it.
func record(ctx context.Context, defense Defense, detail string, count int) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	if recorder.config.Dir == "" {
		return
	}

	if recorder.file == nil {
		path := filepath.Join(recorder.config.Dir, recorder.config.Instance+fileSuffix)

		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, fileMode) //nolint:gosec // G304: the path comes from the process configuration.
		if err != nil {
			return
		}

		recorder.file = file
	}

	entry := event{
		TS: time.Now().UTC(), Service: recorder.config.Service, Instance: recorder.config.Instance,
		Defense: defense, Detail: detail, Count: count, CorrelationID: correlation.From(ctx),
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}

	_, _ = recorder.file.Write(append(line, '\n'))
}
