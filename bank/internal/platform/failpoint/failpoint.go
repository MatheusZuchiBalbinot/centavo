// Package failpoint marks named points where the chaos lab can kill, delay or
// fail the process. Without the chaoslab build tag every point is a no-op the
// compiler removes.
package failpoint

import "context"

const (
	// Between the debit and the credit entry.
	TransferAfterDebitBeforeCredit Name = "transfer.after_debit_before_credit"
	// The write is applied, the answer not sent.
	APIAfterCommitBeforeResponse Name = "api.after_commit_before_response"
	// The partner took the PIX, the bank did not record it.
	RelayAfterPartnerCallBeforeSave Name = "relay.after_partner_call_before_save"
	// The transfer is committed, the relay not told.
	PixOutAfterSaveBeforePublish Name = "pixout.after_save_before_publish"
	// The event is taken in, the partner not answered.
	WebhookAfterReceiveBeforeAck Name = "webhook.after_receive_before_ack"
	// The event is applied, not yet marked.
	InboxAfterApplyBeforeMark Name = "inbox.after_apply_before_mark"
	// The reconciler read the batch and booked nothing.
	ReconAfterComputeBeforeApply Name = "recon.after_compute_before_apply"
	// Between two entries of a reconciliation batch.
	ReconMidApply Name = "recon.mid_apply"
	// The lease was checked in memory, the protected write not begun.
	LeaseAfterCheckBeforeWrite Name = "lease.after_check_before_write"
	// The partner answered, the sweeper wrote nothing yet.
	SweeperAfterStatusBeforeTransition Name = "sweeper.after_status_before_transition"
)

type Name string

// Returns an error only when an armed rule says so (the error action); kill
// never returns.
func Hit(ctx context.Context, name Name) error {
	return hit(ctx, name)
}
