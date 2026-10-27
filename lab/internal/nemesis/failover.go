package nemesis

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/scenarios"
)

const replicaService = "postgres-replica"

// replicaUpstream is where the database links point after a failover: Toxiproxy
// plays the part of the virtual IP.
const replicaUpstream = "postgres-replica:5432"

const replicationLink = "replication"

var databaseLinks = []string{"api_pg", "worker1_pg", "worker2_pg", "auditor_pg"}

var promoteCommand = []string{"psql", "--username", "postgres", "--command", "SELECT pg_promote()"}

const (
	phaseSetUp    = "set_up"
	phasePromoted = "promoted"
)

type failoverRecord struct {
	Fault       string `json:"fault"`
	Replication string `json:"replication"`
	Lag         string `json:"lag"`
	Phase       string `json:"phase"`
}

func synchronousStandbyCommand(replication string) []string {
	standby := ""
	if replication == scenarios.ReplicationSync {
		standby = "replica"
	}

	// ALTER SYSTEM takes no parameters; standby is one of the two constants above.
	setting := fmt.Sprintf("ALTER SYSTEM SET synchronous_standby_names = '%s'", standby)

	return []string{"psql", "--username", "postgres", "--command", setting, "--command", "SELECT pg_reload_conf()"}
}

// At the set time the primary dies for good, the replica is promoted and
// every database link moves to it.
func (n *Nemesis) failOverAt(ctx context.Context, fault scenarios.Fault) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(fault.Schedule.At):
	}

	// Started, the failover runs to the end even if the round stops: half a
	// failover is the split brain the fencing exists to prevent.
	err := n.failOver(context.WithoutCancel(ctx), fault)
	if err != nil {
		n.config.Logger.ErrorContext(ctx, "failing over", "error", err)
	}
}

func (n *Nemesis) setUpReplication(ctx context.Context, fault scenarios.Fault) error {
	_, err := n.config.Docker.Find(ctx, replicaService)
	if err != nil {
		return fmt.Errorf("the replica profile is not up: %w", err)
	}

	err = n.record(history.FunctionFailover, failoverRecord{Fault: fault.ID, Replication: fault.Replication, Lag: fault.Lag.String(), Phase: phaseSetUp})
	if err != nil {
		return err
	}

	primary, err := n.config.Docker.Find(ctx, databaseService)
	if err != nil {
		return err
	}

	err = n.config.Docker.Exec(ctx, primary.ID, synchronousStandbyCommand(fault.Replication))
	if err != nil {
		return err
	}

	if fault.Lag == 0 {
		return nil
	}

	lag := toxicBody{
		Name: fault.ID + "-lag", Type: "latency", Stream: "downstream", Toxicity: 1, Attributes: map[string]int64{"latency": fault.Lag.Milliseconds()},
	}

	return n.addToxic(ctx, replicationLink, lag)
}

func (n *Nemesis) failOver(ctx context.Context, fault scenarios.Fault) error {
	primary, err := n.config.Docker.Find(ctx, databaseService)
	if err != nil {
		return err
	}

	replica, err := n.config.Docker.Find(ctx, replicaService)
	if err != nil {
		return err
	}

	err = n.record(history.FunctionFailover, failoverRecord{Fault: fault.ID, Replication: fault.Replication, Lag: fault.Lag.String(), Phase: phasePromoted})
	if err != nil {
		return err
	}

	n.hasFailedOver.Store(true)

	// The links move first: Toxiproxy closes their live connections to the
	// old primary, which takes no new write from then on. Moving them after
	// the kill hangs on connections to a peer that is gone.
	for _, link := range databaseLinks {
		err := n.toxiproxy(ctx, http.MethodPost, "/proxies/"+url.PathEscape(link), map[string]string{"upstream": replicaUpstream})
		if err != nil {
			return err
		}
	}

	err = n.config.Docker.Exec(ctx, replica.ID, promoteCommand)
	if err != nil {
		return err
	}

	err = n.config.Docker.Kill(ctx, primary.ID)
	if err != nil {
		return err
	}

	return nil
}
