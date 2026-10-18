package nemesis

import (
	"context"
	"fmt"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/scenarios"

	"centavo/lab/internal/docker"
)

// minKillGap keeps Docker's restart backoff from growing: it resets only after
// a container stays up for 10 s.
const minKillGap = 10 * time.Second

const chaosKillCommand = "chaos-kill"

type killRecord struct {
	Target string `json:"target"`
	Via    string `json:"via"`
	Fault  string `json:"fault"`
}

// killRandomly is family 2: SIGKILL a random target at exponential intervals.
func (n *Nemesis) killRandomly(ctx context.Context, fault scenarios.Fault) {
	schedule := n.config.Seed.Stream("nemesis.schedule/" + fault.ID)
	choices := n.config.Seed.Stream("nemesis.targets/" + fault.ID)
	lastKill := map[string]time.Time{}

	for {
		wait := time.Duration(schedule.ExpFloat64() * float64(fault.Schedule.MeanInterval))

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		service := fault.Targets[choices.IntN(len(fault.Targets))]

		isTooSoon := time.Since(lastKill[service]) < minKillGap
		if isTooSoon {
			continue
		}

		err := n.kill(ctx, fault, service)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "random kill", "target", service, "error", err)

			continue
		}

		lastKill[service] = time.Now()
	}
}

// kill records the kill, then runs chaos-kill inside the target, so the
// process dies of its own SIGKILL and the restart policy brings it back.
func (n *Nemesis) kill(ctx context.Context, fault scenarios.Fault, service string) error {
	t, isKnown := targetByService(service)
	if !isKnown {
		return fmt.Errorf("unknown target %s", service)
	}

	container, err := n.config.Docker.Find(ctx, service)
	if err != nil {
		return err
	}

	if container.State == docker.StateStopped {
		return fmt.Errorf("%s is not running", service)
	}

	err = n.record(history.FunctionKill, killRecord{Target: service, Via: "random", Fault: fault.ID})
	if err != nil {
		return err
	}

	return n.config.Docker.Exec(ctx, container.ID, []string{t.binary, chaosKillCommand})
}
