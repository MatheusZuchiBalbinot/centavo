// Package docker talks to the Docker Engine API through docker-proxy, which
// exposes only the calls the chaos bot needs, and acts only on containers
// labeled as chaos targets.
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

const (
	StateStopped ContainerState = "stopped"
	StateRunning ContainerState = "running"
	StatePaused  ContainerState = "paused"
)

const (
	HealthNone    HealthStatus = ""
	HealthHealthy HealthStatus = "healthy"
)

// Docker reports a paused container as running too; here the three are
// exclusive.
type ContainerState string

type HealthStatus string

type Container struct {
	ID           string
	Service      string
	State        ContainerState
	Health       HealthStatus
	RestartCount int
}

type Client struct {
	baseURL string
	project string
	http    *http.Client
}

type listedContainer struct {
	ID     string            `json:"Id"`
	Labels map[string]string `json:"Labels"`
}

type inspected struct {
	ID           string `json:"Id"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		IsRunning bool `json:"Running"`
		IsPaused  bool `json:"Paused"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

type execCreated struct {
	ID string `json:"Id"`
}

type apiCall struct {
	method string
	path   string
	body   any
}

// A container without a healthcheck counts as healthy once it runs.
func (c Container) IsHealthy() bool {
	isCheckPassing := c.Health == HealthNone || c.Health == HealthHealthy

	return c.State == StateRunning && isCheckPassing
}

func New(dockerHost, project string) (Client, error) {
	address, isTCP := strings.CutPrefix(dockerHost, tcpScheme)
	isInvalidHost := !isTCP || address == ""
	if isInvalidHost {
		return Client{}, ErrInvalidHost
	}

	return Client{baseURL: "http://" + address, project: project, http: &http.Client{Timeout: callTimeout}}, nil
}

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

// Refuses containers that are not chaos targets of this project.
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

	container := Container{ID: details.ID, Service: labels[serviceLabel], State: stateOf(details), RestartCount: details.RestartCount}

	if details.State.Health != nil {
		container.Health = HealthStatus(details.State.Health.Status)
	}

	return container, nil
}

// Does not wait: chaos-kill ends the container, so there is nothing to wait
// for.
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

func (c Client) Start(ctx context.Context, id string) error {
	_, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
}

func (c Client) Pause(ctx context.Context, id string) error {
	container, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	if container.State == StatePaused {
		return nil
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/pause", nil, nil)
}

// Docker counts a SIGKILL from outside as a manual stop, so the restart
// policy leaves the container down: the fencing of an old primary after a
// failover.
func (c Client) Kill(ctx context.Context, id string) error {
	_, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/kill?signal=SIGKILL", nil, nil)
}

func (c Client) Unpause(ctx context.Context, id string) error {
	container, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}

	if container.State != StatePaused {
		return nil
	}

	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/unpause", nil, nil)
}

func stateOf(details inspected) ContainerState {
	if details.State.IsPaused {
		return StatePaused
	}

	if details.State.IsRunning {
		return StateRunning
	}

	return StateStopped
}

func (c Client) get(ctx context.Context, path string, into any) error {
	return c.call(ctx, apiCall{method: http.MethodGet, path: path}, into)
}

func (c Client) post(ctx context.Context, path string, body, into any) error {
	return c.call(ctx, apiCall{method: http.MethodPost, path: path, body: body}, into)
}

// 304 (already started) is not an error.
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

	hasNothingToDecode := into == nil || len(content) == 0
	if hasNothingToDecode {
		return nil
	}

	return json.Unmarshal(content, into)
}
