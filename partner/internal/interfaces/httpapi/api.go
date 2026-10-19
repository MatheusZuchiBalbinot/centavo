package httpapi

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"centavo/partner/internal/engine"
	"centavo/partner/internal/modes"
	"centavo/partner/internal/truth"
)

const (
	maxBodyBytes = 16 << 10
	// lateAnswer is past the bank's 2 s timeout: the transfer is processed, the
	// bank never hears it.
	lateAnswer            = 3 * time.Second
	invalidKeyPrefix      = "invalid-"
	invalidKeyReason      = "invalid_destination_key"
	defaultStatementLimit = 100
	maxStatementLimit     = 1_000
)

// correlationPattern is the bank's format for correlation ids; anything else
// is dropped instead of echoed on the webhooks.
var correlationPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

const correlationHeader = "X-Correlation-Id"

// endToEndPattern accepts the bank's ids, E plus hex and an optional attempt.
var endToEndPattern = regexp.MustCompile(`^E[0-9a-fA-F-]{8,80}$`)

// TruthReader is what the API reads from the truth.
type TruthReader interface {
	Get(ctx context.Context, endToEndID string) (truth.Transfer, error)
	Statement(ctx context.Context, participantID string, afterSeq int64, limit int) ([]truth.StatementEntry, error)
	Truth(ctx context.Context, participantID string, since time.Time) (truth.Export, error)
	PendingDeliveries(ctx context.Context) (int, error)
	LastStatementSeq(ctx context.Context, participantID string) (int64, error)
}

// API serves the partner's client API (8081) and its admin port (9092).
type API struct {
	intake       truth.Intake
	reads        TruthReader
	modes        *modes.Registry
	participants map[string]bool
	logger       *slog.Logger
}

// APIConfig wires the handlers.
type APIConfig struct {
	Intake       truth.Intake
	Reads        TruthReader
	Modes        *modes.Registry
	Participants []string
	Logger       *slog.Logger
}

type submitRequest struct {
	EndToEndID     string `json:"end_to_end_id"`
	ParticipantID  string `json:"participant_id"`
	AmountCents    int64  `json:"amount_cents"`
	DestinationKey string `json:"destination_key"`
}

type statusResponse struct {
	EndToEndID  string       `json:"end_to_end_id"`
	Status      truth.Status `json:"status"`
	AmountCents int64        `json:"amount_cents"`
	Reason      string       `json:"reason,omitempty"`
}

type statementResponse struct {
	Entries      []truth.StatementEntry `json:"entries"`
	NextAfterSeq *int64                 `json:"next_after_seq"`
}

type incomingRequest struct {
	ParticipantID string `json:"participant_id"`
	EndToEndID    string `json:"end_to_end_id"`
	AccountID     string `json:"account_id"`
	AmountCents   int64  `json:"amount_cents"`
}

// pending counts the webhooks still to be sent, for the lab's quiescence.
type pendingResponse struct {
	PendingDeliveries int   `json:"pending_deliveries"`
	LastStatementSeq  int64 `json:"last_statement_seq"`
}

// NewAPI builds the handlers.
func NewAPI(config APIConfig) API {
	participants := make(map[string]bool, len(config.Participants))
	for _, participant := range config.Participants {
		participants[participant] = true
	}

	return API{intake: config.Intake, reads: config.Reads, modes: config.Modes, participants: participants, logger: config.Logger}
}

// Handler serves POST /v1/transfers, GET /v1/transfers/{e2e} and GET /v1/statements.
func (a API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+transfersRoute, a.submit)
	mux.HandleFunc("GET "+transferRoute, a.status)
	mux.HandleFunc("GET "+statementsRoute, a.statement)

	return withSecurityHeaders(mux)
}

