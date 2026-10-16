package checker

import (
	"cmp"
	"encoding/json"
	"slices"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// unknownTime marks a violation the history cannot place in time, such as an
// entry written by an earlier round.
const unknownTime int64 = -1

// operation is everything the history says about one client operation.
type operation struct {
	op       string
	function history.Function
	invokeT  int64
	args     json.RawMessage
	result   history.Type
	value    json.RawMessage
	records  []history.Record
}

// isOpen reports an operation without a completion: the lab stopped before it
// ended, so it is as indeterminate as an info.
func (o *operation) isOpen() bool {
	return o.result == ""
}

// The checker decodes the history with its own types, written apart from the
// load generator, so a bug in one is not mirrored in the other.
type depositArgs struct {
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
}

type transferArgs struct {
	From        string `json:"from"`
	To          string `json:"to"`
	AmountCents int64  `json:"amount_cents"`
}

type createAccountValue struct {
	AccountID string `json:"account_id"`
}

type writeValue struct {
	TransactionID string `json:"transaction_id"`
}

type readBalanceArgs struct {
	AccountID string `json:"account_id"`
}

type readBalanceValue struct {
	BalanceCents int64 `json:"balance_cents"`
}

type statementValue struct {
	Entries []statementEntry `json:"entries"`
}

type resolveArgs struct {
	Key string `json:"key"`
}

type resolveValue struct {
	Outcome string `json:"outcome"`
}

// Outcomes resolve can give.
const (
	outcomeApplied = "applied"
	outcomeVoided  = "voided"
)

type statementEntry struct {
	AccountSeq        int64 `json:"account_seq"`
	AmountCents       int64 `json:"amount_cents"`
	BalanceAfterCents int64 `json:"balance_after_cents"`
}

// transactionKindOf maps a client write to the ledger transaction it produces
// (K in docs/invariants-and-checker.md, section 6).
var transactionKindOf = map[history.Function]string{
	history.FunctionDeposit:  snapshot.TransactionDeposit,
	history.FunctionTransfer: snapshot.TransactionInternalTransfer,
}

// operations indexes the history by operation id, in invoke order.
type operations struct {
	byID    map[string]*operation
	ordered []*operation
	lastT   int64
	// resolutions are the answers resolve gave for each key, in order.
	resolutions map[string][]string
}

func indexOperations(records []history.Record) operations {
	index := operations{byID: map[string]*operation{}, resolutions: map[string][]string{}}

	for _, record := range records {
		index.lastT = max(index.lastT, record.TimeNS)

		if record.Type == history.TypeInvoke {
			o := &operation{op: record.Op, function: record.F, invokeT: record.TimeNS, args: record.Value}
			index.byID[record.Op] = o
			index.ordered = append(index.ordered, o)
		}

		o, isKnown := index.byID[record.Op]
		if !isKnown {
			continue
		}

		o.records = append(o.records, record)

		if record.IsCompletion() {
			o.result = record.Type
			o.value = record.Value
		}
	}

	index.indexResolutions()

	return index
}

// indexResolutions collects what every successful resolve answered.
func (ops operations) indexResolutions() {
	for _, o := range ops.ordered {
		isAnswered := o.function == history.FunctionResolve && o.result == history.TypeOK
		if !isAnswered {
			continue
		}

		var args resolveArgs
		_ = json.Unmarshal(o.args, &args)

		var value resolveValue
		_ = json.Unmarshal(o.value, &value)

		ops.resolutions[args.Key] = append(ops.resolutions[args.Key], value.Outcome)
	}
}

// resolutionOf is the single answer resolve gave for a key, and whether
// there is one at all; answers that disagree are a violation of their own.
func (ops operations) resolutionOf(key string) (string, bool) {
	answers := ops.resolutions[key]
	if len(answers) == 0 {
		return "", false
	}

	return answers[0], true
}

// timeOf places a ledger transaction in time through the operation that
// caused it.
func (ops operations) timeOf(clientRef string) int64 {
	o, isKnown := ops.byID[clientRef]
	if !isKnown {
		return unknownTime
	}

	return o.invokeT
}

// ledger indexes the export.
type ledger struct {
	accounts          map[string]snapshot.Account
	accountIDs        []string
	balances          map[string]snapshot.Balance
	transactions      map[string]snapshot.Transaction
	transactionIDs    []string
	byClientRef       map[string][]snapshot.Transaction
	entriesByTx       map[string][]snapshot.Entry
	entriesByAccount  map[string][]snapshot.Entry
	externalCashID    string
	hasExternalCashID bool
}

func indexLedger(s snapshot.Snapshot) ledger {
	l := ledger{
		accounts:         map[string]snapshot.Account{},
		balances:         map[string]snapshot.Balance{},
		transactions:     map[string]snapshot.Transaction{},
		byClientRef:      map[string][]snapshot.Transaction{},
		entriesByTx:      map[string][]snapshot.Entry{},
		entriesByAccount: map[string][]snapshot.Entry{},
	}

	for _, account := range s.Accounts {
		l.accounts[account.ID] = account
		l.accountIDs = append(l.accountIDs, account.ID)

		isExternalCash := account.Kind == snapshot.KindSystem && account.SystemCode == snapshot.ExternalCashCode
		if isExternalCash {
			l.externalCashID = account.ID
			l.hasExternalCashID = true
		}
	}

	for _, balance := range s.Balances {
		l.balances[balance.AccountID] = balance
	}

	for _, transaction := range s.Transactions {
		l.transactions[transaction.ID] = transaction
		l.transactionIDs = append(l.transactionIDs, transaction.ID)
		l.byClientRef[transaction.ClientRef] = append(l.byClientRef[transaction.ClientRef], transaction)
	}

	// Entry ids grow in insertion order, which is the order the bank wrote them.
	entries := slices.Clone(s.Entries)
	slices.SortFunc(entries, func(a, b snapshot.Entry) int { return cmp.Compare(a.ID, b.ID) })

	for _, entry := range entries {
		l.entriesByTx[entry.TransactionID] = append(l.entriesByTx[entry.TransactionID], entry)
		l.entriesByAccount[entry.AccountID] = append(l.entriesByAccount[entry.AccountID], entry)
	}

	return l
}

// applications are the transactions of the operation's own kind (A(o)).
func (l ledger) applications(o *operation) []snapshot.Transaction {
	kind := transactionKindOf[o.function]

	var matching []snapshot.Transaction

	for _, transaction := range l.byClientRef[o.op] {
		if transaction.Kind == kind {
			matching = append(matching, transaction)
		}
	}

	return matching
}

func (l ledger) isCustomer(accountID string) bool {
	return l.accounts[accountID].Kind == snapshot.KindCustomer
}
