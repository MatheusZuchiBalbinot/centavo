// Package snapshot is the ledger export the checker reads: one CSV per table,
// taken from a single REPEATABLE READ snapshot. The ledger schema is an audit
// contract: changing it means changing this package on purpose.
package snapshot

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unique"

	"centavo/lab/internal/platform/fsmode"
)

// Dir is the export directory inside a round directory.
const Dir = "export"

const (
	accountsFile     = "accounts.csv"
	balancesFile     = "account_balances.csv"
	transactionsFile = "ledger_transactions.csv"
	entriesFile      = "ledger_entries.csv"
	transfersFile    = "external_transfers.csv"
	transitionsFile  = "external_transfer_transitions.csv"
	incomingFile     = "incoming_transfers.csv"
	bandsFile        = "ledger_bands.csv"
	metaFile         = "meta.json"
)

// Account kinds and the system accounts the checker reads by code.
const (
	KindCustomer AccountKind = "customer"
	KindSystem   AccountKind = "system"

	ExternalCashCode      = "sys_external_cash"
	PixOutTransitCode     = "sys_pix_out_transit"
	PartnerSettlementCode = "sys_partner_settlement"
	SuspenseCode          = "sys_reconciliation_suspense"
)

// Ledger transaction kinds the checker reads.
const (
	TransactionDeposit                  TransactionKind = "deposit"
	TransactionInternalTransfer         TransactionKind = "internal_transfer"
	TransactionPixOutReserve            TransactionKind = "pix_out_reserve"
	TransactionPixOutSettle             TransactionKind = "pix_out_settle"
	TransactionPixOutRefund             TransactionKind = "pix_out_refund"
	TransactionPixOutReturn             TransactionKind = "pix_out_return"
	TransactionPixInCredit              TransactionKind = "pix_in_credit"
	TransactionReconciliationAdjustment TransactionKind = "reconciliation_adjustment"
)

// ErrMalformedExport means the bank's export does not decode.
var ErrMalformedExport = errors.New("malformed export")

// BandSize is how many entry ids one immutability band covers (I8).
const BandSize = 100_000

var tables = []table{
	{file: accountsFile, header: []string{"id", "kind", "system_code"}, rows: accountRows, load: loadAccount},
	{file: balancesFile, header: []string{"account_id", "balance_cents", "last_entry_seq"}, rows: balanceRows, load: loadBalance},
	{
		file: transactionsFile, header: []string{"id", "kind", "client_ref", "correlation_id", "external_transfer_id", "incoming_transfer_id"},
		rows: transactionRows, load: loadTransaction,
	},
	{
		file: entriesFile, header: []string{"id", "transaction_id", "account_id", "amount_cents", "account_seq", "balance_after_cents"},
		rows: entryRows, load: loadEntry,
	},
	{
		file: transfersFile, header: []string{"id", "end_to_end_id", "client_ref", "account_id", "amount_cents", "status", "status_seq", "failure_code"},
		rows: transferRows, load: loadTransfer,
	},
	{
		file: transitionsFile, header: []string{"transfer_id", "seq", "from_status", "to_status", "cause", "ledger_transaction_id"},
		rows: transitionRows, load: loadTransition,
	},
	{file: incomingFile, header: []string{"id", "end_to_end_id", "account_id", "amount_cents"}, rows: incomingRows, load: loadIncoming},
	{file: bandsFile, header: []string{"band", "count", "sum_cents", "hash"}, rows: bandRows, load: loadBand},
}

// Columns of each CSV, in the order of its header in tables.
const (
	colTransferID = iota
	colTransferEndToEnd
	colTransferClientRef
	colTransferAccount
	colTransferAmount
	colTransferStatus
	colTransferStatusSeq
	colTransferFailure
)

const (
	colTransitionTransfer = iota
	colTransitionSeq
	colTransitionFrom
	colTransitionTo
	colTransitionCause
	colTransitionLedger
)

const (
	colIncomingID = iota
	colIncomingEndToEnd
	colIncomingAccount
	colIncomingAmount
)

const (
	colAccountID = iota
	colAccountKind
	colAccountCode
)

const (
	colBalanceAccount = iota
	colBalanceCents
	colBalanceLastSeq
)

