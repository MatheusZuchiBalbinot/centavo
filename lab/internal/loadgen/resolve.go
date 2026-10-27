package loadgen

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"centavo/lab/internal/history"
	"centavo/lab/internal/seed"
)

// stabilitySample is the share of keys resolved twice; the second answer must
// match the first (I3, stability).
const stabilitySample = 0.1

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
	// Op is the operation resolved, which is not the key's owner when a
	// buggy client reused the key.
	Op string `json:"op"`
}

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

// As operations of their own processes, after heal_complete. Returns the
// next free process number.
func Resolve(ctx context.Context, config ResolveConfig) (int, error) {
	r := &run{
		config: Config{Round: config.Round, History: config.History, Logger: config.Logger}, policy: politePolicy, bank: newBankClient(config.ControlURL, 1),
	}
	r.nextProcess.Store(int64(config.FirstProcess))

	resolver := &client{
		run: r, process: config.FirstProcess, policy: politePolicy, jitter: config.Seed.Stream("cure.retries"),
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

func ReadPixOuts(ctx context.Context, config ResolveConfig, transferIDs []string) error {
	r := &run{
		config: Config{Round: config.Round, History: config.History, Logger: config.Logger}, policy: politePolicy, bank: newBankClient(config.ControlURL, 1),
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

	resolveRequest := request{method: http.MethodPost, path: resolvePath(write.Key), body: body}

	op, err := newOperation(history.FunctionResolve, resolveRequest, resolveArgs{Key: write.Key, Op: cmp.Or(write.Op, write.Key)}, parseResolve)
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
	if err != nil {
		return nil, errMissingField
	}

	isIncomplete := response.Key == "" || !isKnownOutcome
	if isIncomplete {
		return nil, errMissingField
	}

	return json.Marshal(resolveResult{Key: response.Key, Outcome: response.Outcome, Response: response.Response, HTTPStatus: status})
}
