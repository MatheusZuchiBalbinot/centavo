// Package engine runs the partner's background work: deciding transfers and
// delivering webhooks (docs/architecture.md, section 4.4).
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
	tickInterval     = 100 * time.Millisecond
	batchSize        = 100
	deliveryTimeout  = 2 * time.Second
	maxAttempts      = 8
	firstRetryDelay  = 2 * time.Second
	signatureHeader  = "X-Partner-Signature"
	declinedReason   = "partner_declined"
	maxResponseBytes = 64 << 10
)

// Participant is a bank the partner serves.
type Participant struct {
	ID         string
	WebhookURL string
}

// Engine decides transfers and delivers webhooks.
type Engine struct {
	store        *truth.Store
	modes        *modes.Registry
	participants map[string]Participant
	secret       []byte
	http         *http.Client
	logger       *slog.Logger
}

// Config wires an engine.
type Config struct {
	Store        *truth.Store
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
		store:        config.Store,
		modes:        config.Modes,
		participants: participants,
		secret:       config.Secret,
		http:         &http.Client{Timeout: deliveryTimeout},
		logger:       config.Logger,
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
	var buffer [12]byte

	// crypto/rand never fails on supported platforms.
	_, _ = rand.Read(buffer[:])

	return "evt_" + hex.EncodeToString(buffer[:])
}

func (e *Engine) decideDue(ctx context.Context) {
	now := time.Now().UTC()

	due, err := e.store.Due(ctx, now, batchSize)
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
		return e.store.Return(ctx, truth.ReturnCommand{EndToEndID: transfer.EndToEndID, Now: now, EventID: NewEventID(), Planner: decider.Plan})
	}

	if decider.Reject() {
		return e.store.Reject(ctx, truth.RejectCommand{EndToEndID: transfer.EndToEndID, Reason: declinedReason, Now: now, EventID: NewEventID(), Planner: decider.Plan})
	}

	return e.store.Settle(ctx, truth.SettleCommand{EndToEndID: transfer.EndToEndID, Now: now, ReturnAt: decider.ReturnAfterSettle(), EventID: NewEventID(), Planner: decider.Plan})
}

func (e *Engine) deliverDue(ctx context.Context) {
	now := time.Now().UTC()

	due, err := e.store.DueDeliveries(ctx, now, batchSize)
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
		e.record(ctx, e.store.MarkDelivered(ctx, delivery.ID))

		return
	}

	attempt := delivery.Attempts + 1
	isLastAttempt := attempt >= maxAttempts
	next := now.Add(firstRetryDelay << delivery.Attempts)

	e.logger.InfoContext(ctx, "webhook delivery failed", "event_id", delivery.EventID, "attempt", attempt, "abandoned", isLastAttempt, "error", err)
	e.record(ctx, e.store.Reschedule(ctx, delivery.ID, next, isLastAttempt))
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

// Sign is t=<unix>,v1=<hex of HMAC-SHA256(secret, t + "." + body)>
// (docs/architecture.md, section 4.2).
func Sign(secret []byte, at time.Time, body []byte) string {
	timestamp := strconv.FormatInt(at.Unix(), 10)

	mac := hmac.New(sha256.New, secret)

	// Writing to an in-memory hash never fails.
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)

	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func (e *Engine) record(ctx context.Context, err error) {
	if err != nil {
		e.logger.WarnContext(ctx, "updating a delivery", "error", err)
	}
}
