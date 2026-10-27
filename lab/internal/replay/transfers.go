package replay

import (
	"encoding/json"
	"time"

	"centavo/lab/internal/history"
)

// maxTransfersPerSecond bounds the money moves a replay keeps. At the lab's
// usual load every one of them fits; a heavier round keeps the first ones of
// each second, so the timeline still covers the whole round.
const maxTransfersPerSecond = 12

// Transfer is one move of money a client asked for, an internal transfer or a
// PIX out, and how it ended.
type Transfer struct {
	TMillis     int64            `json:"t_ms"`
	Kind        history.Function `json:"kind"`
	Outcome     history.Type     `json:"outcome"`
	Op          string           `json:"op"`
	From        string           `json:"from"`
	To          string           `json:"to,omitempty"`
	Destination string           `json:"destination,omitempty"`
	AmountCents int64            `json:"amount_cents"`
	LatencyMs   float64          `json:"latency_ms"`
	// Error is the bank's code for a refusal, or the kind of failure when
	// the client got no answer.
	Error string `json:"error,omitempty"`
}

// The arguments a transfer and a PIX out carry in their invoke record.
type moveArgs struct {
	From           string `json:"from"`
	To             string `json:"to"`
	AccountID      string `json:"account_id"`
	DestinationKey string `json:"destination_key"`
	AmountCents    int64  `json:"amount_cents"`
}

type moveError struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

type pendingMove struct {
	invokedAt int64
	args      moveArgs
}

func isMove(function history.Function) bool {
	return function == history.FunctionTransfer || function == history.FunctionPixOut
}

func (b *Builder) holdMove(record history.Record) {
	var args moveArgs

	// A value that does not decode still shows the move, without its accounts.
	_ = json.Unmarshal(record.Value, &args)

	b.moves[record.Op] = pendingMove{invokedAt: record.TimeNS, args: args}
}

func (b *Builder) closeMove(record history.Record) {
	pending, isKnown := b.moves[record.Op]
	if !isKnown {
		return
	}

	delete(b.moves, record.Op)

	second := int(record.TimeNS / int64(time.Second))
	if b.movesBySecond[second] >= maxTransfersPerSecond {
		return
	}

	b.movesBySecond[second]++

	var failure moveError

	_ = json.Unmarshal(record.Error, &failure)

	transfer := Transfer{
		TMillis: record.TimeNS / int64(time.Millisecond), Kind: record.F, Outcome: record.Type, Op: record.Op,
		From: fromOf(pending.args), To: pending.args.To, Destination: pending.args.DestinationKey, AmountCents: pending.args.AmountCents,
		LatencyMs: float64(record.TimeNS-pending.invokedAt) / float64(time.Millisecond), Error: errorOf(failure),
	}

	b.replay.Transfers = append(b.replay.Transfers, transfer)
}

// A transfer names its source account from; a PIX out names it account_id.
func fromOf(args moveArgs) string {
	if args.From != "" {
		return args.From
	}

	return args.AccountID
}

func errorOf(failure moveError) string {
	if failure.Code != "" {
		return failure.Code
	}

	return failure.Kind
}
