package application

import (
	"errors"
	"fmt"

	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

// errUnknownTransferStatus is a transfer in a status the reconciliation has no
// rule for: the batch stops rather than move money by default.
var errUnknownTransferStatus = errors.New("reconciliation has no rule for the transfer's status")

// The kinds of a partner statement line.
const (
	StatementSettlement StatementKind = "settlement"
	StatementReturn     StatementKind = "return"
	StatementIncoming   StatementKind = "incoming"
)

// The verdicts of reconciling one statement line.
const (
	DivergenceMatched           Divergence = "matched"
	DivergenceMissingSettlement Divergence = "missing_settlement"
	DivergenceMissingReturn     Divergence = "missing_return"
	DivergenceMissingIncoming   Divergence = "missing_incoming"
	DivergenceDuplicate         Divergence = "duplicate_at_partner"
	DivergenceAmountMismatch    Divergence = "amount_mismatch"
	DivergenceUnknownEntry      Divergence = "unknown_entry"
)

// The actions recorded with each item.
const (
	actionNone     ReconciliationAction = "none"
	actionWalk     ReconciliationAction = "walk_to_partner_status"
	actionCredit   ReconciliationAction = "credit_incoming"
	actionSuspense ReconciliationAction = "suspense"
	// actionWalkAndSuspense settles the amount the bank knows and books the
	// rest of what the partner moved to suspense.
	actionWalkAndSuspense ReconciliationAction = "walk_and_suspense"
)

// StatementKind is a line of the partner's statement.
type StatementKind string

// StatementEntry is one line of the partner's statement, numbered by seq.
type StatementEntry struct {
	Seq         int64
	Kind        StatementKind
	EndToEndID  transfer.EndToEndID
	AmountCents money.Cents
	// AccountID is the destination of an incoming PIX.
	AccountID string
	// CorrelationID is the one the PIX came with, its client_ref for a PIX
	// out: the reconciler's failpoints match it, like any other step of the PIX.
	CorrelationID string
}

// Divergence is how a statement entry compares with the ledger.
type Divergence string

// ReconciliationAction is what the reconciler did about one statement entry.
type ReconciliationAction string

// entryFacts are what the ledger says about one statement entry.
type entryFacts struct {
	transfer    ExternalTransfer
	hasTransfer bool
	// priorItems counts entries of the same kind and end_to_end_id already
	// reconciled: a second settlement is the partner's double processing.
	priorItems     int
	isCredited     bool
	isAccountKnown bool
}

// verdict is what the reconciler books for an entry.
type verdict struct {
	divergence Divergence
	action     ReconciliationAction
	// walkTo is the partner status to walk the transfer to, if any.
	walkTo transfer.PartnerStatus
	// suspenseCents moves money from the partner reserve mirror to suspense:
	// positive when the partner paid out more than the ledger knows (a
	// receivable), negative when it paid in more (a payable).
	suspenseCents money.Cents
}

// classify compares one statement entry with the ledger. It never touches a
// customer's account without a known cause: what has no explanation goes to
// suspense (an unexplained divergence goes to suspense, never to a customer).
func classify(entry StatementEntry, facts entryFacts) (verdict, error) {
	switch entry.Kind {
	case StatementSettlement:
		return classifySettlement(entry, facts)
	case StatementReturn:
		return classifyReturn(entry, facts)
	case StatementIncoming:
		return classifyIncoming(entry, facts), nil
	default:
		return verdict{divergence: DivergenceUnknownEntry, action: actionNone}, nil
	}
}

func classifySettlement(entry StatementEntry, facts entryFacts) (verdict, error) {
	paidOut := verdict{divergence: DivergenceUnknownEntry, action: actionSuspense, suspenseCents: entry.AmountCents}

	if !facts.hasTransfer {
		return paidOut, nil
	}

	if facts.priorItems > 0 {
		paidOut.divergence = DivergenceDuplicate

		return paidOut, nil
	}

	gap := entry.AmountCents - facts.transfer.Amount.Cents()
	if gap != 0 {
		return verdict{divergence: DivergenceAmountMismatch, action: actionWalkAndSuspense, walkTo: transfer.PartnerSettled, suspenseCents: gap}, nil
	}

	switch facts.transfer.Status {
	case transfer.StatusConfirmed, transfer.StatusReversed:
		return verdict{divergence: DivergenceMatched, action: actionNone}, nil
	case transfer.StatusCreated, transfer.StatusSent:
		return verdict{divergence: DivergenceMissingSettlement, action: actionWalk, walkTo: transfer.PartnerSettled}, nil
	case transfer.StatusFailed:
		// The bank refunded a PIX the partner paid (in a naive relay): the
		// money is owed back to the bank and waits in suspense.
		paidOut.divergence = DivergenceMissingSettlement

		return paidOut, nil
	default:
		return verdict{}, fmt.Errorf("%w: %s", errUnknownTransferStatus, facts.transfer.Status)
	}
}

func classifyReturn(entry StatementEntry, facts entryFacts) (verdict, error) {
	paidIn := verdict{divergence: DivergenceUnknownEntry, action: actionSuspense, suspenseCents: -entry.AmountCents}

	if !facts.hasTransfer {
		return paidIn, nil
	}

	if facts.priorItems > 0 {
		paidIn.divergence = DivergenceDuplicate

		return paidIn, nil
	}

	switch facts.transfer.Status {
	case transfer.StatusReversed:
		return verdict{divergence: DivergenceMatched, action: actionNone}, nil
	case transfer.StatusCreated, transfer.StatusSent, transfer.StatusConfirmed:
		return verdict{divergence: DivergenceMissingReturn, action: actionWalk, walkTo: transfer.PartnerReturned}, nil
	case transfer.StatusFailed:
		// A return of a PIX the bank already refunded: the partner paid the
		// money in, and it waits in suspense.
		paidIn.divergence = DivergenceMissingReturn

		return paidIn, nil
	default:
		return verdict{}, fmt.Errorf("%w: %s", errUnknownTransferStatus, facts.transfer.Status)
	}
}

func classifyIncoming(entry StatementEntry, facts entryFacts) verdict {
	if facts.isCredited {
		return verdict{divergence: DivergenceMatched, action: actionNone}
	}

	if !facts.isAccountKnown {
		return verdict{divergence: DivergenceUnknownEntry, action: actionSuspense, suspenseCents: -entry.AmountCents}
	}

	return verdict{divergence: DivergenceMissingIncoming, action: actionCredit}
}
