package nemesis

import (
	"context"
	"time"

	"centavo/lab/internal/history"
)

type freezeRecord struct {
	Target    string `json:"target"`
	Failpoint string `json:"failpoint,omitempty"`
	Duration  string `json:"duration"`
	Via       string `json:"via,omitempty"`
}

// The process blocked its own path; the lab pauses the whole container for
// the same time, so the lease renewals stop too and the lease really expires.
func (n *Nemesis) freeze(event failpointEvent) {
	duration, err := time.ParseDuration(event.Arg)
	if err != nil {
		n.config.Logger.Warn("a freeze event without a duration", "target", event.Instance, "arg", event.Arg)

		return
	}

	n.freezes.Go(func() { n.pauseFor(event.Instance, event.Failpoint, "", duration) })
}

func (n *Nemesis) pauseFor(service, failpoint, via string, duration time.Duration) {
	ctx := context.Background()

	container, err := n.config.Docker.Find(ctx, service)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "finding a container to freeze", "target", service, "error", err)

		return
	}

	value := freezeRecord{Target: service, Failpoint: failpoint, Duration: duration.String(), Via: via}

	err = n.record(history.FunctionFreeze, value)
	if err != nil {
		n.config.Logger.ErrorContext(ctx, "recording a freeze", "error", err)

		return
	}

	err = n.config.Docker.Pause(ctx, container.ID)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "pausing a container", "target", service, "error", err)

		return
	}

	time.Sleep(duration)

	err = n.config.Docker.Unpause(ctx, container.ID)
	if err != nil {
		n.config.Logger.WarnContext(ctx, "resuming a frozen container", "target", service, "error", err)
	}
}
