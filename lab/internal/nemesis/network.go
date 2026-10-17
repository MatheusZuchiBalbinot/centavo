package nemesis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/scenarios"
)

// toxiproxyURL is Toxiproxy's API, on the Compose network only (lock 3).
const toxiproxyURL = "http://toxiproxy:8474"

// timeoutToxic is the toxic that holds data, the base of both partitions
// that leave the connection open.
const timeoutToxic = "timeout"

// networkRecord is a network episode in the history.
type networkRecord struct {
	Fault   string   `json:"fault"`
	Proxies []string `json:"proxies"`
	Toxic   string   `json:"toxic"`
	Phase   string   `json:"phase"`
}

const (
	phaseStart = "start"
	phaseEnd   = "end"
)

// toxicBody is one toxic in Toxiproxy's API.
type toxicBody struct {
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	Stream     string           `json:"stream"`
	Toxicity   float64          `json:"toxicity"`
	Attributes map[string]int64 `json:"attributes"`
}

// startNetwork runs every network fault: a fault without a schedule holds
// for the whole round, a scheduled one comes in episodes.
func (n *Nemesis) startNetwork(ctx context.Context) {
	for _, fault := range n.config.Scenario.Faults {
		if fault.Kind != scenarios.FaultNetwork {
			continue
		}

		isScheduled := fault.Schedule.Type != ""
		if isScheduled {
			n.wg.Go(func() { n.networkEpisodes(ctx, fault) })

			continue
		}

		n.applyNetwork(ctx, fault)
	}
}

func (n *Nemesis) networkEpisodes(ctx context.Context, fault scenarios.Fault) {
	stream := n.config.Seed.Stream("nemesis.network/" + fault.ID)

	for {
		wait := time.Duration(stream.ExpFloat64() * float64(fault.Schedule.MeanInterval))
		span := fault.Window.Max - fault.Window.Min
		length := fault.Window.Min + time.Duration(stream.Int64N(int64(span)+1))

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		n.applyNetwork(ctx, fault)

		select {
		case <-ctx.Done():
			// The cure resets Toxiproxy; nothing to remove here.
			return
		case <-time.After(length):
		}

		n.removeNetwork(ctx, fault)
	}
}

func (n *Nemesis) applyNetwork(ctx context.Context, fault scenarios.Fault) {
	err := n.record(history.FunctionNetwork, networkRecord{Fault: fault.ID, Proxies: fault.Proxies, Toxic: fault.Toxic.Type, Phase: phaseStart})
	if err != nil {
		n.config.Logger.ErrorContext(ctx, "recording a network fault", "error", err)

		return
	}

	for _, proxy := range fault.Proxies {
		err := n.applyToxic(ctx, proxy, fault)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "applying a toxic", "proxy", proxy, "error", err)
		}
	}
}

func (n *Nemesis) removeNetwork(ctx context.Context, fault scenarios.Fault) {
	for _, proxy := range fault.Proxies {
		err := n.removeToxic(ctx, proxy, fault)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "removing a toxic", "proxy", proxy, "error", err)
		}
	}

	err := n.record(history.FunctionNetwork, networkRecord{Fault: fault.ID, Proxies: fault.Proxies, Toxic: fault.Toxic.Type, Phase: phaseEnd})
	if err != nil {
		n.config.Logger.ErrorContext(ctx, "recording a network fault", "error", err)
	}
}

// applyToxic turns the scenario's toxic into Toxiproxy calls. The three
// partitions are not toxics of their own: reject disables the proxy,
// blackhole holds both directions, one_way holds only the answers.
func (n *Nemesis) applyToxic(ctx context.Context, proxy string, fault scenarios.Fault) error {
	toxic := fault.Toxic

	switch toxic.Type {
	case scenarios.ToxicReject:
		return n.setProxyEnabled(ctx, proxy, false)
	case scenarios.ToxicBlackhole:
		err := n.addToxic(ctx, proxy, toxicBody{Name: fault.ID + "-up", Type: timeoutToxic, Stream: "upstream", Toxicity: toxic.Toxicity, Attributes: map[string]int64{timeoutToxic: 0}})
		if err != nil {
			return err
		}

		return n.addToxic(ctx, proxy, toxicBody{Name: fault.ID + "-down", Type: timeoutToxic, Stream: "downstream", Toxicity: toxic.Toxicity, Attributes: map[string]int64{timeoutToxic: 0}})
	case scenarios.ToxicOneWay:
		return n.addToxic(ctx, proxy, toxicBody{Name: fault.ID + "-down", Type: timeoutToxic, Stream: "downstream", Toxicity: toxic.Toxicity, Attributes: map[string]int64{timeoutToxic: 0}})
	default:
		return n.addToxic(ctx, proxy, toxicBody{Name: fault.ID, Type: toxic.Type, Stream: toxic.Stream, Toxicity: toxic.Toxicity, Attributes: toxic.Attributes})
	}
}

func (n *Nemesis) removeToxic(ctx context.Context, proxy string, fault scenarios.Fault) error {
	switch fault.Toxic.Type {
	case scenarios.ToxicReject:
		return n.setProxyEnabled(ctx, proxy, true)
	case scenarios.ToxicBlackhole:
		err := n.deleteToxic(ctx, proxy, fault.ID+"-up")
		if err != nil {
			return err
		}

		return n.deleteToxic(ctx, proxy, fault.ID+"-down")
	case scenarios.ToxicOneWay:
		return n.deleteToxic(ctx, proxy, fault.ID+"-down")
	default:
		return n.deleteToxic(ctx, proxy, fault.ID)
	}
}

func (n *Nemesis) addToxic(ctx context.Context, proxy string, toxic toxicBody) error {
	return n.toxiproxy(ctx, http.MethodPost, "/proxies/"+url.PathEscape(proxy)+"/toxics", toxic)
}

func (n *Nemesis) deleteToxic(ctx context.Context, proxy, name string) error {
	return n.toxiproxy(ctx, http.MethodDelete, "/proxies/"+url.PathEscape(proxy)+"/toxics/"+url.PathEscape(name), nil)
}

func (n *Nemesis) setProxyEnabled(ctx context.Context, proxy string, isEnabled bool) error {
	return n.toxiproxy(ctx, http.MethodPost, "/proxies/"+url.PathEscape(proxy), map[string]bool{"enabled": isEnabled})
}

// resetNetwork is the network part of the cure: every proxy enabled, every
// toxic gone. Toxics never expire by themselves.
func (n *Nemesis) resetNetwork(ctx context.Context) {
	err := n.toxiproxy(ctx, http.MethodPost, "/reset", nil)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "resetting Toxiproxy", "error", err)
	}
}

func (n *Nemesis) toxiproxy(ctx context.Context, method, path string, body any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	var payload io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}

		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, n.toxiproxyURL+path, payload)
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := n.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// The body only enriches the error message.
	content, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("toxiproxy %s %s answered %d: %s", method, path, response.StatusCode, content)
	}

	return nil
}
