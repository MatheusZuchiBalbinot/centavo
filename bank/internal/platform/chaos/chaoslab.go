//go:build chaoslab

package chaos

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"centavo/bank/internal/platform/chaosbuild"
	"centavo/bank/internal/platform/clock"
	"centavo/bank/internal/platform/failpoint"
	"centavo/bank/internal/platform/httpserver"
	"centavo/bank/internal/platform/labevent"
)

// Routes of the chaos server.
const (
	failpointsRoute = "/chaos/failpoints"
	failpointRoute  = "/chaos/failpoints/{name}"
	clockRoute      = "/chaos/clock"
)

const (
	tokenHeader  = "X-Chaos-Token" //nolint:gosec // G101: a header name, not a secret.
	maxRuleBytes = 4 << 10
	procDir      = "/proc"
	cmdlineFile  = "cmdline"
)

var (
	errNoMainProcess = errors.New("no main process found in /proc")
	errBadRule       = errors.New("rule body must be one JSON rule")
	errBadSkew       = errors.New(`clock body must be {"offset": "+120s", "ttl": "10m"}`)
)

type skewRequest struct {
	Offset string `json:"offset"`
	TTL    string `json:"ttl"`
}

// Setup records fired failpoints in the shared volume and arms the rules of
// CENTAVO_FAILPOINTS.
func Setup(config Config) error {
	failpoint.ConfigureEvents(failpoint.EventsConfig{Dir: EventsDir, Service: config.Service, Instance: config.Instance})
	labevent.Configure(labevent.Config{Dir: EventsDir, Service: config.Service, Instance: config.Instance})

	return failpoint.ArmFromSpec(config.FailpointSpec, config.Seed, time.Now())
}

// Server is the chaos control server.
func Server(config Config) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+failpointRoute, armFailpoint)
	mux.HandleFunc("GET "+failpointsRoute, listFailpoints)
	mux.HandleFunc("DELETE "+failpointsRoute, clearFailpoints)
	mux.HandleFunc("PUT "+clockRoute, setClock)
	mux.HandleFunc("DELETE "+clockRoute, clearClock)

	return httpserver.New(Addr, requireToken(config.Token, mux))
}

// KillMain finds the main process of the container (the binary started by tini,
// not this chaos-kill invocation) and sends it SIGKILL. The images have no
// shell and no kill command, so the binary kills itself this way.
func KillMain(binary string) error {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return fmt.Errorf("listing processes: %w", err)
	}

	self := os.Getpid()

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		if pid == self {
			continue
		}

		if isMainProcess(pid, binary) {
			return syscall.Kill(pid, syscall.SIGKILL)
		}
	}

	return errNoMainProcess
}

// requireToken is the safety rule that every chaos call carries the chaos
// token: every control call carries the secret.
func requireToken(token string, next http.Handler) http.Handler {
	expected := []byte(token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received := []byte(r.Header.Get(tokenHeader))

		isAuthorized := len(expected) > 0 && subtle.ConstantTimeCompare(received, expected) == 1
		if !isAuthorized {
			http.Error(w, "missing or wrong "+tokenHeader, http.StatusUnauthorized)

			return
		}

		// The sentinel marks the chaos server in the binary for the CI check.
		w.Header().Set("X-Chaos-Build", chaosbuild.Sentinel())
		next.ServeHTTP(w, r)
	})
}

func armFailpoint(w http.ResponseWriter, r *http.Request) {
	var rule failpoint.Rule

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRuleBytes))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&rule)
	if err != nil {
		http.Error(w, errBadRule.Error(), http.StatusBadRequest)

		return
	}

	name := failpoint.Name(r.PathValue("name"))

	err = failpoint.Arm(name, rule, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)

		return
	}

	writeJSON(w, map[string]string{"armed": string(name)})
}

func listFailpoints(w http.ResponseWriter, _ *http.Request) {
	rules := failpoint.Rules(time.Now())
	writeJSON(w, rules)
}

func clearFailpoints(w http.ResponseWriter, _ *http.Request) {
	failpoint.DisarmAll()
	writeJSON(w, map[string]bool{"cleared": true})
}

// setClock skews this process's wall clock.
func setClock(w http.ResponseWriter, r *http.Request) {
	var request skewRequest

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRuleBytes))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&request)
	if err != nil {
		http.Error(w, errBadSkew.Error(), http.StatusBadRequest)

		return
	}

	offset, errOffset := time.ParseDuration(request.Offset)
	ttl, errTTL := time.ParseDuration(request.TTL)

	isMalformedSkew := errOffset != nil || errTTL != nil
	if isMalformedSkew {
		http.Error(w, errBadSkew.Error(), http.StatusBadRequest)

		return
	}

	err = clock.SetSkew(offset, ttl, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)

		return
	}

	writeJSON(w, map[string]string{"offset": offset.String()})
}

func clearClock(w http.ResponseWriter, _ *http.Request) {
	clock.ClearSkew()
	writeJSON(w, map[string]bool{"cleared": true})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")

	// An encode error means the lab went away; there is nobody to tell.
	_ = json.NewEncoder(w).Encode(body)
}

// isMainProcess matches "<binary> api|worker|serve", never healthcheck or
// another chaos-kill.
func isMainProcess(pid int, binary string) bool {
	path := filepath.Join(procDir, strconv.Itoa(pid), cmdlineFile)

	cmdline, err := os.ReadFile(path) //nolint:gosec // G304: /proc/<pid>/cmdline, built from a pid.
	if err != nil {
		return false
	}

	fields := bytes.Split(bytes.TrimRight(cmdline, "\x00"), []byte{0})
	isOtherCommand := len(fields) < 2 || string(fields[0]) != binary
	if isOtherCommand {
		return false
	}

	role := string(fields[1])

	return role == "api" || role == "worker" || role == "serve"
}
