// Package docker talks to the Docker Engine API through docker-proxy, which
// exposes only the calls the chaos bot needs (lock 5 of docs/chaos-bot.md,
// section 16). It acts only on containers labeled as chaos targets.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	targetLabel  = "centavo.chaos.target"
	projectLabel = "com.docker.compose.project"
	serviceLabel = "com.docker.compose.service"
	tcpScheme    = "tcp://"
	callTimeout  = 10 * time.Second
	maxBodyBytes = 1 << 20
)

// Errors of acting on the chaos targets.
var (
	ErrNotATarget      = errors.New("container is not a chaos target")
	ErrUnknownService  = errors.New("no chaos target runs this service")
	ErrInvalidHost     = errors.New("DOCKER_HOST must be tcp://host:port")
	errUnexpectedReply = errors.New("unexpected answer from the Docker API")
)

// Container is what the bot needs to know about a chaos target.
type Container struct {
	ID           string
	Service      string
	IsRunning    bool
	IsPaused     bool
	Health       string
	RestartCount int
}

// IsHealthy reports a running container whose healthcheck passes; a
// container without a healthcheck counts as healthy once it runs.
func (c Container) IsHealthy() bool {
	return c.IsRunning && !c.IsPaused && (c.Health == "" || c.Health == "healthy")
}

// Client is a Docker API client scoped to one Compose project.
type Client struct {
	baseURL string
	project string
	http    *http.Client
}

// New builds a client for DOCKER_HOST=tcp://docker-proxy:2375.
func New(dockerHost, project string) (Client, error) {
	address, isTCP := strings.CutPrefix(dockerHost, tcpScheme)
	if !isTCP || address == "" {
		return Client{}, ErrInvalidHost
	}

	return Client{baseURL: "http://" + address, project: project, http: &http.Client{Timeout: callTimeout}}, nil
}

type listedContainer struct {
	ID     string            `json:"Id"`
	Labels map[string]string `json:"Labels"`
}

// Targets lists the chaos targets of the project, running or not.
func (c Client) Targets(ctx context.Context) ([]Container, error) {
	filters, err := json.Marshal(map[string][]string{"label": {targetLabel + "=true", projectLabel + "=" + c.project}})
	if err != nil {
		return nil, err
	}

	var listed []listedContainer

	err = c.get(ctx, "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), &listed)
	if err != nil {
		return nil, err
	}

	targets := make([]Container, 0, len(listed))

	for _, entry := range listed {
		container, err := c.Inspect(ctx, entry.ID)
		if err != nil {
			return nil, err
		}

		targets = append(targets, container)
	}

	return targets, nil
}

// Find returns the target running a Compose service.
func (c Client) Find(ctx context.Context, service string) (Container, error) {
	targets, err := c.Targets(ctx)
	if err != nil {
		return Container{}, err
	}

	for _, target := range targets {
		if target.Service == service {
			return target, nil
		}
	}

	return Container{}, fmt.Errorf("%w: %s", ErrUnknownService, service)
}

type inspected struct {
	ID           string `json:"Id"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Running bool `json:"Running"`
		Paused  bool `json:"Paused"`
		Health  *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// Inspect reads a container's state; it refuses containers that are not
// chaos targets of this project.
func (c Client) Inspect(ctx context.Context, id string) (Container, error) {
	var details inspected

	err := c.get(ctx, "/containers/"+url.PathEscape(id)+"/json", &details)
	if err != nil {
		return Container{}, err
	}

	labels := details.Config.Labels

	isTarget := labels[targetLabel] == "true" && labels[projectLabel] == c.project
	if !isTarget {
		return Container{}, fmt.Errorf("%w: %s", ErrNotATarget, id)
	}

	container := Container{
		ID: details.ID, Service: labels[serviceLabel], IsRunning: details.State.Running, IsPaused: details.State.Paused, RestartCount: details.RestartCount,
	}

	if details.State.Health != nil {
		container.Health = details.State.Health.Status
	}

	return container, nil
}

type execCreated struct {
	ID string `json:"Id"`
}

// Exec runs a command in a target and does not wait for it: chaos-kill ends
// the container, so there is nothing to wait for.
func (c Client) Exec(ctx context.Context, id string, command []string) error {
	_, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	var created execCreated

	err = c.post(ctx, "/containers/"+url.PathEscape(id)+"/exec", map[string][]string{"Cmd": command}, &created)
	if err != nil {
		return err
	}

	return c.post(ctx, "/exec/"+url.PathEscape(created.ID)+"/start", map[string]bool{"Detach": true}, nil)
}

// Start starts a stopped target; starting a running one is a no-op.
func (c Client) Start(ctx context.Context, id string) error {
	_, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
}

// Pause freezes a running target, every goroutine of it included; pausing
// one already paused is a no-op.
func (c Client) Pause(ctx context.Context, id string) error {
	container, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	if container.IsPaused {
		return nil
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/pause", nil, nil)
}

// Kill sends SIGKILL from outside. Docker counts it as a manual stop, so the
// restart policy leaves the container down: the fencing of an old primary
// after a failover (docs/architecture.md, section 5.10).
func (c Client) Kill(ctx context.Context, id string) error {
	_, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/kill?signal=SIGKILL", nil, nil)
}

// Unpause resumes a paused target; resuming one that is not paused is a no-op.
func (c Client) Unpause(ctx context.Context, id string) error {
	container, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	if !container.IsPaused {
		return nil
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/unpause", nil, nil)
}

// apiCall is one request to the Docker Engine API; body is sent as JSON when
// it is not nil.
type apiCall struct {
	method string
	path   string
	body   any
}

func (c Client) get(ctx context.Context, path string, into any) error {
	return c.call(ctx, apiCall{method: http.MethodGet, path: path}, into)
}

func (c Client) post(ctx context.Context, path string, body, into any) error {
	return c.call(ctx, apiCall{method: http.MethodPost, path: path, body: body}, into)
}

// call sends one request. 304 (already started) is not an error.
func (c Client) call(ctx context.Context, sent apiCall, into any) error {
	var payload io.Reader = http.NoBody

	if sent.body != nil {
		encoded, err := json.Marshal(sent.body)
		if err != nil {
			return err
		}

		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, sent.method, c.baseURL+sent.path, payload)
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("docker %s %s: %w", sent.method, sent.path, err)
	}
	defer response.Body.Close()

	content, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("docker %s %s: %w", sent.method, sent.path, err)
	}

	isSuccess := response.StatusCode < http.StatusMultipleChoices || response.StatusCode == http.StatusNotModified
	if !isSuccess {
		return fmt.Errorf("%w: %s %s answered %d: %s", errUnexpectedReply, sent.method, sent.path, response.StatusCode, bytes.TrimSpace(content))
	}

	if into == nil || len(content) == 0 {
		return nil
	}

	return json.Unmarshal(content, into)
}
