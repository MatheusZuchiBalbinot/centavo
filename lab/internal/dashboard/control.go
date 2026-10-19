package dashboard

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"centavo/lab/internal/attacks"
	"centavo/lab/internal/runner"
)

// Bounds of the control API.
const (
	// maxBodyBytes is far above the largest valid start request.
	maxBodyBytes = 1 << 10
	jsonMedia    = "application/json"
	// Round list pages.
	defaultPageSize = 25
	maxPageSize     = 100
	// The audit log keeps the network of the caller, not the address.
	ipv4LoggedBits = 24
	ipv6LoggedBits = 48
	ipv4Bits       = 32
	ipv6Bits       = 128
)

// RoundRunner starts and stops the rounds the panel asks for.
type RoundRunner interface {
	Start(request runner.Request) (runner.State, error)
	Stop() (runner.State, error)
	State() runner.State
}

type labResponse struct {
	Status          runner.Status `json:"status"`
	Attack          attacks.ID    `json:"attack,omitempty"`
	Scenario        string        `json:"scenario,omitempty"`
	DurationSeconds int           `json:"duration_seconds,omitempty"`
	StartedAt       *time.Time    `json:"started_at,omitempty"`
}

type attackResponse struct {
	ID        attacks.ID `json:"id"`
	Scenarios []string   `json:"scenarios"`
}

type menuResponse struct {
	Attacks          []attackResponse `json:"attacks"`
	DurationsSeconds []int            `json:"durations_seconds"`
}

type startRequest struct {
	Attack          attacks.ID `json:"attack"`
	DurationSeconds int        `json:"duration_seconds"`
}

type roundsPage struct {
	Rounds     []summary `json:"rounds"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// control serves the routes that start and stop rounds.
type control struct {
	panel   panelHandlers
	runner  RoundRunner
	limiter *windowLimiter
}

func (c control) lab(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, labResponseOf(c.runner.State()))
}

func (c control) menu(w http.ResponseWriter, _ *http.Request) {
	response := menuResponse{}

	for _, attack := range attacks.Menu() {
		response.Attacks = append(response.Attacks, attackResponse{ID: attack.ID, Scenarios: attack.Scenarios})
	}

	for _, duration := range attacks.Durations() {
		response.DurationsSeconds = append(response.DurationsSeconds, int(duration.Seconds()))
	}

	writeJSON(w, http.StatusOK, response)
}

// start picks a scenario of the attack and starts the round. It answers
// 202: the round runs for minutes, and GET /api/lab follows it.
func (c control) start(w http.ResponseWriter, r *http.Request) {
	var body startRequest

	err := decodeJSON(w, r, &body)
	if err != nil {
		c.panel.writeError(w, r, err)

		return
	}

	retryAfter := c.limiter.wait()
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		c.panel.writeError(w, r, errRateLimited)

		return
	}

	duration := time.Duration(body.DurationSeconds) * time.Second

	scenario, err := attacks.Pick(body.Attack, duration, pickIndex)
	if err != nil {
		c.panel.writeError(w, r, err)

		return
	}

	request := runner.Request{Attack: body.Attack, Scenario: scenario, Duration: duration}

	state, err := c.runner.Start(request)
	if err != nil {
		c.panel.writeError(w, r, err)

		return
	}

	c.limiter.record()
	c.panel.config.Logger.InfoContext(r.Context(), "round started from the panel",
		"attack", request.Attack, "scenario", request.Scenario, "duration", request.Duration, "network", networkOf(r.RemoteAddr))
	writeJSON(w, http.StatusAccepted, labResponseOf(state))
}

func (c control) stop(w http.ResponseWriter, r *http.Request) {
	err := checkOrigin(r)
	if err != nil {
		c.panel.writeError(w, r, err)

		return
	}

	state, err := c.runner.Stop()
	if err != nil {
		c.panel.writeError(w, r, err)

		return
	}

	c.panel.config.Logger.InfoContext(r.Context(), "round stopped from the panel",
		"scenario", state.Request.Scenario, "network", networkOf(r.RemoteAddr))
	writeJSON(w, http.StatusAccepted, labResponseOf(state))
}

func labResponseOf(state runner.State) labResponse {
	if state.Status == runner.StatusIdle {
		return labResponse{Status: state.Status}
	}

	startedAt := state.StartedAt

	return labResponse{
		Status: state.Status, Attack: state.Request.Attack, Scenario: state.Request.Scenario,
		DurationSeconds: int(state.Request.Duration.Seconds()), StartedAt: &startedAt,
	}
}

func pageSizeOf(raw string) (int, error) {
	if raw == "" {
		return defaultPageSize, nil
	}

	size, err := strconv.Atoi(raw)
	isOutOfRange := err != nil || size < 1 || size > maxPageSize

	if isOutOfRange {
		return 0, errInvalidRequest
	}

	return size, nil
}

// decodeJSON reads one small JSON object from a same-origin request. Asking
// for application/json also keeps other sites out: a cross-site form cannot
// send it without a CORS preflight, which the panel never answers.
func decodeJSON(w http.ResponseWriter, r *http.Request, into any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	isJSON := err == nil && mediaType == jsonMedia

	if !isJSON {
		return errUnsupportedMedia
	}

	err = checkOrigin(r)
	if err != nil {
		return err
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	err = decoder.Decode(into)

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return errBodyTooLarge
	}

	hasTrailingData := err == nil && decoder.More()
	if err != nil || hasTrailingData {
		return errInvalidRequest
	}

	return nil
}

// checkOrigin refuses a state change sent by a page of another site. Browsers
// send Origin on every POST; a request without one did not come from a page.
func checkOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}

	parsed, err := url.Parse(origin)
	isSameHost := err == nil && parsed.Host == r.Host

	if !isSameHost {
		return errForeignOrigin
	}

	return nil
}

// pickIndex chooses the scenario of a round; it is not a secret, so the
// non-cryptographic generator is enough.
func pickIndex(n int) int {
	return rand.IntN(n) //nolint:gosec // G404: picking a test scenario, not a secret.
}

// networkOf keeps the caller's network for the audit log, never the full
// address.
func networkOf(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return ""
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}

	if ip.To4() != nil {
		return ip.Mask(net.CIDRMask(ipv4LoggedBits, ipv4Bits)).String() + "/" + strconv.Itoa(ipv4LoggedBits)
	}

	return ip.Mask(net.CIDRMask(ipv6LoggedBits, ipv6Bits)).String() + "/" + strconv.Itoa(ipv6LoggedBits)
}
