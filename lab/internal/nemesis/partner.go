package nemesis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/scenarios"
)

// partnerModesTTL makes the lies a lease, renewed with the failpoints (lock 6).
const partnerModesTTL = "30s"

type armPartnerRecord struct {
	Fault string          `json:"fault"`
	Modes json.RawMessage `json:"modes"`
}

// partnerFaults are the scenario's lies of the partner.
func (n *Nemesis) partnerFaults() []scenarios.Fault {
	var faults []scenarios.Fault

	for _, fault := range n.config.Scenario.Faults {
		if fault.Kind == scenarios.FaultPartner {
			faults = append(faults, fault)
		}
	}

	return faults
}

// armPartner records and sets the partner's lies.
func (n *Nemesis) armPartner(ctx context.Context) error {
	for _, fault := range n.partnerFaults() {
		err := n.record(history.FunctionArmPartner, armPartnerRecord{Fault: fault.ID, Modes: fault.Modes})
		if err != nil {
			return err
		}
	}

	n.pushPartner(ctx)

	return nil
}

// pushPartner (re)sends every lie; the partner extends a mode it already has.
func (n *Nemesis) pushPartner(ctx context.Context) {
	for _, fault := range n.partnerFaults() {
		body, err := n.partnerModes(fault)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "encoding partner modes", "fault", fault.ID, "error", err)

			continue
		}

		err = n.chaosCall(ctx, http.MethodPut, n.config.PartnerAdminURL+"/chaos/modes", body)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "setting partner modes", "fault", fault.ID, "error", err)
		}
	}
}

// partnerModes adds the participant, the seed and the TTL to the scenario's lies.
func (n *Nemesis) partnerModes(fault scenarios.Fault) ([]byte, error) {
	var modes map[string]any

	err := json.Unmarshal(fault.Modes, &modes)
	if err != nil {
		return nil, err
	}

	modes["participant_id"] = n.config.ParticipantID
	modes["ttl"] = partnerModesTTL
	modes["seed"] = fmt.Sprintf("%#x", n.config.Seed.Stream("partner.lies/"+fault.ID).Uint64())

	return json.Marshal(modes)
}

// clearPartner makes the partner honest again; deliveries already planned
// still go out (docs/chaos-bot.md, section 3.1).
func (n *Nemesis) clearPartner(ctx context.Context) {
	if n.config.PartnerAdminURL == "" {
		return
	}

	err := n.chaosCall(ctx, http.MethodDelete, n.config.PartnerAdminURL+"/chaos/modes", nil)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "making the partner honest", "error", err)
	}
}

// renewPartner keeps the lies alive while the round runs.
func (n *Nemesis) renewPartner(ctx context.Context) {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		n.pushPartner(ctx)
	}
}
