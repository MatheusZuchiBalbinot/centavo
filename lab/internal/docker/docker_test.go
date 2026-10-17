package docker_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"centavo/lab/internal/docker"
)

// fakeEngine answers the few Engine API calls the bot makes.
type fakeEngine struct {
	mu       sync.Mutex
	labels   map[string]map[string]string
	paused   map[string]bool
	execs    []string
	started  []string
	unpaused []string
	pauses   []string
}

func newFakeEngine() *fakeEngine {
	target := func(service string) map[string]string {
		return map[string]string{"centavo.chaos.target": "true", "com.docker.compose.project": "centavo", "com.docker.compose.service": service}
	}

	return &fakeEngine{
		labels: map[string]map[string]string{
			"api":   target("bank-api"),
			"other": {"centavo.chaos.target": "true", "com.docker.compose.project": "elsewhere", "com.docker.compose.service": "bank-api"},
			"lab":   {"com.docker.compose.project": "centavo", "com.docker.compose.service": "lab"},
		},
		paused: map[string]bool{"api": true},
	}
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	switch {
	case r.URL.Path == "/containers/json":
		listed := []map[string]any{{"Id": "api", "Labels": f.labels["api"]}}
		_ = json.NewEncoder(w).Encode(listed)
	case len(parts) == 3 && parts[2] == "json":
		labels, isKnown := f.labels[parts[1]]
		if !isKnown {
			http.NotFound(w, r)

			return
		}

		details := map[string]any{"Id": parts[1], "RestartCount": 2, "State": map[string]any{"Running": true, "Paused": f.paused[parts[1]], "Health": map[string]string{"Status": "healthy"}}, "Config": map[string]any{"Labels": labels}}
		_ = json.NewEncoder(w).Encode(details)
	case len(parts) == 3 && parts[2] == "exec":
		var body struct{ Cmd []string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.execs = append(f.execs, parts[1]+":"+strings.Join(body.Cmd, " "))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id": "exec-1"}`))
	case parts[0] == "exec":
		w.WriteHeader(http.StatusOK)
	case len(parts) == 3 && parts[2] == "start":
		f.started = append(f.started, parts[1])
		w.WriteHeader(http.StatusNotModified)
	case len(parts) == 3 && parts[2] == "pause":
		f.pauses = append(f.pauses, parts[1])
		f.paused[parts[1]] = true
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[2] == "unpause":
		f.unpaused = append(f.unpaused, parts[1])
		f.paused[parts[1]] = false
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func newClient(t *testing.T, engine *fakeEngine) docker.Client {
	t.Helper()

	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	client, err := docker.New("tcp://"+strings.TrimPrefix(server.URL, "http://"), "centavo")
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func TestFind_ReadsTheTargetState(t *testing.T) {
	client := newClient(t, newFakeEngine())

	target, err := client.Find(t.Context(), "bank-api")
	if err != nil {
		t.Fatal(err)
	}

	isRead := target.ID == "api" && target.IsPaused && target.RestartCount == 2 && target.Health == "healthy" && !target.IsHealthy()
	if !isRead {
		t.Fatalf("target: %+v", target)
	}

	_, err = client.Find(t.Context(), "partner")
	if !errors.Is(err, docker.ErrUnknownService) {
		t.Fatalf("unknown service: got %v", err)
	}
}

func TestActions_RunOnlyOnTargetsOfTheProject(t *testing.T) {
	engine := newFakeEngine()
	client := newClient(t, engine)

	err := client.Exec(t.Context(), "api", []string{"/app/bank", "chaos-kill"})
	if err != nil {
		t.Fatal(err)
	}

	err = client.Unpause(t.Context(), "api")
	if err != nil {
		t.Fatal(err)
	}

	err = client.Start(t.Context(), "api")
	if err != nil {
		t.Fatalf("start of a running container (304): %v", err)
	}

	for _, id := range []string{"lab", "other"} {
		err := client.Exec(t.Context(), id, []string{"/app/lab", "chaos-kill"})
		if !errors.Is(err, docker.ErrNotATarget) {
			t.Fatalf("exec on %s: got %v, want ErrNotATarget", id, err)
		}
	}

	isExpected := len(engine.execs) == 1 && engine.execs[0] == "api:/app/bank chaos-kill" && len(engine.unpaused) == 1 && len(engine.started) == 1
	if !isExpected {
		t.Fatalf("execs %v, unpaused %v, started %v", engine.execs, engine.unpaused, engine.started)
	}
}

// TestPause_FreezesARunningTargetOnce: a freeze pauses the container, and a
// second pause of a paused one asks the engine for nothing.
func TestPause_FreezesARunningTargetOnce(t *testing.T) {
	engine := newFakeEngine()
	engine.paused["api"] = false
	client := newClient(t, engine)

	for range 2 {
		err := client.Pause(t.Context(), "api")
		if err != nil {
			t.Fatal(err)
		}
	}

	err := client.Pause(t.Context(), "lab")
	if !errors.Is(err, docker.ErrNotATarget) {
		t.Fatalf("pause of a container that is not a target: got %v", err)
	}

	if len(engine.pauses) != 1 {
		t.Fatalf("pauses %v, want one", engine.pauses)
	}
}

func TestNew_RequiresATCPHost(t *testing.T) {
	for _, host := range []string{"unix:///var/run/docker.sock", "docker-proxy:2375", "tcp://"} {
		_, err := docker.New(host, "centavo")
		if !errors.Is(err, docker.ErrInvalidHost) {
			t.Errorf("%q: got %v", host, err)
		}
	}
}
