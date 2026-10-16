// Package snapshot is the ledger export the checker reads: one CSV per table,
// taken from a single REPEATABLE READ snapshot. The ledger schema is an audit
// contract (docs/invariants-and-checker.md, section 2): changing it means
// changing this package on purpose.
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
)

// Dir is the export directory inside a round directory.
const Dir = "export"

const (
	accountsFile     = "accounts.csv"
	balancesFile     = "account_balances.csv"
	transactionsFile = "ledger_transactions.csv"
	entriesFile      = "ledger_entries.csv"
	metaFile         = "meta.json"
)

// Account kinds and the system account the checker reads by code.
const (
	KindCustomer = "customer"
	KindSystem   = "system"

	ExternalCashCode = "sys_external_cash"
)

// Ledger transaction kinds the checker attributes to client operations.
const (
	TransactionDeposit          = "deposit"
	TransactionInternalTransfer = "internal_transfer"
)

var ErrMalformedExport = errors.New("malformed export")

// Account is a row of accounts.
type Account struct {
	ID         string
	Kind       string
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
	ID            string
	Kind          string
	ClientRef     string
	CorrelationID string
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
}

type table struct {
	file   string
	header []string
	rows   func(s *Snapshot) [][]string
	load   func(s *Snapshot, record []string) error
}

var tables = []table{
	{file: accountsFile, header: []string{"id", "kind", "system_code"}, rows: accountRows, load: loadAccount},
	{file: balancesFile, header: []string{"account_id", "balance_cents", "last_entry_seq"}, rows: balanceRows, load: loadBalance},
	{file: transactionsFile, header: []string{"id", "kind", "client_ref", "correlation_id"}, rows: transactionRows, load: loadTransaction},
	{file: entriesFile, header: []string{"id", "transaction_id", "account_id", "amount_cents", "account_seq", "balance_after_cents"}, rows: entryRows, load: loadEntry},
}

// Write stores the snapshot in roundDir/export.
func Write(roundDir string, s Snapshot) error {
	dir := filepath.Join(roundDir, Dir)

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return fmt.Errorf("creating export directory: %w", err)
	}

	for _, t := range tables {
		err := writeTable(filepath.Join(dir, t.file), t.header, t.rows(&s))
		if err != nil {
			return err
		}
	}

	meta, err := json.MarshalIndent(s.Meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding export meta: %w", err)
	}

	return os.WriteFile(filepath.Join(dir, metaFile), append(meta, '\n'), 0o600)
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

func writeTable(path string, header []string, rows [][]string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: path is built from the round directory and a constant name.
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

func accountRows(s *Snapshot) [][]string {
	rows := make([][]string, 0, len(s.Accounts))
	for _, a := range s.Accounts {
		rows = append(rows, []string{a.ID, a.Kind, a.SystemCode})
	}

	return rows
}

func balanceRows(s *Snapshot) [][]string {
	rows := make([][]string, 0, len(s.Balances))
	for _, b := range s.Balances {
		rows = append(rows, []string{b.AccountID, itoa(b.BalanceCents), itoa(b.LastEntrySeq)})
	}

	return rows
}

func transactionRows(s *Snapshot) [][]string {
	rows := make([][]string, 0, len(s.Transactions))
	for _, t := range s.Transactions {
		rows = append(rows, []string{t.ID, t.Kind, t.ClientRef, t.CorrelationID})
	}

	return rows
}

func entryRows(s *Snapshot) [][]string {
	rows := make([][]string, 0, len(s.Entries))
	for _, e := range s.Entries {
		rows = append(rows, []string{itoa(e.ID), e.TransactionID, e.AccountID, itoa(e.AmountCents), itoa(e.AccountSeq), itoa(e.BalanceAfterCents)})
	}

	return rows
}

func loadAccount(s *Snapshot, record []string) error {
	s.Accounts = append(s.Accounts, Account{ID: record[0], Kind: record[1], SystemCode: record[2]})

	return nil
}

func loadBalance(s *Snapshot, record []string) error {
	numbers, err := atois(record[1], record[2])
	if err != nil {
		return err
	}

	s.Balances = append(s.Balances, Balance{AccountID: record[0], BalanceCents: numbers[0], LastEntrySeq: numbers[1]})

	return nil
}

func loadTransaction(s *Snapshot, record []string) error {
	s.Transactions = append(s.Transactions, Transaction{ID: record[0], Kind: record[1], ClientRef: record[2], CorrelationID: record[3]})

	return nil
}

func loadEntry(s *Snapshot, record []string) error {
	numbers, err := atois(record[0], record[3], record[4], record[5])
	if err != nil {
		return err
	}

	entry := Entry{ID: numbers[0], TransactionID: record[1], AccountID: record[2], AmountCents: numbers[1], AccountSeq: numbers[2], BalanceAfterCents: numbers[3]}
	s.Entries = append(s.Entries, entry)

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
