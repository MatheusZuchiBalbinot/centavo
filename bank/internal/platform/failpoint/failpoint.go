// Package failpoint marks named points where the chaos lab can kill, delay
// or fail the process (docs/chaos-bot.md, section 5). Without the chaoslab
// build tag every point is a no-op the compiler removes.
package failpoint

import "context"

// Name identifies one failpoint of docs/failure-catalog.md, section 2.
type Name string

const (
	// TransferAfterDebitBeforeCredit is FP01: between the debit and the credit entry.
	TransferAfterDebitBeforeCredit Name = "transfer.after_debit_before_credit"
	// APIAfterCommitBeforeResponse is FP02: the write is applied, the answer not sent.
	APIAfterCommitBeforeResponse Name = "api.after_commit_before_response"
)

// Hit marks the passage through a named point. It returns an error only when
// an armed rule says so (the error action); kill never returns.
func Hit(ctx context.Context, name Name) error {
	return hit(ctx, name)
}
