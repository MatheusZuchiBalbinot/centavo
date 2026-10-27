package dashboard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"centavo/lab/internal/checker"
	"centavo/lab/internal/replay"
)

const (
	maxSummaries = 1_000
	// launchTolerance absorbs clock rounding between the panel noting a
	// start and the round writing its own.
	launchTolerance = 2 * time.Second
)

// A round is finished once the checker has written its report.
const reportJSONFile = "report.json"

// Only names matching this are ever joined to the rounds directory.
var roundDirPattern = regexp.MustCompile(`^r(\d{4,})$`)

type summary struct {
	Round      string          `json:"round"`
	Scenario   string          `json:"scenario"`
	Seed       string          `json:"seed"`
	Verdict    checker.Verdict `json:"verdict"`
	Seconds    int             `json:"seconds"`
	Operations int             `json:"operations"`
	Fails      int             `json:"fails"`
	Counters   replay.Counters `json:"counters"`
	// Defenses is the round's defense profile, "robust" or with some
	// turned off, so the panel can tell a control round from a real one.
	Defenses  string `json:"defenses"`
	ReportURL string `json:"report_url"`
}

// Remembers the finished rounds' summaries, so a long list is parsed once.
type tracker struct {
	roundsDir string

	mu        sync.Mutex
	summaries map[string]summary
}

// Parses each byte of the growing history once.
type follower struct {
	dir     string
	builder *replay.Builder
	offset  int64
}

func newTracker(roundsDir string) *tracker {
	return &tracker{roundsDir: roundsDir, summaries: map[string]summary{}}
}

func newFollower(dir string) *follower {
	return &follower{dir: dir}
}

func (f *follower) advance() (*replay.Replay, error) {
	manifest, err := replay.ReadManifest(f.dir)
	if err != nil {
		// The round directory exists before its round.json.
		return nil, nil
	}

	if f.builder == nil {
		f.builder = replay.NewBuilder(manifest.Round, manifest.Scenario, manifest.Seed)
	}

	file, err := os.Open(filepath.Join(f.dir, replay.HistoryFile))
	if err != nil {
		return nil, nil
	}

	defer file.Close()

	_, err = file.Seek(f.offset, 0)
	if err != nil {
		return nil, err
	}

	consumed, err := replay.FeedFrom(f.builder, file)
	f.offset += consumed

	if err != nil {
		return nil, err
	}

	current := f.builder.Replay().WithManifest(manifest)

	return &current, nil
}

// Give or take the moment between launching `lab run` and its first write.
func (t *tracker) roundStartedSince(since time.Time) string {
	entries, err := os.ReadDir(t.roundsDir)
	if err != nil {
		return ""
	}

	ids := roundsNewestFirst(entries)
	if len(ids) == 0 {
		return ""
	}

	manifest, err := replay.ReadManifest(filepath.Join(t.roundsDir, ids[0]))
	if err != nil {
		return ""
	}

	isThisRound := !manifest.StartedAt.Before(since.Add(-launchTolerance))
	if !isThisRound {
		return ""
	}

	return ids[0]
}

func (t *tracker) page(cursor string, size int) (roundsPage, error) {
	entries, err := os.ReadDir(t.roundsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return roundsPage{Rounds: []summary{}}, nil
	}

	if err != nil {
		return roundsPage{}, fmt.Errorf("listing rounds: %w", err)
	}

	ids := roundsNewestFirst(entries)

	if cursor != "" {
		ids = ids[indexAfter(ids, cursor):]
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	page := roundsPage{Rounds: []summary{}}

	for _, id := range ids {
		round, isFinished := t.summaryOf(id)
		if !isFinished {
			continue
		}

		if len(page.Rounds) == size {
			page.NextCursor = page.Rounds[size-1].Round

			break
		}

		page.Rounds = append(page.Rounds, round)
	}

	return page, nil
}

// A cursor that is no longer there starts from the round numbered right
// below it.
func indexAfter(ids []string, cursor string) int {
	cursorNumber := roundNumberOf(cursor)

	for index, id := range ids {
		if roundNumberOf(id) < cursorNumber {
			return index
		}
	}

	return len(ids)
}

func roundNumberOf(id string) int {
	match := roundDirPattern.FindStringSubmatch(id)
	if match == nil {
		return 0
	}

	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}

	return number
}

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
		Counters: built.Counters, Defenses: built.Defenses, ReportURL: reportURLOf(id),
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

func roundsNewestFirst(entries []os.DirEntry) []string {
	type numbered struct {
		id     string
		number int
	}

	var rounds []numbered

	for _, entry := range entries {
		isRound := entry.IsDir() && roundDirPattern.MatchString(entry.Name())
		if !isRound {
			continue
		}

		rounds = append(rounds, numbered{id: entry.Name(), number: roundNumberOf(entry.Name())})
	}

	slices.SortFunc(rounds, func(a, b numbered) int { return b.number - a.number })

	ids := make([]string, 0, len(rounds))
	for _, round := range rounds {
		ids = append(ids, round.id)
	}

	return ids
}
