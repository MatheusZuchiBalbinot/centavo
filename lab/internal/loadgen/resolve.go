package loadgen

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"centavo/lab/internal/history"
	"centavo/lab/internal/seed"
)

// stabilitySample is the share of keys resolved twice; the second answer
// must match the first (I3, stability).
const stabilitySample = 0.1

// ResolveConfig is the resolve step of the cure (docs/chaos-bot.md, section 3).
type ResolveConfig struct {
	Round string
	Seed  seed.Seed
	// ControlURL reaches the bank directly, never through Toxiproxy: the
	// lab's control calls happen only after the cure.
	ControlURL   string
	History      *history.Writer
	Logger       *slog.Logger
	Writes       []IndeterminateWrite
	FirstProcess int
}

type resolveArgs struct {
	Key string `json:"key"`
}

// resolveBody is the request being resolved, as it was sent.
type resolveBody struct {
	Endpoint string          `json:"endpoint"`
	Body     json.RawMessage `json:"body"`
}

type resolveResult struct {
	Key        string          `json:"key"`
	Outcome    string          `json:"outcome"`
	Response   json.RawMessage `json:"response,omitempty"`
	HTTPStatus int             `json:"http_status"`
}

// Resolve asks the bank for the outcome of every indeterminate write, as
// operations of their own processes, after heal_complete. It returns the
// next free process number.
func Resolve(ctx context.Context, config ResolveConfig) (int, error) {
	r := &run{
		config: Config{Round: config.Round, History: config.History, Logger: config.Logger},
		policy: politePolicy,
		bank:   newBankClient(config.ControlURL, 1),
	}
	r.nextProcess.Store(int64(config.FirstProcess))

	resolver := &client{
		run:     r,
		process: config.FirstProcess,
		policy:  politePolicy,
		jitter:  config.Seed.Stream("cure.retries"),
	}
	sample := config.Seed.Stream("cure.sample")

	for _, write := range config.Writes {
		op, err := resolveOperation(write)
		if err != nil {
			return 0, err
		}

		_, err = resolver.execute(ctx, op)
		if err != nil {
			return 0, err
		}

		isSampled := sample.Float64() < stabilitySample
		if !isSampled {
			continue
		}

		_, err = resolver.execute(ctx, op)
		if err != nil {
			return 0, err
		}
	}

	return int(r.nextProcess.Load()) + 1, nil
}

// ReadPixOuts records the final state of every PIX out of the round, after
// the quiescence (read_pix_out of docs/invariants-and-checker.md, section 3).
func ReadPixOuts(ctx context.Context, config ResolveConfig, transferIDs []string) error {
	r := &run{
		config: Config{Round: config.Round, History: config.History, Logger: config.Logger},
		policy: politePolicy,
		bank:   newBankClient(config.ControlURL, 1),
	}
	r.nextProcess.Store(int64(config.FirstProcess))

	reader := &client{run: r, process: config.FirstProcess, policy: politePolicy, jitter: config.Seed.Stream("cure.reads")}

	for _, id := range transferIDs {
		op, err := readPixOutOperation(id)
		if err != nil {
			return err
		}

		_, err = reader.execute(ctx, op)
		if err != nil {
			return err
		}
	}

	return nil
}

func resolveOperation(write IndeterminateWrite) (operation, error) {
	body, err := json.Marshal(resolveBody{Endpoint: write.Path, Body: write.Body})
	if err != nil {
		return operation{}, err
	}

	resolveRequest := request{method: http.MethodPost, path: "/v1/operations/" + url.PathEscape(write.Key) + "/resolve", body: body}

	op, err := newOperation(history.FunctionResolve, resolveRequest, resolveArgs{Key: write.Key}, parseResolve)
	op.hasNoKey = true

	return op, err
}

func parseResolve(body []byte, status int) (json.RawMessage, error) {
	var response struct {
		Key      string          `json:"key"`
		Outcome  string          `json:"outcome"`
		Response json.RawMessage `json:"response"`
	}

	err := json.Unmarshal(body, &response)

	// superseded: another request took the key, so this one never applied.
	isKnownOutcome := response.Outcome == "applied" || response.Outcome == "voided" || response.Outcome == "superseded"
	if err != nil || response.Key == "" || !isKnownOutcome {
		return nil, errMissingField
	}

	return json.Marshal(resolveResult{Key: response.Key, Outcome: response.Outcome, Response: response.Response, HTTPStatus: status})
}
