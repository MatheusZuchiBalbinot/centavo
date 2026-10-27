package nemesis

import (
	"context"
	"fmt"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/scenarios"

	"centavo/lab/internal/docker"
)

// databaseService is PostgreSQL's Compose service, a chaos target with its
// own tini, so a SIGKILL to the postmaster ends the container and the
// restart policy brings it back through crash recovery.
const databaseService = "postgres"

// The commands run inside the PostgreSQL container. The oldest process
// named postgres is the postmaster; the newest bank_app one is a busy backend.
var (
	killPostmasterCommand = []string{"pkill", "-KILL", "-o", "-x", "postgres"}
	killBackendCommand    = []string{"pkill", "-KILL", "-n", "-f", "postgres: bank_app"}
)

type databaseRecord struct {
	Fault    string `json:"fault"`
	Action   string `json:"action"`
	Duration string `json:"duration,omitempty"`
}

// A failover sets up replication right away, so a stack without the replica
// fails the round instead of passing it without the fault.
func (n *Nemesis) startDatabase(ctx context.Context) error {
	for _, fault := range n.config.Scenario.Faults {
		if fault.Kind != scenarios.FaultDatabase {
			continue
		}

		if fault.Action != scenarios.DatabaseFailover {
			n.wg.Go(func() { n.databaseEpisodes(ctx, fault) })

			continue
		}

		err := n.setUpReplication(ctx, fault)
		if err != nil {
			return fmt.Errorf("setting up replication for %s: %w", fault.ID, err)
		}

		n.wg.Go(func() { n.failOverAt(ctx, fault) })
	}

	return nil
}

func (n *Nemesis) databaseEpisodes(ctx context.Context, fault scenarios.Fault) {
	stream := n.config.Seed.Stream("nemesis.db/" + fault.ID)

	for {
		wait := time.Duration(stream.ExpFloat64() * float64(fault.Schedule.MeanInterval))
		length := fault.Window.Min + time.Duration(stream.Int64N(int64(fault.Window.Max-fault.Window.Min)+1))

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		err := n.hitDatabase(ctx, fault, length)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "database fault", "fault", fault.ID, "action", fault.Action, "error", err)
		}
	}
}

func (n *Nemesis) hitDatabase(ctx context.Context, fault scenarios.Fault, length time.Duration) error {
	container, err := n.config.Docker.Find(ctx, databaseService)
	if err != nil {
		return err
	}

	isDown := container.State != docker.StateRunning
	if isDown {
		// Still coming back from the last fault: hitting it again would only
		// stretch the outage.
		return nil
	}

	value := databaseRecord{Fault: fault.ID, Action: fault.Action}
	if fault.Action == scenarios.DatabaseFreeze {
		value.Duration = length.String()
	}

	err = n.record(history.FunctionDatabase, value)
	if err != nil {
		return err
	}

	switch fault.Action {
	case scenarios.DatabaseKillPostmaster:
		return n.config.Docker.Exec(ctx, container.ID, killPostmasterCommand)
	case scenarios.DatabaseKillBackend:
		return n.config.Docker.Exec(ctx, container.ID, killBackendCommand)
	default:
		return n.freezeDatabase(ctx, container.ID, length)
	}
}

func (n *Nemesis) freezeDatabase(ctx context.Context, id string, length time.Duration) error {
	err := n.config.Docker.Pause(ctx, id)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
	case <-time.After(length):
	}

	// Resumed even when the round ends early: the cure checks it too.
	return n.config.Docker.Unpause(context.WithoutCancel(ctx), id)
}

// After a failover the old primary stays down: starting it would be split
// brain.
func (n *Nemesis) healDatabase(ctx context.Context) {
	if n.hasFailedOver.Load() {
		return
	}

	container, err := n.config.Docker.Find(ctx, databaseService)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "finding the database to heal", "error", err)

		return
	}

	if container.State == docker.StatePaused {
		err = n.config.Docker.Unpause(ctx, container.ID)
	}

	isStillStopped := container.State == docker.StateStopped && err == nil
	if isStillStopped {
		err = n.config.Docker.Start(ctx, container.ID)
	}

	if err != nil {
		n.config.Logger.WarnContext(ctx, "healing the database", "error", err)
	}
}
