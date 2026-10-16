package loadgen

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/internal/seed"
	"centavo/lab/scenarios"
)

// fastPolicy keeps retry tests short.
var fastPolicy = retryPolicy{
	attemptTimeout: 500 * time.Millisecond,
	maxRetries:     2,
	backoffBase:    time.Millisecond,
	backoffCap:     2 * time.Millisecond,
	deadline:       2 * time.Second,
}

func TestClassify(t *testing.T) {
	parse := parseDeposit
	validBody := `{"transaction_id": "t1", "balance_cents": 10}`

	cases := []struct {
		name        string
		status      int
		body        string
		wantVerdict verdict
		wantKind    outcomeKind
	}{
		{name: "success", status: 201, body: validBody, wantVerdict: verdictSucceeded},
		{name: "success with a cut body", status: 201, body: `{"transaction_id": "t1", "bal`, wantVerdict: verdictRetry, wantKind: outcomeInvalidBody},
		{name: "success missing a field", status: 201, body: `{"transaction_id": "t1"}`, wantVerdict: verdictRetry, wantKind: outcomeInvalidBody},
		{name: "definitive domain error", status: 422, body: `{"error": {"code": "INSUFFICIENT_FUNDS"}}`, wantVerdict: verdictFailed, wantKind: outcomeDomain},
		{name: "request in progress", status: 409, body: `{"error": {"code": "REQUEST_IN_PROGRESS"}}`, wantVerdict: verdictRetry, wantKind: outcomeRequestInProgress},
		{name: "server error, even with a definitive code", status: 503, body: `{"error": {"code": "INSUFFICIENT_FUNDS"}}`, wantVerdict: verdictRetry, wantKind: outcomeServerError},
		{name: "4xx without a known code", status: 404, body: `not found`, wantVerdict: verdictGiveUp, wantKind: outcomeUnexpected},
		{name: "known code on a non-error status", status: 302, body: `{"error": {"code": "INSUFFICIENT_FUNDS"}}`, wantVerdict: verdictGiveUp, wantKind: outcomeUnexpected},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := classify(tc.status, []byte(tc.body), parse)

			if result.verdict != tc.wantVerdict || result.outcome.Kind != tc.wantKind {
				t.Fatalf("got verdict %d kind %q, want %d %q", result.verdict, result.outcome.Kind, tc.wantVerdict, tc.wantKind)
			}
		})
	}
}

func TestBackoff_StaysWithinTheExponentialCeiling(t *testing.T) {
	jitter := seed.Seed(1).Stream("test")

	for attempt := 1; attempt <= 12; attempt++ {
		ceiling := min(politePolicy.backoffBase<<(attempt-1), politePolicy.backoffCap)

		for range 50 {
			wait := politePolicy.backoff(jitter, attempt)
			if wait < 0 || wait > ceiling {
				t.Fatalf("attempt %d waited %v, ceiling %v", attempt, wait, ceiling)
			}
		}
	}
}

func TestRun_RecordsAWellFormedHistoryThatMatchesTheBank(t *testing.T) {
	bank := newFakeBank()
	server := httptest.NewServer(bank)
	defer server.Close()

	workload := scenarios.Workload{Profile: scenarios.ProfileLedgerOnly, Clients: 4, RatePerSecond: 400, Accounts: 8, ExclusiveAccounts: true}
	report, path := runRound(t, server.URL, workload, politePolicy)

	isClean := report.IsWellFormed() && len(report.Open) == 0 && report.Info == 0
	if !isClean {
		t.Fatalf("history: %+v", report)
	}

	// 8 accounts and 8 funding deposits, then the load itself.
	if report.OK <= 16 {
		t.Fatalf("only %d operations succeeded", report.OK)
	}

	assertEveryWriteReachedTheBankWithItsKey(t, path, bank)
}

