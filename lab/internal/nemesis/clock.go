package nemesis

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/scenarios"
)

// skewRecord is a clock fault in the history.
type skewRecord struct {
	Fault   string   `json:"fault"`
	Targets []string `json:"targets"`
	Offset  string   `json:"offset"`
}

// skewRequest is the bank's PUT /chaos/clock body.
type skewRequest struct {
	Offset string `json:"offset"`
	TTL    string `json:"ttl"`
}

func (n *Nemesis) clockFaults() []scenarios.Fault {
	var faults []scenarios.Fault

	for _, fault := range n.config.Scenario.Faults {
		if fault.Kind == scenarios.FaultClock {
			faults = append(faults, fault)
		}
	}

	return faults
}

// skewClocks records the clock faults and keeps them set: like the
// failpoint rules, a skew is a lease the lab renews, so it ends by itself
// when the lab dies and comes back to a restarted process.
func (n *Nemesis) skewClocks(ctx context.Context) error {
	faults := n.clockFaults()

	for _, fault := range faults {
		err := n.record(history.FunctionSkewClock, skewRecord{Fault: fault.ID, Targets: fault.Targets, Offset: fault.Offset.String()})
		if err != nil {
			return err
		}
	}

	if len(faults) == 0 {
		return nil
	}

	n.wg.Go(func() { n.renewSkews(ctx, faults) })

	return nil
}

func (n *Nemesis) renewSkews(ctx context.Context, faults []scenarios.Fault) {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		for _, fault := range faults {
			n.pushSkew(ctx, fault)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (n *Nemesis) pushSkew(ctx context.Context, fault scenarios.Fault) {
	body, err := json.Marshal(skewRequest{Offset: fault.Offset.String(), TTL: ruleTTL})
	if err != nil {
		return
	}

	for _, service := range fault.Targets {
		t, isKnown := targetByService(service)
		isUnreachable := !isKnown || t.chaosURL == ""
		if isUnreachable {
			continue
		}

		err := n.chaosCall(ctx, http.MethodPut, t.chaosURL+clockPath, body)
		if err != nil {
			// A target that is down gets the skew on the next renewal.
			n.config.Logger.DebugContext(ctx, "skewing a clock", "target", service, "error", err)
		}
	}
}

// clearClocks puts every bank process's clock right; a process that is down
// has no skew to clear.
func (n *Nemesis) clearClocks(ctx context.Context) {
	for _, t := range targets {
		if t.chaosURL == "" {
			continue
		}

		err := n.chaosCall(ctx, http.MethodDelete, t.chaosURL+clockPath, nil)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "clearing a clock skew", "target", t.service, "error", err)
		}
	}
}