// submit is idempotent by end_to_end_id: 202 the first time, 200 with the
// current status after that, 422 for a destination key that does not exist.
func (a API) submit(w http.ResponseWriter, r *http.Request) {
	var request submitRequest

	err := decode(w, r, &request)
	if err == nil {
		err = a.validate(request)
	}

	if err != nil {
		writeError(w, r, a.logger, errInvalidRequest)

		return
	}

	now := time.Now().UTC()
	decider := a.modes.For(request.ParticipantID, now)

	if refuseIfDown(w, decider) {
		return
	}

	transfer := newTransfer(request, decider, now)
	transfer.CorrelationID = correlationOf(r)

	result, err := a.intake.Submit(r.Context(), transfer)
	if err != nil {
		writeError(w, r, a.logger, fmt.Errorf("recording a transfer: %w", err))

		return
	}

	a.answerSubmit(r.Context(), w, result, decider)
}

func (a API) answerSubmit(ctx context.Context, w http.ResponseWriter, result truth.SubmitResult, decider modes.Decider) {
	transfer := result.Transfer
	response := statusResponse{EndToEndID: transfer.EndToEndID, Status: transfer.Status, AmountCents: transfer.AmountCents, Reason: transfer.RejectReason}

	if !result.IsNew {
		writeJSON(w, http.StatusOK, response)

		return
	}

	if transfer.Status == truth.StatusRejected {
		writeJSON(w, http.StatusUnprocessableEntity, response)

		return
	}

	if decider.ErrorAfterProcess() {
		writeKnownError(w, errAnswerAfterProcess)

		return
	}

	if decider.TimeoutAfterProcess() {
		sleep(ctx, lateAnswer)
	}

	writeJSON(w, http.StatusAccepted, response)
}

// newTransfer decides, at submission, whether the transfer is refused for an
// unknown key and whether the partner's double-processing bug will hit it.
func newTransfer(request submitRequest, decider modes.Decider, now time.Time) truth.Transfer {
	transfer := truth.Transfer{
		EndToEndID: request.EndToEndID, ParticipantID: request.ParticipantID, AmountCents: request.AmountCents,
		DestinationKey: request.DestinationKey, Status: truth.StatusProcessing, SettleAt: now.Add(decider.SettleDelay()), CreatedAt: now,
	}

	if strings.HasPrefix(request.DestinationKey, invalidKeyPrefix) {
		transfer.Status = truth.StatusRejected
		transfer.RejectReason = invalidKeyReason

		return transfer
	}

	transfer.IsDoubleProcessed = decider.DoubleProcess()

	return transfer
}

// status is the authoritative status: what a webhook can never override.
func (a API) status(w http.ResponseWriter, r *http.Request) {
	e2e := r.PathValue("e2e")

	transfer, err := a.reads.Get(r.Context(), e2e)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	decider := a.modes.For(transfer.ParticipantID, time.Now().UTC())
	if refuseIfDown(w, decider) {
		return
	}

	response := statusResponse{EndToEndID: transfer.EndToEndID, Status: transfer.Status, AmountCents: transfer.AmountCents, Reason: transfer.RejectReason}

	writeJSON(w, http.StatusOK, response)
}

func (a API) statement(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	participant := query.Get("participant_id")

	afterSeqText := cmp.Or(query.Get("after_seq"), "0")
	limitText := cmp.Or(query.Get("limit"), strconv.Itoa(defaultStatementLimit))

	afterSeq, errAfter := strconv.ParseInt(afterSeqText, 10, 64)
	limit, errLimit := strconv.Atoi(limitText)

	isValid := a.participants[participant] && errAfter == nil && errLimit == nil && afterSeq >= 0 && limit > 0 && limit <= maxStatementLimit
	if !isValid {
		writeError(w, r, a.logger, errInvalidStatementQuery)

		return
	}

	decider := a.modes.For(participant, time.Now().UTC())
	if refuseIfDown(w, decider) {
		return
	}

	entries, err := a.reads.Statement(r.Context(), participant, afterSeq, limit)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	response := statementResponse{Entries: append([]truth.StatementEntry{}, entries...)}

	if len(entries) == limit {
		last := entries[len(entries)-1].Seq
		response.NextAfterSeq = &last
	}

	writeJSON(w, http.StatusOK, response)
}

