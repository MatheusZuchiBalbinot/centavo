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

// partnerModesTTL makes the lies a lease, renewed with the failpoints.
const partnerModesTTL = "30s"

type armPartnerRecord struct {
	Fault string          `json:"fault"`
	Modes json.RawMessage `json:"modes"`
}

func (n *Nemesis) partnerFaults() []scenarios.Fault {
	var faults []scenarios.Fault

	for _, fault := range n.config.Scenario.Faults {
		if fault.Kind == scenarios.FaultPartner {
			faults = append(faults, fault)
		}
	}

	return faults
}

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

// The partner extends a mode it already has.
func (n *Nemesis) pushPartner(ctx context.Context) {
	for _, fault := range n.partnerFaults() {
		body, err := n.partnerModes(fault)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "encoding partner modes", "fault", fault.ID, "error", err)

			continue
		}

		err = n.chaosCall(ctx, http.MethodPut, n.config.PartnerAdminURL+modesPath, body)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "setting partner modes", "fault", fault.ID, "error", err)
		}
	}
}

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

// Deliveries already planned still go out.
func (n *Nemesis) clearPartner(ctx context.Context) {
	if n.config.PartnerAdminURL == "" {
		return
	}

	err := n.chaosCall(ctx, http.MethodDelete, n.config.PartnerAdminURL+modesPath, nil)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "making the partner honest", "error", err)
	}
}

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
