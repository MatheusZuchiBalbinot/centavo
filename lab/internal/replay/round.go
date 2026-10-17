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
)

// Files of a round directory the replay reads.
const (
	HistoryFile  = "history.jsonl"
	manifestFile = "round.json"
	reportFile   = "report.json"
	// File is where `lab export-replay` writes the replay.
	File = "replay.json"
	// maxLineBytes matches the checker's limit for one history record.
	maxLineBytes = 1 << 20
)

// Manifest is the part of round.json a replay shows.
type Manifest struct {
	Round    string `json:"round"`
	Scenario string `json:"scenario"`
	Seed     string `json:"seed"`
	Verdict  string `json:"verdict"`
}

type report struct {
	Verdict    string  `json:"verdict"`
	Invariants []Check `json:"invariants"`
}

// ReadManifest loads round.json.
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

// FromRound builds the replay of an archived round: its history, and the
// verdict of its report when the checker wrote one.
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

	replay := builder.Replay()
	replay.Verdict = manifest.Verdict

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

// FeedFrom feeds every complete line of reader to the builder and returns
// how many bytes it consumed: a line still being written waits for later.
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
