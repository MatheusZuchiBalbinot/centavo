package dashboard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"centavo/lab/internal/replay"

	"centavo/lab/internal/checker"
)

// Bounds of what the panel sends and keeps.
const (
	// liveWindow is how much of the current round the live charts show.
	liveWindow = 10 * time.Minute
	// maxRoundsListed is the length of the round list.
	maxRoundsListed = 20
	// maxSummaries bounds the cache of finished rounds' counters.
	maxSummaries = 1_000
)

// summary is what the headline adds up from one finished round.
type summary struct {
	Round      string          `json:"round"`
	Scenario   string          `json:"scenario"`
	Seed       string          `json:"seed"`
	Verdict    checker.Verdict `json:"verdict"`
	Seconds    int             `json:"seconds"`
	Operations int             `json:"operations"`
	Fails      int             `json:"fails"`
	Counters   replay.Counters `json:"counters"`
	ReportURL  string          `json:"report_url"`
}

// headline is the panel's top line (docs/architecture.md, section 13).
type headline struct {
	ChaosSeconds  int `json:"chaos_seconds"`
	Kills         int `json:"kills"`
	Rounds        int `json:"rounds"`
	FailedRounds  int `json:"failed_rounds"`
	Failovers     int `json:"failovers"`
	NetworkFaults int `json:"network_faults"`
}

type liveResponse struct {
	Current  *replay.Replay `json:"current"`
	Headline headline       `json:"headline"`
	Rounds   []summary      `json:"rounds"`
}

// tracker follows the round being written and remembers finished ones.
type tracker struct {
	roundsDir string

	mu        sync.Mutex
	current   string
	builder   *replay.Builder
	offset    int64
	summaries map[string]summary
}

func newTracker(roundsDir string) *tracker {
	return &tracker{roundsDir: roundsDir, summaries: map[string]summary{}}
}

// live reads what was appended to the newest round's history since the last
// call, so a long round is parsed once, not on every poll.
func (t *tracker) live() (liveResponse, error) {
	entries, err := os.ReadDir(t.roundsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return liveResponse{Rounds: []summary{}}, nil
	}

	if err != nil {
		return liveResponse{}, fmt.Errorf("listing rounds: %w", err)
	}

	ids := roundsNewestFirst(entries)

	t.mu.Lock()
	defer t.mu.Unlock()

	response := liveResponse{Rounds: []summary{}}

	if len(ids) > 0 {
		current, err := t.follow(ids[0])
		if err != nil {
			return liveResponse{}, err
		}

		response.Current = current
	}

	response.Headline, response.Rounds = t.summarize(ids)

	return response, nil
}

func (t *tracker) follow(id string) (*replay.Replay, error) {
	dir := filepath.Join(t.roundsDir, id)

	if id != t.current {
		manifest, err := replay.ReadManifest(dir)
		if err != nil {
			// The round directory exists before its round.json.
			return nil, nil
		}

		t.current, t.offset = id, 0
		t.builder = replay.NewBuilder(manifest.Round, manifest.Scenario, manifest.Seed)
	}

	file, err := os.Open(filepath.Join(dir, replay.HistoryFile)) //nolint:gosec // G304: id matched roundDirPattern.
	if err != nil {
		return nil, nil
	}
	defer file.Close()

	_, err = file.Seek(t.offset, 0)
	if err != nil {
		return nil, err
	}

	consumed, err := replay.FeedFrom(t.builder, file)
	t.offset += consumed

	if err != nil {
		return nil, err
	}

	current := t.builder.Replay()
	current.Seconds = lastSeconds(current.Seconds, int(liveWindow/time.Second))

	return &current, nil
}

func lastSeconds(seconds []replay.Second, keep int) []replay.Second {
	if len(seconds) <= keep {
		return seconds
	}

	return seconds[len(seconds)-keep:]
}

// summarize adds up the finished rounds, reading each one's history once.
func (t *tracker) summarize(ids []string) (headline, []summary) {
	var (
		total  headline
		listed []summary
	)

	for _, id := range ids {
		round, isFinished := t.summaryOf(id)
		if !isFinished {
			continue
		}

		total.Rounds++
		total.ChaosSeconds += round.Seconds
		total.Kills += round.Counters.Kills + round.Counters.FailpointFires
		total.Failovers += round.Counters.Failovers
		total.NetworkFaults += round.Counters.Network

		if round.Verdict == checker.VerdictFail {
			total.FailedRounds++
		}

		if len(listed) < maxRoundsListed {
			listed = append(listed, round)
		}
	}

	return total, append([]summary{}, listed...)
}

// summaryOf returns a finished round's summary: one with a report.
func (t *tracker) summaryOf(id string) (summary, bool) {
	cached, isCached := t.summaries[id]
	if isCached {
		return cached, true
	}

	dir := filepath.Join(t.roundsDir, id)

	_, err := os.Stat(filepath.Join(dir, reportJSONFile))
	if err != nil {
		return summary{}, false
	}

	built, err := replay.FromRound(dir)
	if err != nil {
		return summary{}, false
	}

	round := summary{
		Round: id, Scenario: built.Scenario, Seed: built.Seed, Verdict: built.Verdict, Seconds: len(built.Seconds),
		Counters: built.Counters, ReportURL: "/rounds/" + id + "/report.md",
	}

	for _, second := range built.Seconds {
		round.Operations += second.OK + second.Fail + second.Info
		round.Fails += second.Fail
	}

	if len(t.summaries) < maxSummaries {
		t.summaries[id] = round
	}

	return round, true
}