func TestRun_RetriesWithTheSameKeyAndRetiresTheProcessAfterInfo(t *testing.T) {
	bank := newFakeBank()
	bank.failFirstAttempt = true
	bank.alwaysFailTransfers = true
	server := httptest.NewServer(bank)
	defer server.Close()

	workload := scenarios.Workload{Profile: scenarios.ProfileLedgerOnly, Clients: 2, RatePerSecond: 200, Accounts: 4, ExclusiveAccounts: true}
	report, path := runRound(t, server.URL, workload, fastPolicy)

	if !report.IsWellFormed() || len(report.Open) != 0 {
		t.Fatalf("history: %+v", report)
	}

	if report.Info == 0 {
		t.Fatal("transfers always fail with 503, yet no operation ended as info")
	}

	records := readHistory(t, path)
	retiredProcess := map[int]bool{}

	for _, record := range records {
		isRetried := record.Type == history.TypeOK && record.F == history.FunctionDeposit
		if isRetried && record.Attempts != 2 {
			t.Fatalf("deposit %s took %d attempts, want 2 (one 503, then success)", record.Op, record.Attempts)
		}

		if record.Type == history.TypeInfo {
			retiredProcess[record.Process.ClientNumber()] = true
		}

		process := record.Process.ClientNumber()

		hasNewProcess := process > workload.Clients
		if hasNewProcess && !strings.Contains(record.Op, fmt.Sprintf("-p%02d-", process)) {
			t.Fatalf("op %s does not carry its process %d", record.Op, process)
		}
	}

	if len(retiredProcess) == 0 {
		t.Fatal("no process was retired")
	}

	if got := bank.maxAttemptsPerKey(); got != fastPolicy.maxRetries+1 {
		t.Fatalf("the bank saw at most %d attempts per key, want %d", got, fastPolicy.maxRetries+1)
	}
}

func TestRun_DuplicatorSendsEveryWriteTwiceWithTheSameKey(t *testing.T) {
	bank := newFakeBank()
	server := httptest.NewServer(bank)
	defer server.Close()

	workload := scenarios.Workload{Profile: scenarios.ProfileLedgerOnly, Clients: 2, RatePerSecond: 200, Accounts: 4, ExclusiveAccounts: true}
	workload.ClientMix = map[scenarios.ClientProfile]float64{scenarios.ClientDuplicator: 1}
	report, path := runRound(t, server.URL, workload, fastPolicy)

	if !report.IsWellFormed() || len(report.Open) != 0 {
		t.Fatalf("history: %+v", report)
	}

	var loadWrites int

	for _, record := range readHistory(t, path) {
		isLoadWrite := record.Type == history.TypeOK && record.Attempts == duplicateCopies
		if !isLoadWrite {
			continue
		}

		loadWrites++

		isWrite := record.F == history.FunctionDeposit || record.F == history.FunctionTransfer
		if isWrite && bank.attemptsFor(record.Op) != duplicateCopies {
			t.Fatalf("write %s reached the bank %d times", record.Op, bank.attemptsFor(record.Op))
		}
	}

	if loadWrites == 0 {
		t.Fatal("no duplicated operation in the load")
	}
}

