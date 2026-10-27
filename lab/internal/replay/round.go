package replay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"centavo/lab/internal/checker"
	"centavo/lab/scenarios"
)

// codeNames are the failpoint names a scenario description cites between
// backticks: the panel says what happens, not where in the code.
var codeNames = regexp.MustCompile("\\s*\\([^)]*`[^)]*\\)")

const (
	HistoryFile  = "history.jsonl"
	manifestFile = "round.json"
	reportFile   = "report.json"
	File         = "replay.json"
	// maxLineBytes matches the checker's limit for one history record.
	maxLineBytes = 1 << 20
)

type Manifest struct {
	Round     string          `json:"round"`
	Scenario  string          `json:"scenario"`
	Seed      string          `json:"seed"`
	Verdict   checker.Verdict `json:"verdict"`
	Duration  string          `json:"duration"`
	StartedAt time.Time       `json:"started_at"`
	Defenses  string          `json:"defenses"`
	Stopped   bool            `json:"stopped"`
	Error     string          `json:"error"`
}

type report struct {
	Verdict    checker.Verdict `json:"verdict"`
	Invariants []Check         `json:"invariants"`
}

func ReadManifest(roundDir string) (Manifest, error) {
	content, err := os.ReadFile(filepath.Join(roundDir, manifestFile)) //nolint:gosec // G304: a round directory of the operator's rounds.
	if err != nil {
		return Manifest{}, fmt.Errorf("reading round.json: %w", err)
	}

	var manifest Manifest

	err = json.Unmarshal(content, &manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("decoding round.json: %w", err)
	}

	return manifest, nil
}

func FromRound(roundDir string) (Replay, error) {
	manifest, err := ReadManifest(roundDir)
	if err != nil {
		return Replay{}, err
	}

	builder := NewBuilder(manifest.Round, manifest.Scenario, manifest.Seed)

	file, err := os.Open(filepath.Join(roundDir, HistoryFile)) //nolint:gosec // G304: a round directory of the operator's rounds.
	if err != nil {
		return Replay{}, fmt.Errorf("opening the history: %w", err)
	}

	defer file.Close()

	_, err = FeedFrom(builder, file)
	if err != nil {
		return Replay{}, err
	}

	replay := builder.Replay().WithManifest(manifest)

	verdict, err := readReport(roundDir)
	if err != nil {
		return Replay{}, err
	}

	if verdict != nil {
		replay.Verdict = verdict.Verdict
		replay.Checks = verdict.Invariants
	}

	return replay, nil
}

// Returns how many bytes it consumed: a line still being written waits for
// later.
func FeedFrom(builder *Builder, reader io.Reader) (int64, error) {
	buffered := bufio.NewReaderSize(reader, maxLineBytes)

	var consumed int64

	for {
		line, err := buffered.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return consumed, fmt.Errorf("a history line is longer than %d bytes", maxLineBytes)
		}

		if err != nil {
			return consumed, ignoreEOF(err)
		}

		consumed += int64(len(line))
		builder.FeedLine(line)
	}
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

func readReport(roundDir string) (*report, error) {
	content, err := os.ReadFile(filepath.Join(roundDir, reportFile)) //nolint:gosec // G304: a round directory of the operator's rounds.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading report.json: %w", err)
	}

	var verdict report

	err = json.Unmarshal(content, &verdict)
	if err != nil {
		return nil, fmt.Errorf("decoding report.json: %w", err)
	}

	return &verdict, nil
}

func (r Replay) WithManifest(manifest Manifest) Replay {
	r.Verdict = manifest.Verdict
	r.StartedAt = manifest.StartedAt
	r.Defenses = manifest.Defenses
	r.Stopped = manifest.Stopped
	r.Error = manifest.Error

	duration, err := time.ParseDuration(manifest.Duration)
	if err == nil {
		r.DurationSeconds = int(duration.Seconds())
	}

	scenario, err := scenarios.Load(manifest.Scenario)
	if err == nil {
		r.Description = codeNames.ReplaceAllString(scenario.Description, "")
	}

	return r
}
