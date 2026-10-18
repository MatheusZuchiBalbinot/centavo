// Package failpoint marks named points where the chaos lab can kill, delay or
// fail the process. Without the chaoslab build tag every point is a no-op the
// compiler removes.
package failpoint

import "context"

const (
	// TransferAfterDebitBeforeCredit is `transfer.after_debit_before_credit`:
	// between the debit and the credit entry.
	TransferAfterDebitBeforeCredit Name = "transfer.after_debit_before_credit"
	// APIAfterCommitBeforeResponse is `api.after_commit_before_response`: the
	// write is applied, the answer not sent.
	APIAfterCommitBeforeResponse Name = "api.after_commit_before_response"
	// RelayAfterPartnerCallBeforeSave is
	// `relay.after_partner_call_before_save`: the partner took the PIX, the
	// bank did not record it.
	RelayAfterPartnerCallBeforeSave Name = "relay.after_partner_call_before_save"
	// PixOutAfterSaveBeforePublish is `pixout.after_save_before_publish`: the
	// transfer is committed, the relay not told.
	PixOutAfterSaveBeforePublish Name = "pixout.after_save_before_publish"
	// WebhookAfterReceiveBeforeAck is `webhook.after_receive_before_ack`: the
	// event is taken in, the partner not answered.
	WebhookAfterReceiveBeforeAck Name = "webhook.after_receive_before_ack"
	// InboxAfterApplyBeforeMark is `inbox.after_apply_before_mark`: the event
	// is applied, not yet marked.
	InboxAfterApplyBeforeMark Name = "inbox.after_apply_before_mark"
	// ReconAfterComputeBeforeApply is `recon.after_compute_before_apply`: the
	// reconciler read the batch and booked nothing.
	ReconAfterComputeBeforeApply Name = "recon.after_compute_before_apply"
	// ReconMidApply is `recon.mid_apply`: between two entries of a
	// reconciliation batch.
	ReconMidApply Name = "recon.mid_apply"
	// LeaseAfterCheckBeforeWrite is `lease.after_check_before_write`: the lease
	// was checked in memory, the protected write not begun.
	LeaseAfterCheckBeforeWrite Name = "lease.after_check_before_write"
	// SweeperAfterStatusBeforeTransition is
	// `sweeper.after_status_before_transition`: the partner answered, the
	// sweeper wrote nothing yet.
	SweeperAfterStatusBeforeTransition Name = "sweeper.after_status_before_transition"
)

// Name identifies one failpoint.
type Name string

// Hit marks the passage through a named point. It returns an error only when
// an armed rule says so (the error action); kill never returns.
func Hit(ctx context.Context, name Name) error {
	return hit(ctx, name)
}
