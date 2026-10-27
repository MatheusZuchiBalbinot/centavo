package nemesis

import (
	"context"
	"strconv"
	"time"

	"centavo/lab/internal/manual"
	"centavo/lab/scenarios"
)

// How an injection came to be, as its record says.
const (
	viaRandom = "random"
	viaManual = "manual"
)

const (
	manualPollInterval = 500 * time.Millisecond
	manualFaultPrefix  = "manual-"
	latencyToxic       = "latency"
	toxicDownstream    = "downstream"
	fullToxicity       = 1
)

func (n *Nemesis) openManual(ctx context.Context) error {
	if n.config.CommandsDir == "" {
		return nil
	}

	err := manual.Open(n.config.CommandsDir)
	if err != nil {
		return err
	}

	n.wg.Go(func() { n.obey(ctx) })

	return nil
}

// The panel stops getting commands in before anything of the cure runs.
func (n *Nemesis) closeManual(ctx context.Context) {
	if n.config.CommandsDir == "" {
		return
	}

	err := manual.Close(n.config.CommandsDir)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "closing the manual attacks", "error", err)
	}
}

func (n *Nemesis) obey(ctx context.Context) {
	ticker := time.NewTicker(manualPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		commands, err := manual.Take(n.config.CommandsDir)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "reading the manual attacks", "error", err)

			continue
		}

		for _, command := range commands {
			n.carryOut(ctx, command)
		}
	}
}

// Manual attacks go through the same steps as the scenario's, recorded the
// same way, so the checker judges the round as it judges any other.
func (n *Nemesis) carryOut(ctx context.Context, command manual.Command) {
	n.config.Logger.InfoContext(ctx, "manual attack", "action", command.Action, "target", command.Target)

	switch command.Action {
	case manual.Kill:
		n.killByHand(ctx, command.Target)
	case manual.Freeze:
		n.freezeByHand(ctx, command.Target)
	case manual.Cut, manual.Slow:
		n.wg.Go(func() { n.networkByHand(ctx, command) })
	}
}

func (n *Nemesis) killByHand(ctx context.Context, service string) {
	err := n.killService(ctx, service)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "manual kill", "target", service, "error", err)
	}
}

func (n *Nemesis) killService(ctx context.Context, service string) error {
	if service == databaseService {
		fault := scenarios.Fault{ID: manualFaultID(), Action: scenarios.DatabaseKillPostmaster}

		return n.hitDatabase(ctx, fault, 0, viaManual)
	}

	return n.kill(ctx, service, viaManual, "")
}

func (n *Nemesis) freezeByHand(ctx context.Context, service string) {
	if service != databaseService {
		n.freezes.Go(func() { n.pauseFor(service, "", viaManual, manual.FreezeFor) })

		return
	}

	fault := scenarios.Fault{ID: manualFaultID(), Action: scenarios.DatabaseFreeze}

	n.freezes.Go(func() {
		err := n.hitDatabase(ctx, fault, manual.FreezeFor, viaManual)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "manual database freeze", "error", err)
		}
	})
}

func (n *Nemesis) networkByHand(ctx context.Context, command manual.Command) {
	fault := scenarios.Fault{ID: manualFaultID(), Kind: scenarios.FaultNetwork, Proxies: []string{command.Target}, Toxic: manualToxic(command.Action)}
	length := manual.CutFor

	if command.Action == manual.Slow {
		length = manual.SlowFor
	}

	n.applyNetwork(ctx, fault, viaManual)

	select {
	case <-ctx.Done():
		// The cure resets Toxiproxy; nothing to remove here.
		return
	case <-time.After(length):
	}

	n.removeNetwork(ctx, fault, viaManual)
}

func manualToxic(action manual.Action) scenarios.Toxic {
	if action == manual.Slow {
		attributes := map[string]int64{latencyToxic: manual.SlowLatency, "jitter": manual.SlowJitter}

		return scenarios.Toxic{Type: latencyToxic, Stream: toxicDownstream, Toxicity: fullToxicity, Attributes: attributes}
	}

	return scenarios.Toxic{Type: scenarios.ToxicBlackhole, Toxicity: fullToxicity}
}

// Toxics are named after their fault, so two manual faults on one link at
// once need different ids.
func manualFaultID() string {
	return manualFaultPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}