func runRound(t *testing.T, bankURL string, workload scenarios.Workload, policy retryPolicy) (history.Report, string) {
	t.Helper()

	if workload.ClientMix == nil {
		workload.ClientMix = map[scenarios.ClientProfile]float64{scenarios.ClientPolite: 1}
	}
	path := filepath.Join(t.TempDir(), "history.jsonl")

	writer, err := history.Create(path, "r0001")
	if err != nil {
		t.Fatal(err)
	}

	config := Config{
		Round:    "r0001",
		Seed:     seed.Seed(0x5eed),
		Workload: workload,
		Duration: 300 * time.Millisecond,
		BankURL:  bankURL,
		History:  writer,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err = runWith(t.Context(), config, policy)
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	report, err := history.Validate(file)
	if err != nil {
		t.Fatal(err)
	}

	return report, path
}

// assertEveryWriteReachedTheBankWithItsKey checks the link the checker relies
// on: the idempotency key of each write is its operation id, and reads carry none.
func assertEveryWriteReachedTheBankWithItsKey(t *testing.T, path string, bank *fakeBank) {
	t.Helper()

	for _, record := range readHistory(t, path) {
		if record.Type != history.TypeOK {
			continue
		}

		isWrite := record.F == history.FunctionCreateAccount || record.F == history.FunctionDeposit || record.F == history.FunctionTransfer
		attempts := bank.attemptsFor(record.Op)

		if isWrite && attempts != 1 {
			t.Fatalf("write %s reached the bank %d times with its key", record.Op, attempts)
		}

		if !isWrite && attempts != 0 {
			t.Fatalf("read %s carried an idempotency key", record.Op)
		}

		if !bank.sawCorrelation(record.Op) {
			t.Fatalf("operation %s never reached the bank as its correlation id", record.Op)
		}
	}
}

func readHistory(t *testing.T, path string) []history.Record {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var records []history.Record

	for line := range strings.SplitSeq(strings.TrimSpace(string(content)), "\n") {
		var record history.Record

		err := json.Unmarshal([]byte(line), &record)
		if err != nil {
			t.Fatal(err)
		}

		records = append(records, record)
	}

	return records
}

// fakeBank is a correct in-memory bank with optional faults.
type fakeBank struct {
	failFirstAttempt    bool
	alwaysFailTransfers bool

	mu           sync.Mutex
	nextID       int
	balances     map[string]int64
	attempts     map[string]int
	correlations map[string]bool
}

func newFakeBank() *fakeBank {
	return &fakeBank{balances: map[string]int64{}, attempts: map[string]int{}, correlations: map[string]bool{}}
}

func (b *fakeBank) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.correlations[r.Header.Get("X-Correlation-Id")] = true

	key := r.Header.Get("Idempotency-Key")
	if key != "" {
		b.attempts[key]++
	}

	isFirstAttempt := key != "" && b.attempts[key] == 1
	isTransfer := r.URL.Path == "/v1/transfers"

	shouldFail := (b.failFirstAttempt && isFirstAttempt) || (b.alwaysFailTransfers && isTransfer)
	if shouldFail {
		writeFake(w, http.StatusServiceUnavailable, `{"error": {"code": "TEMPORARILY_UNAVAILABLE"}}`)

		return
	}

	b.route(w, r)
}

func (b *fakeBank) route(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/accounts":
		b.nextID++
		id := fmt.Sprintf("acc-%d", b.nextID)
		b.balances[id] = 0
		writeFake(w, http.StatusCreated, `{"account_id": "`+id+`"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/deposits":
		var body depositArgs
		_ = json.NewDecoder(r.Body).Decode(&body)
		b.balances[string(body.AccountID)] += body.AmountCents
		writeFake(w, http.StatusCreated, fmt.Sprintf(`{"transaction_id": "t", "balance_cents": %d}`, b.balances[string(body.AccountID)]))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/transfers":
		b.transfer(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/statement"):
		writeFake(w, http.StatusOK, `{"entries": [], "next_after_seq": null}`)
	case r.Method == http.MethodGet:
		id := strings.TrimPrefix(r.URL.Path, "/v1/accounts/")
		writeFake(w, http.StatusOK, fmt.Sprintf(`{"account_id": "%s", "balance_cents": %d}`, id, b.balances[id]))
	default:
		writeFake(w, http.StatusNotFound, ``)
	}
}

func (b *fakeBank) transfer(w http.ResponseWriter, r *http.Request) {
	var body transferBody
	_ = json.NewDecoder(r.Body).Decode(&body)

	from := string(body.FromAccountID)
	if b.balances[from] < body.AmountCents {
		writeFake(w, http.StatusUnprocessableEntity, `{"error": {"code": "INSUFFICIENT_FUNDS"}}`)

		return
	}

	b.balances[from] -= body.AmountCents
	b.balances[string(body.ToAccountID)] += body.AmountCents
	writeFake(w, http.StatusCreated, fmt.Sprintf(`{"transaction_id": "t", "from_balance_cents": %d}`, b.balances[from]))
}

func (b *fakeBank) attemptsFor(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.attempts[key]
}

func (b *fakeBank) maxAttemptsPerKey() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	highest := 0
	for _, count := range b.attempts {
		highest = max(highest, count)
	}

	return highest
}

func (b *fakeBank) sawCorrelation(op string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.correlations[op]
}

func writeFake(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}
