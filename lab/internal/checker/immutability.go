package checker

import (
	"fmt"

	"centavo/lab/internal/snapshot"
)

// I8 is immutability (docs/invariants-and-checker.md, I8).
const (
	InvariantI8 Invariant = "I8"
	RuleI8Bands Rule      = "I8.bands"
)

// previousStateFile is the state the round before this one left, copied
// into the round directory before the check.
const previousStateFile = "state_before.json"

// checkImmutability compares every band the previous round saw complete with
// the same band now: an entry checked before can never change or vanish.
// The previous round's last band could still grow, so it is left out; a
// ledger that starts with another transaction is a rebuilt database.
func (e *evaluation) checkImmutability(previous *snapshot.LedgerState, bands []snapshot.Band, firstTransactionID string) {
	isComparable := previous != nil && len(previous.Bands) > 0 && previous.FirstTransactionID == firstTransactionID
	if !isComparable {
		return
	}

	current := map[int64]snapshot.Band{}
	for _, band := range bands {
		current[band.Band] = band
	}

	completeBands := previous.Bands[:len(previous.Bands)-1]

	for _, before := range completeBands {
		now := current[before.Band]
		if now == before {
			continue
		}

		message := fmt.Sprintf(
			"a faixa %d do ledger mudou desde a rodada anterior: %d lançamentos e soma %d antes, %d e %d agora",
			before.Band,
			before.Count,
			before.SumCents,
			now.Count,
			now.SumCents,
		)
		e.add(Violation{Invariant: InvariantI8, Rule: RuleI8Bands, TimeNS: unknownTime, Message: message})
	}
}
