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

const (
	outcomeApplied = "applied"
	outcomeVoided  = "voided"
	// outcomeSuperseded: another request took the key, so this one never
	// applied; like voided, it counts as not applied.
	outcomeSuperseded = "superseded"
)

var transactionKindOf = map[history.Function]snapshot.TransactionKind{
	history.FunctionDeposit:  snapshot.TransactionDeposit,
	history.FunctionTransfer: snapshot.TransactionInternalTransfer,
	history.FunctionPixOut:   snapshot.TransactionPixOutReserve,
}

type operation struct {
	op       string
	function history.Function
	invokeT  int64
	args     json.RawMessage
	result   history.Type
	value    json.RawMessage
	// key is the Idempotency-Key the operation sent: its own id, or the key of
	// an earlier operation that a buggy client reused.
	key string
}

type reusedKeyArgs struct {
	ReusedKey string `json:"reused_key"`
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
	TransferID    string `json:"transfer_id"`
}

type pixOutArgs struct {
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
}

type pixInArgs struct {
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
	EndToEndID  string `json:"end_to_end_id"`
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
	// Op names the operation resolved; older histories only have the key,
	// which was then always the operation's own.
	Op string `json:"op"`
}

type resolveValue struct {
	Outcome string `json:"outcome"`
}

type statementEntry struct {
	AccountSeq        int64 `json:"account_seq"`
	AmountCents       int64 `json:"amount_cents"`
	BalanceAfterCents int64 `json:"balance_after_cents"`
}

type operations struct {
	byID        map[string]*operation
	ordered     []*operation
	lastT       int64
	resolutions map[string][]string
	// No invariant reads them; the report says how many a person launched,
	// because the seed does not repeat those.
	manualAttacks map[history.Function]int
}

// How the nemesis marks a fault launched from the panel, and the record that
// only closes a network fault already counted.
type nemesisMarks struct {
	Via   string `json:"via"`
	Phase string `json:"phase"`
}

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
	systemIDs         map[string]string
	transfers         []snapshot.ExternalTransfer
	transitions       map[string][]snapshot.Transition
	byTransfer        map[string][]snapshot.Transaction
	byIncoming        map[string][]snapshot.Transaction
	incomingByE2E     map[string][]snapshot.Incoming
}

// The lab stopped before it ended, so it is as indeterminate as an info.
func (o *operation) isOpen() bool {
	return o.result == ""
}

func isClientKind(kind snapshot.TransactionKind) bool {
	for _, clientKind := range transactionKindOf {
		if clientKind == kind {
			return true
		}
	}

	return false
}

func newOperations() operations {
	return operations{byID: map[string]*operation{}, resolutions: map[string][]string{}, manualAttacks: map[history.Function]int{}}
}

func indexOperations(records []history.Record) operations {
	index := newOperations()

	for _, record := range records {
		index.add(record)
	}

	index.indexResolutions()

	return index
}

// Only what the invariants read is kept: a long round's records would not
// fit in the checker's memory, so the report reads the few it shows again
// from the file.
func (ops *operations) add(record history.Record) {
	ops.lastT = max(ops.lastT, record.TimeNS)

	if record.Process.IsNemesis() {
		ops.countManual(record)
	}

	if record.Type == history.TypeInvoke {
		// validate runs once the whole history is read and reports a value
		// that does not decode; until then a bad one just reuses no key.
		var reused reusedKeyArgs
		_ = json.Unmarshal(record.Value, &reused)

		o := &operation{op: record.Op, function: record.F, invokeT: record.TimeNS, args: record.Value, key: cmp.Or(reused.ReusedKey, record.Op)}
		ops.byID[record.Op] = o
		ops.ordered = append(ops.ordered, o)
	}

	o, isKnown := ops.byID[record.Op]
	isKnownCompletion := isKnown && record.IsCompletion()

	if isKnownCompletion {
		o.result = record.Type
		o.value = record.Value
	}
}

func (ops *operations) countManual(record history.Record) {
	var marks nemesisMarks

	err := json.Unmarshal(record.Value, &marks)
	isManualStart := err == nil && marks.Via == viaManual && marks.Phase != phaseEnd

	if isManualStart {
		ops.manualAttacks[record.F]++
	}
}

func (ops operations) indexResolutions() {
	for _, o := range ops.ordered {
		isAnswered := o.function == history.FunctionResolve && o.result == history.TypeOK
		if !isAnswered {
			continue
		}

		var args resolveArgs
		decodeValidated(o.args, &args)

		var value resolveValue
		decodeValidated(o.value, &value)

		resolved := cmp.Or(args.Op, args.Key)
		ops.resolutions[resolved] = append(ops.resolutions[resolved], value.Outcome)
	}
}

// Answers that disagree are a violation of their own.
func (ops operations) resolutionOf(key string) (string, bool) {
	answers := ops.resolutions[key]
	if len(answers) == 0 {
		return "", false
	}

	return answers[0], true
}

func (ops operations) timeOf(clientRef string) int64 {
	o, isKnown := ops.byID[clientRef]
	if !isKnown {
		return unknownTime
	}

	return o.invokeT
}

func (l ledger) systemID(code string) string {
	return l.systemIDs[code]
}

