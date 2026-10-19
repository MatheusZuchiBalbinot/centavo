package history_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"centavo/lab/internal/history"
)

func TestWriter_ConcurrentAppendsKeepIndexAndTimeInFileOrder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")

	writer, err := history.Create(path, "r0001")
	if err != nil {
		t.Fatal(err)
	}

	const processes, perProcess = 8, 50

	var wg sync.WaitGroup

	for process := 1; process <= processes; process++ {
		wg.Go(func() {
			for range perProcess {
				_, err := writer.AppendDurable(history.Record{Process: history.Client(process), Type: history.TypeInvoke, F: history.FunctionReadBalance, Op: "op"})
				if err != nil {
					t.Error(err)
				}
			}
		})
	}

	wg.Wait()

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	records := readRecords(t, path)
	if len(records) != processes*perProcess {
		t.Fatalf("got %d records, want %d", len(records), processes*perProcess)
	}

	for i, record := range records {
		isInOrder := record.Index == int64(i+1) && (i == 0 || record.TimeNS >= records[i-1].TimeNS)
		if !isInOrder {
			t.Fatalf("record %d has index %d and time %d after %d", i, record.Index, record.TimeNS, records[max(i-1, 0)].TimeNS)
		}

		isUnexpected := record.Round != "r0001" || record.Wall == ""
		if isUnexpected {
			t.Fatalf("record %d: round %q, wall %q", i, record.Round, record.Wall)
		}
	}
}

func TestWriter_DurableAppendIsOnDiskWhenItReturns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")

	writer, err := history.Create(path, "r0001")
	if err != nil {
		t.Fatal(err)
	}

	defer closeWriter(t, writer)

	written, err := writer.AppendDurable(history.Record{Process: history.Client(1), Type: history.TypeInvoke, F: history.FunctionDeposit, Op: "r0001-p01-000001"})
	if err != nil {
		t.Fatal(err)
	}

	// Read through a separate handle while the writer is still open: the
	// invoke must already be in the file, as if the lab had crashed right here.
	records := readRecords(t, path)
	isUnexpected := len(records) != 1 || records[0].Index != written.Index
	if isUnexpected {
		t.Fatalf("file holds %+v right after the durable append", records)
	}
}

func TestWriter_RefusesToOverwriteAndToAppendAfterClose(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")

	writer, err := history.Create(path, "r0001")
	if err != nil {
		t.Fatal(err)
	}

	_, err = history.Create(path, "r0001")
	if err == nil {
		t.Fatal("a second writer opened an existing history")
	}

	closeWriter(t, writer)

	_, err = writer.Append(history.Record{Type: history.TypeOK})
	if !errors.Is(err, history.ErrWriterClosed) {
		t.Fatalf("append after close: got %v", err)
	}
}

func TestValidate_AcceptsAWellFormedHistory(t *testing.T) {
	t.Parallel()

	lines := []string{
		`{"index":1,"round":"r1","process":1,"type":"invoke","f":"deposit","op":"r1-p01-000001"}`,
		`{"index":2,"round":"r1","process":2,"type":"invoke","f":"transfer","op":"r1-p02-000001"}`,
		`{"index":3,"round":"r1","process":1,"type":"attempt","op":"r1-p01-000001","n":1}`,
		`{"index":4,"round":"r1","process":1,"type":"ok","f":"deposit","op":"r1-p01-000001","attempts":2}`,
		`{"index":5,"round":"r1","process":2,"type":"info","f":"transfer","op":"r1-p02-000001"}`,
		`{"index":6,"round":"r1","process":3,"type":"invoke","f":"transfer","op":"r1-p03-000001"}`,
		`{"index":7,"round":"r1","process":3,"type":"fail","f":"transfer","op":"r1-p03-000001"}`,
		`{"index":8,"round":"r1","process":1,"type":"invoke","f":"read_balance","op":"r1-p01-000002"}`,
	}

	report := validate(t, lines)

	if !report.IsWellFormed() {
		t.Fatalf("violations: %+v", report.Violations)
	}

	isCounted := report.Invocations == 4 && report.OK == 1 && report.Fail == 1 && report.Info == 1
	if !isCounted {
		t.Fatalf("report: %+v", report)
	}

	isUnexpected := len(report.Open) != 1 || report.Open[0] != "r1-p01-000002"
	if isUnexpected {
		t.Fatalf("open operations: %v", report.Open)
	}
}

func TestValidate_FlagsEachBrokenRule(t *testing.T) {
	t.Parallel()

	invoke := `{"index":1,"round":"r1","process":1,"type":"invoke","f":"deposit","op":"a"}`

	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "index goes back", lines: []string{invoke, `{"index":1,"round":"r1","process":2,"type":"invoke","f":"deposit","op":"b"}`}, want: "does not follow"},
		{name: "two completions", lines: []string{invoke, `{"index":2,"round":"r1","process":1,"type":"ok","f":"deposit","op":"a"}`, `{"index":3,"round":"r1","process":1,"type":"fail","f":"deposit","op":"a"}`}, want: "not open"},
		{name: "invoked twice", lines: []string{invoke, `{"index":2,"round":"r1","process":1,"type":"ok","f":"deposit","op":"a"}`, `{"index":3,"round":"r1","process":1,"type":"invoke","f":"deposit","op":"a"}`}, want: "invoked twice"},
		{name: "two open per process", lines: []string{invoke, `{"index":2,"round":"r1","process":1,"type":"invoke","f":"deposit","op":"b"}`}, want: "still open"},
		{name: "process reused after info", lines: []string{invoke, `{"index":2,"round":"r1","process":1,"type":"info","f":"deposit","op":"a"}`, `{"index":3,"round":"r1","process":1,"type":"invoke","f":"deposit","op":"b"}`}, want: "retired"},
		{name: "completion from another process", lines: []string{invoke, `{"index":2,"round":"r1","process":9,"type":"ok","f":"deposit","op":"a"}`}, want: "invoked by 1"},
		{name: "completion with another f", lines: []string{invoke, `{"index":2,"round":"r1","process":1,"type":"ok","f":"transfer","op":"a"}`}, want: "invoked as deposit"},
		{name: "mixed rounds", lines: []string{invoke, `{"index":2,"round":"r2","process":2,"type":"invoke","f":"deposit","op":"b"}`}, want: "differs"},
		{name: "unknown type", lines: []string{`{"index":1,"round":"r1","process":1,"type":"guess","op":"a"}`}, want: "unknown record type"},
		{name: "garbage line", lines: []string{invoke, `{"index":2,"round"`}, want: "not a JSON record"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			report := validate(t, tc.lines)

			if report.IsWellFormed() {
				t.Fatal("broken history passed")
			}

			isExpected := strings.Contains(report.Violations[0].Message, tc.want)
			if !isExpected {
				t.Fatalf("first violation %q does not mention %q", report.Violations[0].Message, tc.want)
			}
		})
	}
}

func validate(t *testing.T, lines []string) history.Report {
	t.Helper()

	report, err := history.Validate(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}

	return report
}

func readRecords(t *testing.T, path string) []history.Record {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	var records []history.Record

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record history.Record

		err := json.Unmarshal(scanner.Bytes(), &record)
		if err != nil {
			t.Fatalf("line %q: %v", scanner.Text(), err)
		}

		records = append(records, record)
	}

	return records
}

func closeWriter(t *testing.T, writer *history.Writer) {
	t.Helper()

	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}
}