const (
	colTransactionID = iota
	colTransactionKind
	colTransactionClientRef
	colTransactionCorrelation
	colTransactionExternal
	colTransactionIncoming
)

const (
	colEntryID = iota
	colEntryTransaction
	colEntryAccount
	colEntryAmount
	colEntrySeq
	colEntryBalanceAfter
)

// AccountKind tells customer accounts from system ones.
type AccountKind string

// TransactionKind is the kind of a ledger transaction.
type TransactionKind string

// TransferStatus is one state of an external transfer.
type TransferStatus string

// Account is a row of accounts.
type Account struct {
	ID         string
	Kind       AccountKind
	SystemCode string
}

// Balance is a row of account_balances.
type Balance struct {
	AccountID    string
	BalanceCents int64
	LastEntrySeq int64
}

// Transaction is a row of ledger_transactions.
type Transaction struct {
	ID                 string
	Kind               TransactionKind
	ClientRef          string
	CorrelationID      string
	ExternalTransferID string
	IncomingTransferID string
}

// ExternalTransfer is a row of external_transfers.
type ExternalTransfer struct {
	ID          string
	EndToEndID  string
	ClientRef   string
	AccountID   string
	AmountCents int64
	Status      TransferStatus
	StatusSeq   int64
	FailureCode string
}

// Transition is a row of external_transfer_transitions; From is "" for the first.
type Transition struct {
	TransferID          string
	Seq                 int64
	From                TransferStatus
	To                  TransferStatus
	Cause               string
	LedgerTransactionID string
}

// Incoming is a row of incoming_transfers.
type Incoming struct {
	ID          string
	EndToEndID  string
	AccountID   string
	AmountCents int64
}

// Entry is a row of ledger_entries.
type Entry struct {
	ID                int64
	TransactionID     string
	AccountID         string
	AmountCents       int64
	AccountSeq        int64
	BalanceAfterCents int64
}

// Meta says what the export covers.
type Meta struct {
	Round   string    `json:"round"`
	TakenAt time.Time `json:"taken_at"`
	// FirstTransactionID tells one database from another: a rebuilt stack
	// starts a new ledger, and its bands owe nothing to the old one's.
	FirstTransactionID string `json:"first_transaction_id"`
}

// Band is the fingerprint of the ledger entries whose ids fall in one band.
type Band struct {
	Band     int64 `json:"band"`
	Count    int64 `json:"count"`
	SumCents int64 `json:"sum_cents"`
	Hash     int64 `json:"hash"`
}

// Snapshot is the part of the ledger a round touched: the accounts it
// created, every system account, the transactions with its client_refs and
// every entry of those accounts and transactions.
type Snapshot struct {
	Meta         Meta
	Accounts     []Account
	Balances     []Balance
	Transactions []Transaction
	Entries      []Entry
	Transfers    []ExternalTransfer
	Transitions  []Transition
	Incoming     []Incoming
	// Bands cover the whole ledger, not only the round.
	Bands []Band
}

// LedgerState is what a round leaves for the next one to check I8 against: the
// fingerprint of the whole ledger when it was exported.
type LedgerState struct {
	FirstTransactionID string `json:"first_transaction_id"`
	Bands              []Band `json:"bands"`
}

type table struct {
	file   string
	header []string
	rows   func(s *Snapshot) [][]string
	load   func(s *Snapshot, record []string) error
}

// Write stores the snapshot in roundDir/export.
func Write(roundDir string, state Snapshot) error {
	dir := filepath.Join(roundDir, Dir)

	err := os.MkdirAll(dir, fsmode.PrivateDir)
	if err != nil {
		return fmt.Errorf("creating export directory: %w", err)
	}

	for _, t := range tables {
		err := writeTable(filepath.Join(dir, t.file), t.header, t.rows(&state))
		if err != nil {
			return err
		}
	}

	meta, err := json.MarshalIndent(state.Meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding export meta: %w", err)
	}

	return os.WriteFile(filepath.Join(dir, metaFile), append(meta, '\n'), fsmode.PrivateFile)
}