func indexLedger(state snapshot.Snapshot) ledger {
	l := ledger{
		accounts: map[string]snapshot.Account{}, balances: map[string]snapshot.Balance{},
		transactions: map[string]snapshot.Transaction{}, byClientRef: map[string][]snapshot.Transaction{},
		systemIDs: map[string]string{}, transfers: state.Transfers, transitions: map[string][]snapshot.Transition{},
		byTransfer: map[string][]snapshot.Transaction{}, byIncoming: map[string][]snapshot.Transaction{}, incomingByE2E: map[string][]snapshot.Incoming{},
	}

	for _, transition := range state.Transitions {
		l.transitions[transition.TransferID] = append(l.transitions[transition.TransferID], transition)
	}

	for _, incoming := range state.Incoming {
		l.incomingByE2E[incoming.EndToEndID] = append(l.incomingByE2E[incoming.EndToEndID], incoming)
	}

	for _, account := range state.Accounts {
		l.accounts[account.ID] = account
		l.accountIDs = append(l.accountIDs, account.ID)

		if account.Kind == snapshot.KindSystem {
			l.systemIDs[account.SystemCode] = account.ID
		}

		isExternalCash := account.Kind == snapshot.KindSystem && account.SystemCode == snapshot.ExternalCashCode
		if isExternalCash {
			l.externalCashID = account.ID
			l.hasExternalCashID = true
		}
	}

	for _, balance := range state.Balances {
		l.balances[balance.AccountID] = balance
	}

	for _, transaction := range state.Transactions {
		l.transactions[transaction.ID] = transaction
		l.transactionIDs = append(l.transactionIDs, transaction.ID)
		l.byClientRef[transaction.ClientRef] = append(l.byClientRef[transaction.ClientRef], transaction)

		if transaction.ExternalTransferID != "" {
			l.byTransfer[transaction.ExternalTransferID] = append(l.byTransfer[transaction.ExternalTransferID], transaction)
		}

		if transaction.IncomingTransferID != "" {
			l.byIncoming[transaction.IncomingTransferID] = append(l.byIncoming[transaction.IncomingTransferID], transaction)
		}
	}

	// Nothing reads the snapshot's entries in their export order after this,
	// so the second index sorts them in place: a long round keeps two copies
	// of its entries instead of three.
	l.entriesByTx = groupEntries(slices.Clone(state.Entries), transactionOfEntry)
	l.entriesByAccount = groupEntries(state.Entries, accountOfEntry)

	return l
}

func transactionOfEntry(entry snapshot.Entry) string { return entry.TransactionID }

func accountOfEntry(entry snapshot.Entry) string { return entry.AccountID }

// Each group is a window of the sorted slice, so a long round pays for its
// entries once per index, not once per group. Entry ids grow in insertion
// order, the order the bank wrote them.
func groupEntries(sorted []snapshot.Entry, keyOf func(snapshot.Entry) string) map[string][]snapshot.Entry {
	slices.SortFunc(sorted, func(a, b snapshot.Entry) int {
		byKey := cmp.Compare(keyOf(a), keyOf(b))

		return cmp.Or(byKey, cmp.Compare(a.ID, b.ID))
	})

	groups := map[string][]snapshot.Entry{}
	start := 0

	for i := 1; i <= len(sorted); i++ {
		isRunOver := i == len(sorted) || keyOf(sorted[i]) != keyOf(sorted[start])
		if !isRunOver {
			continue
		}

		// The full slice expression caps the window, so an append to a group
		// can never write over the next one.
		groups[keyOf(sorted[start])] = sorted[start:i:i]
		start = i
	}

	return groups
}

func (e *evaluation) applications(o *operation) []snapshot.Transaction {
	kind := transactionKindOf[o.function]

	var matching []snapshot.Transaction

	for _, transaction := range e.ledger.byClientRef[o.key] {
		isOwn := transaction.Kind == kind && e.ownerOf(transaction) == o.op
		if isOwn {
			matching = append(matching, transaction)
		}
	}

	return matching
}

// A buggy write that reused a key with the same request: the bank answered
// with the stored response, so both operations are one application.
func (e *evaluation) isReplayOfTheKey(o *operation) bool {
	isReusedOK := o.key != o.op && o.result == history.TypeOK
	if !isReusedOK {
		return false
	}

	var value writeValue
	decodeValidated(o.value, &value)

	kind := transactionKindOf[o.function]

	for _, transaction := range e.ledger.byClientRef[o.key] {
		isTheStoredAnswer := transaction.ID == value.TransactionID || (value.TransferID != "" && transaction.ExternalTransferID == value.TransferID)
		isReplay := transaction.Kind == kind && e.ownerOf(transaction) != o.op && isTheStoredAnswer
		if isReplay {
			return true
		}
	}

	return false
}

// client_ref is the key, which names the operation that owns it, unless a
// buggy client took an idle key for another request: the bank then records
// that request's correlation id, which is its operation id.
func (e *evaluation) ownerOf(transaction snapshot.Transaction) string {
	sender, isKnown := e.ops.byID[transaction.CorrelationID]
	isSentWithThisKey := isKnown && sender.key == transaction.ClientRef
	if isSentWithThisKey {
		return sender.op
	}

	return transaction.ClientRef
}

func (l ledger) isCustomer(accountID string) bool {
	return l.accounts[accountID].Kind == snapshot.KindCustomer
}
