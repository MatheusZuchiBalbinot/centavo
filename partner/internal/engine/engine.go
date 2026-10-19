// Package engine runs the partner's background work: deciding transfers and
// delivering webhooks.
package engine

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"centavo/partner/internal/modes"
	"centavo/partner/internal/truth"
)

const (
	tickInterval      = 100 * time.Millisecond
	batchSize         = 100
	deliveryTimeout   = 2 * time.Second
	maxAttempts       = 8
	firstRetryDelay   = 2 * time.Second
	signatureHeader   = "X-Partner-Signature"
	correlationHeader = "X-Correlation-Id"
	declinedReason    = "partner_declined"
	maxResponseBytes  = 64 << 10
)

// eventIDBytes is the randomness of a webhook id.
const eventIDBytes = 12

// Participant is a bank the partner serves.
type Participant struct {
	ID         string
	WebhookURL string
}

// Queue is what the engine reads and records about due work: the transfers
// waiting for a decision and the webhooks waiting to be delivered.
type Queue interface {
	Due(ctx context.Context, now time.Time, limit int) ([]truth.Transfer, error)
	DueDeliveries(ctx context.Context, now time.Time, limit int) ([]truth.Delivery, error)
	MarkDelivered(ctx context.Context, id int64) error
	Reschedule(ctx context.Context, id int64, next time.Time) error
	Abandon(ctx context.Context, id int64, at time.Time) error
}

// Engine decides transfers and delivers webhooks.
type Engine struct {
	queue        Queue
	saga         truth.Saga
	modes        *modes.Registry
	participants map[string]Participant
	secret       []byte
	http         *http.Client
	logger       *slog.Logger
}

// Config wires an engine.
type Config struct {
	Queue        Queue
	Saga         truth.Saga
	Modes        *modes.Registry
	Participants []Participant
	Secret       []byte
	Logger       *slog.Logger
}

// New builds an engine.
func New(config Config) *Engine {
	participants := make(map[string]Participant, len(config.Participants))
	for _, participant := range config.Participants {
		participants[participant.ID] = participant
	}

	return &Engine{
		queue: config.Queue, saga: config.Saga, modes: config.Modes, participants: participants, secret: config.Secret,
		http: &http.Client{Timeout: deliveryTimeout}, logger: config.Logger,
	}
}

// Run decides and delivers until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		e.decideDue(ctx)
		e.deliverDue(ctx)
	}
}

// NewEventID returns a random webhook id.
func NewEventID() string {
	var buffer [eventIDBytes]byte

	// crypto/rand never fails on supported platforms.
	_, _ = rand.Read(buffer[:])

	return "evt_" + hex.EncodeToString(buffer[:])
}

// Sign is t=<unix>,v1=<hex of HMAC-SHA256(secret, t + "." + body)>.
func Sign(secret []byte, at time.Time, body []byte) string {
	timestamp := strconv.FormatInt(at.Unix(), 10)

	mac := hmac.New(sha256.New, secret)

	// Writing to an in-memory hash never fails.
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)

	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func (e *Engine) decideDue(ctx context.Context) {
	now := time.Now().UTC()

	due, err := e.queue.Due(ctx, now, batchSize)
	if err != nil {
		e.logger.WarnContext(ctx, "listing due transfers", "error", err)

		return
	}

	for _, transfer := range due {
		err := e.decide(ctx, transfer, now)
		if err != nil {
			e.logger.WarnContext(ctx, "deciding a transfer", "end_to_end_id", transfer.EndToEndID, "error", err)
		}
	}
}

func (e *Engine) decide(ctx context.Context, transfer truth.Transfer, now time.Time) error {
	decider := e.modes.For(transfer.ParticipantID, now)

	if transfer.Status == truth.StatusSettled {
		return e.saga.Return(ctx, truth.ReturnCommand{EndToEndID: transfer.EndToEndID, Now: now, EventID: NewEventID(), Planner: decider.Plan})
	}

	if decider.Reject() {
		rejectCommand := truth.RejectCommand{EndToEndID: transfer.EndToEndID, Reason: declinedReason, Now: now, EventID: NewEventID(), Planner: decider.Plan}

		return e.saga.Reject(ctx, rejectCommand)
	}

	settleCommand := truth.SettleCommand{
		EndToEndID: transfer.EndToEndID, Now: now, ReturnAt: decider.ReturnAfterSettle(), EventID: NewEventID(), Planner: decider.Plan,
	}

	return e.saga.Settle(ctx, settleCommand)
}

func (e *Engine) deliverDue(ctx context.Context) {
	now := time.Now().UTC()

	due, err := e.queue.DueDeliveries(ctx, now, batchSize)
	if err != nil {
		e.logger.WarnContext(ctx, "listing due deliveries", "error", err)

		return
	}

	for _, delivery := range due {
		e.deliver(ctx, delivery, now)
	}
}

// deliver sends one webhook at least once: a failure is retried with
// exponential backoff, and given up after maxAttempts, like a real provider.
func (e *Engine) deliver(ctx context.Context, delivery truth.Delivery, now time.Time) {
	err := e.send(ctx, delivery, now)
	if err == nil {
		e.record(ctx, e.queue.MarkDelivered(ctx, delivery.ID))

		return
	}

	attempt := delivery.Attempts + 1
	isLastAttempt := attempt >= maxAttempts
	next := now.Add(firstRetryDelay << delivery.Attempts)

	e.logger.InfoContext(ctx, "webhook delivery failed", "event_id", delivery.EventID, "attempt", attempt, "abandoned", isLastAttempt, "error", err)

	if isLastAttempt {
		e.record(ctx, e.queue.Abandon(ctx, delivery.ID, next))

		return
	}

	e.record(ctx, e.queue.Reschedule(ctx, delivery.ID, next))
}

func (e *Engine) send(ctx context.Context, delivery truth.Delivery, now time.Time) error {
	participant, isKnown := e.participants[delivery.ParticipantID]
	if !isKnown {
		return fmt.Errorf("unknown participant %q", delivery.ParticipantID)
	}

	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, participant.WebhookURL, bytes.NewReader(delivery.Payload))
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(signatureHeader, Sign(e.secret, now, delivery.Payload))

	// The webhook carries the PIX's correlation id back, so the bank's logs
	// and failpoints follow the PIX into its webhooks.
	if delivery.CorrelationID != "" {
		request.Header.Set(correlationHeader, delivery.CorrelationID)
	}

	response, err := e.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// Drain so the connection can be reused; the body itself is ignored.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

	if response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook answered %d", response.StatusCode)
	}

	return nil
}

func (e *Engine) record(ctx context.Context, err error) {
	if err != nil {
		e.logger.WarnContext(ctx, "updating a delivery", "error", err)
	}
}