// incoming is a lab-only call: a PIX arrives for an account of the bank and the
// partner announces it by webhook.
func (a API) incoming(w http.ResponseWriter, r *http.Request) {
	var request incomingRequest

	err := decode(w, r, &request)

	isKnownSender := err == nil && a.participants[request.ParticipantID] && endToEndPattern.MatchString(request.EndToEndID)
	isValid := isKnownSender && request.AccountID != "" && request.AmountCents > 0
	if !isValid {
		writeError(w, r, a.logger, errInvalidIncoming)

		return
	}

	now := time.Now().UTC()
	incoming := truth.Incoming{
		EndToEndID: request.EndToEndID, ParticipantID: request.ParticipantID, AccountID: request.AccountID,
		AmountCents: request.AmountCents, CreatedAt: now, CorrelationID: correlationOf(r),
	}
	decider := a.modes.For(request.ParticipantID, now)

	isNew, err := a.intake.AddIncoming(r.Context(), truth.IncomingCommand{Incoming: incoming, EventID: engine.NewEventID(), Planner: decider.Plan})
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusCreated, map[string]bool{"accepted": true, "new": isNew})
}

// truthExport is the lab's view of everything the partner really did.
func (a API) truthExport(w http.ResponseWriter, r *http.Request) {
	participant := r.URL.Query().Get("participant_id")
	if !a.participants[participant] {
		writeError(w, r, a.logger, errUnknownParticipant)

		return
	}

	since, err := sinceOf(r)
	if err != nil {
		writeError(w, r, a.logger, errInvalidSince)

		return
	}

	export, err := a.reads.Truth(r.Context(), participant, since)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusOK, export)
}

// sinceOf reads the optional since of a truth export; without it the export
// starts at the beginning.
func sinceOf(r *http.Request) (time.Time, error) {
	since := r.URL.Query().Get("since")
	if since == "" {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339Nano, since)
}

// pending is what the lab's quiescence waits on: webhooks still to deliver
// and, for a participant, the statement entry its reconciliation must reach.
func (a API) pending(w http.ResponseWriter, r *http.Request) {
	count, err := a.reads.PendingDeliveries(r.Context())
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	lastSeq, err := a.reads.LastStatementSeq(r.Context(), r.URL.Query().Get("participant_id"))
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusOK, pendingResponse{PendingDeliveries: count, LastStatementSeq: lastSeq})
}

// correlationOf is the caller's correlation id, or none when it is missing or
// malformed.
func correlationOf(r *http.Request) string {
	received := r.Header.Get(correlationHeader)
	if !correlationPattern.MatchString(received) {
		return ""
	}

	return received
}

func (a API) validate(request submitRequest) error {
	isValid := endToEndPattern.MatchString(request.EndToEndID) && a.participants[request.ParticipantID] && request.AmountCents > 0 && request.DestinationKey != ""
	if !isValid {
		return errInvalidRequest
	}

	return nil
}

// refuseIfDown answers for an outage: 503, or a connection closed with no
// answer at all.
func refuseIfDown(w http.ResponseWriter, decider modes.Decider) bool {
	mode, isDown := decider.Outage()
	if !isDown {
		return false
	}

	isRefused := mode == modes.OutageRefuse && hangUp(w)
	if isRefused {
		return true
	}

	writeKnownError(w, errUnavailable)

	return true
}

// hangUp closes the connection without writing a byte.
func hangUp(w http.ResponseWriter) bool {
	hijacker, canHijack := w.(http.Hijacker)
	if !canHijack {
		return false
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		return false
	}

	// The closed connection is the answer; there is nobody left to report to.
	_ = conn.Close()

	return true
}

func decode(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// The status line is already written; an encode error means the client is gone.
	_ = json.NewEncoder(w).Encode(body)
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