// Read loads the snapshot of roundDir/export.
func Read(roundDir string) (Snapshot, error) {
	dir := filepath.Join(roundDir, Dir)

	var s Snapshot

	for _, t := range tables {
		err := readTable(filepath.Join(dir, t.file), t.header, func(record []string) error { return t.load(&s, record) })
		if err != nil {
			return Snapshot{}, err
		}
	}

	meta, err := os.ReadFile(filepath.Join(dir, metaFile)) //nolint:gosec // G304: the round directory comes from the operator.
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading export meta: %w", err)
	}

	err = json.Unmarshal(meta, &s.Meta)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %s: %w", ErrMalformedExport, metaFile, err)
	}

	return s, nil
}

// StateOf is the state the snapshot leaves behind.
func StateOf(state Snapshot) LedgerState {
	return LedgerState{FirstTransactionID: state.Meta.FirstTransactionID, Bands: append([]Band{}, state.Bands...)}
}

// WriteState stores a ledger state.
func WriteState(path string, state LedgerState) error {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the ledger state: %w", err)
	}

	return os.WriteFile(path, append(content, '\n'), fsmode.PrivateFile)
}

// ReadState loads a ledger state; false means there is none yet.
func ReadState(path string) (LedgerState, bool, error) {
	content, err := os.ReadFile(path) //nolint:gosec // G304: the path comes from the operator's rounds directory.
	if errors.Is(err, os.ErrNotExist) {
		return LedgerState{}, false, nil
	}

	if err != nil {
		return LedgerState{}, false, fmt.Errorf("reading the ledger state: %w", err)
	}

	var state LedgerState

	err = json.Unmarshal(content, &state)
	if err != nil {
		return LedgerState{}, false, fmt.Errorf("%w: ledger state: %w", ErrMalformedExport, err)
	}

	return state, true, nil
}

func writeTable(path string, header []string, rows [][]string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fsmode.PrivateFile) //nolint:gosec // G304: the round directory and a constant name.
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Base(path), err)
	}

	writer := csv.NewWriter(file)

	err = writer.Write(header)
	if err == nil {
		err = writer.WriteAll(rows)
	}

	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}

	return closeErr
}

func readTable(path string, header []string, load func([]string) error) error {
	file, err := os.Open(path) //nolint:gosec // G304: path is built from the round directory and a constant name.
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}

	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = len(header)
	reader.ReuseRecord = true

	_, err = reader.Read()
	if err != nil {
		return fmt.Errorf("%w: %s: missing header: %w", ErrMalformedExport, filepath.Base(path), err)
	}

	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrMalformedExport, filepath.Base(path), err)
		}

		err = load(record)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrMalformedExport, filepath.Base(path), err)
		}
	}
}

func accountRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Accounts))
	for _, a := range state.Accounts {
		rows = append(rows, []string{a.ID, string(a.Kind), a.SystemCode})
	}

	return rows
}

func balanceRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Balances))
	for _, b := range state.Balances {
		rows = append(rows, []string{b.AccountID, itoa(b.BalanceCents), itoa(b.LastEntrySeq)})
	}

	return rows
}

func transactionRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Transactions))
	for _, t := range state.Transactions {
		rows = append(rows, []string{t.ID, string(t.Kind), t.ClientRef, t.CorrelationID, t.ExternalTransferID, t.IncomingTransferID})
	}

	return rows
}

func entryRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Entries))
	for _, e := range state.Entries {
		rows = append(rows, []string{itoa(e.ID), e.TransactionID, e.AccountID, itoa(e.AmountCents), itoa(e.AccountSeq), itoa(e.BalanceAfterCents)})
	}

	return rows
}

func transferRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Transfers))
	for _, t := range state.Transfers {
		rows = append(rows, []string{t.ID, t.EndToEndID, t.ClientRef, t.AccountID, itoa(t.AmountCents), string(t.Status), itoa(t.StatusSeq), t.FailureCode})
	}

	return rows
}

func transitionRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Transitions))
	for _, t := range state.Transitions {
		rows = append(rows, []string{t.TransferID, itoa(t.Seq), string(t.From), string(t.To), t.Cause, t.LedgerTransactionID})
	}

	return rows
}

func incomingRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Incoming))
	for _, i := range state.Incoming {
		rows = append(rows, []string{i.ID, i.EndToEndID, i.AccountID, itoa(i.AmountCents)})
	}

	return rows
}

