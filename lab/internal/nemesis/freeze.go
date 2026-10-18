package nemesis

import (
	"context"
	"time"

	"centavo/lab/internal/history"
)

// freezeRecord is a container pause in the history.
type freezeRecord struct {
	Target    string `json:"target"`
	Failpoint string `json:"failpoint"`
	Duration  string `json:"duration"`
}

// freeze completes a freeze failpoint: the process blocked its own path, and
// the lab pauses the whole container for the same time, so the lease renewals
// stop too and the lease really expires. The cure unpauses whatever is still
// paused.
func (n *Nemesis) freeze(event failpointEvent) {
	duration, err := time.ParseDuration(event.Arg)
	if err != nil {
		n.config.Logger.Warn("a freeze event without a duration", "target", event.Instance, "arg", event.Arg)

		return
	}

	n.freezes.Go(func() { n.pauseFor(event, duration) })
}

func (n *Nemesis) pauseFor(event failpointEvent, duration time.Duration) {
	ctx := context.Background()

	container, err := n.config.Docker.Find(ctx, event.Instance)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "finding a container to freeze", "target", event.Instance, "error", err)

		return
	}

	value := freezeRecord{Target: event.Instance, Failpoint: event.Failpoint, Duration: duration.String()}

	err = n.record(history.FunctionFreeze, value)
	if err != nil {
		n.config.Logger.ErrorContext(ctx, "recording a freeze", "error", err)

		return
	}

	err = n.config.Docker.Pause(ctx, container.ID)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "pausing a container", "target", event.Instance, "error", err)

		return
	}

	time.Sleep(duration)

	err = n.config.Docker.Unpause(ctx, container.ID)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "resuming a frozen container", "target", event.Instance, "error", err)
	}
}
