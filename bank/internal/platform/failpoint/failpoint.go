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
	// RelayAfterPartnerCallBeforeSave is FP03: the partner took the PIX, the bank did not record it.
	RelayAfterPartnerCallBeforeSave Name = "relay.after_partner_call_before_save"
	// PixOutAfterSaveBeforePublish is FP04: the transfer is committed, the relay not told.
	PixOutAfterSaveBeforePublish Name = "pixout.after_save_before_publish"
	// WebhookAfterReceiveBeforeAck is FP05: the event is taken in, the partner not answered.
	WebhookAfterReceiveBeforeAck Name = "webhook.after_receive_before_ack"
	// InboxAfterApplyBeforeMark is FP06: the event is applied, not yet marked.
	InboxAfterApplyBeforeMark Name = "inbox.after_apply_before_mark"
	// ReconAfterComputeBeforeApply is FP07: the reconciler read the batch and booked nothing.
	ReconAfterComputeBeforeApply Name = "recon.after_compute_before_apply"
	// ReconMidApply is FP08: between two entries of a reconciliation batch.
	ReconMidApply Name = "recon.mid_apply"
	// SweeperAfterStatusBeforeTransition is FP11: the partner answered, the sweeper wrote nothing yet.
	SweeperAfterStatusBeforeTransition Name = "sweeper.after_status_before_transition"
)

// Hit marks the passage through a named point. It returns an error only when
// an armed rule says so (the error action); kill never returns.
func Hit(ctx context.Context, name Name) error {
	return hit(ctx, name)
}
