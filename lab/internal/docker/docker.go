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

	err = c.call(ctx, http.MethodGet, "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), nil, &listed)
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

	err := c.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &details)
	if err != nil {
		return Container{}, err
	}

	labels := details.Config.Labels

	isTarget := labels[targetLabel] == "true" && labels[projectLabel] == c.project
	if !isTarget {
		return Container{}, fmt.Errorf("%w: %s", ErrNotATarget, id)
	}

	container := Container{
		ID:           details.ID,
		Service:      labels[serviceLabel],
		IsRunning:    details.State.Running,
		IsPaused:     details.State.Paused,
		RestartCount: details.RestartCount,
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

	err = c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/exec", map[string][]string{"Cmd": command}, &created)
	if err != nil {
		return err
	}

	return c.call(ctx, http.MethodPost, "/exec/"+url.PathEscape(created.ID)+"/start", map[string]bool{"Detach": true}, nil)
}

// Start starts a stopped target; starting a running one is a no-op.
func (c Client) Start(ctx context.Context, id string) error {
	_, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
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

	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/unpause", nil, nil)
}

// call sends one request. 304 (already started) is not an error.
func (c Client) call(ctx context.Context, method, path string, body, into any) error {
	var payload io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}

		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("docker %s %s: %w", method, path, err)
	}
	defer response.Body.Close()

	content, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("docker %s %s: %w", method, path, err)
	}

	isSuccess := response.StatusCode < http.StatusMultipleChoices || response.StatusCode == http.StatusNotModified
	if !isSuccess {
		return fmt.Errorf("%w: %s %s answered %d: %s", errUnexpectedReply, method, path, response.StatusCode, bytes.TrimSpace(content))
	}

	if into == nil || len(content) == 0 {
		return nil
	}

	return json.Unmarshal(content, into)
}