func loadTransfer(state *Snapshot, record []string) error {
	numbers, err := atois(record[colTransferAmount], record[colTransferStatusSeq])
	if err != nil {
		return err
	}

	transfer := ExternalTransfer{
		ID: record[colTransferID], EndToEndID: record[colTransferEndToEnd], ClientRef: record[colTransferClientRef],
		AccountID: record[colTransferAccount], AmountCents: numbers[0], Status: TransferStatus(record[colTransferStatus]),
		StatusSeq: numbers[1], FailureCode: record[colTransferFailure],
	}
	state.Transfers = append(state.Transfers, transfer)

	return nil
}

func loadTransition(state *Snapshot, record []string) error {
	numbers, err := atois(record[colTransitionSeq])
	if err != nil {
		return err
	}

	transition := Transition{
		TransferID: record[colTransitionTransfer], Seq: numbers[0], From: TransferStatus(record[colTransitionFrom]),
		To: TransferStatus(record[colTransitionTo]), Cause: record[colTransitionCause], LedgerTransactionID: record[colTransitionLedger],
	}

	state.Transitions = append(state.Transitions, transition)

	return nil
}

func loadIncoming(state *Snapshot, record []string) error {
	numbers, err := atois(record[colIncomingAmount])
	if err != nil {
		return err
	}

	incoming := Incoming{ID: record[colIncomingID], EndToEndID: record[colIncomingEndToEnd], AccountID: record[colIncomingAccount], AmountCents: numbers[0]}
	state.Incoming = append(state.Incoming, incoming)

	return nil
}

func loadAccount(state *Snapshot, record []string) error {
	account := Account{ID: record[colAccountID], Kind: AccountKind(record[colAccountKind]), SystemCode: record[colAccountCode]}
	state.Accounts = append(state.Accounts, account)

	return nil
}

func loadBalance(state *Snapshot, record []string) error {
	numbers, err := atois(record[colBalanceCents], record[colBalanceLastSeq])
	if err != nil {
		return err
	}

	balance := Balance{AccountID: record[colBalanceAccount], BalanceCents: numbers[0], LastEntrySeq: numbers[1]}
	state.Balances = append(state.Balances, balance)

	return nil
}

func loadTransaction(state *Snapshot, record []string) error {
	transaction := Transaction{
		ID: record[colTransactionID], Kind: TransactionKind(record[colTransactionKind]), ClientRef: record[colTransactionClientRef],
		CorrelationID: record[colTransactionCorrelation], ExternalTransferID: record[colTransactionExternal], IncomingTransferID: record[colTransactionIncoming],
	}

	state.Transactions = append(state.Transactions, transaction)

	return nil
}

func loadEntry(state *Snapshot, record []string) error {
	numbers, err := atois(record[colEntryID], record[colEntryAmount], record[colEntrySeq], record[colEntryBalanceAfter])
	if err != nil {
		return err
	}

	// Interned, the ids stop pinning the whole CSV line of every entry: a
	// long round has millions of entries over a few accounts, and two entries
	// per transaction.
	entry := Entry{
		ID: numbers[0], TransactionID: unique.Make(record[colEntryTransaction]).Value(), AccountID: unique.Make(record[colEntryAccount]).Value(),
		AmountCents: numbers[1], AccountSeq: numbers[2], BalanceAfterCents: numbers[3],
	}
	state.Entries = append(state.Entries, entry)

	return nil
}

func bandRows(state *Snapshot) [][]string {
	rows := make([][]string, 0, len(state.Bands))
	for _, b := range state.Bands {
		rows = append(rows, []string{itoa(b.Band), itoa(b.Count), itoa(b.SumCents), itoa(b.Hash)})
	}

	return rows
}

func loadBand(state *Snapshot, record []string) error {
	numbers, err := atois(record...)
	if err != nil {
		return err
	}

	state.Bands = append(state.Bands, Band{Band: numbers[0], Count: numbers[1], SumCents: numbers[2], Hash: numbers[3]})

	return nil
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}

func atois(fields ...string) ([]int64, error) {
	numbers := make([]int64, len(fields))

	for i, field := range fields {
		number, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("not an integer: %q", field)
		}

		numbers[i] = number
	}

	return numbers, nil
}
