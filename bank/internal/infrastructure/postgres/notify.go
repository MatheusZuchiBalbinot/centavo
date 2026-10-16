package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/domain/transfer"
)

// PixOutChannel is the NOTIFY channel of new PIX out.
const PixOutChannel = "pix_out"

// reconnectDelay paces the listener after a lost connection.
const reconnectDelay = time.Second

// NotifyPublisher publishes a transfer with NOTIFY: the naive publication,
// which nobody receives while no worker is listening (FC04).
type NotifyPublisher struct {
	store LedgerStore
}

// NewNotifyPublisher wraps a pool.
func NewNotifyPublisher(pool *pgxpool.Pool) NotifyPublisher {
	return NotifyPublisher{store: NewLedgerStore(pool)}
}

// Publish sends the transfer id on the pix_out channel.
func (p NotifyPublisher) Publish(ctx context.Context, id transfer.ID) error {
	return p.store.Notify(ctx, PixOutChannel, id.String())
}

// Listen holds a connection with LISTEN on the channel and calls handle for
// every notification, reconnecting until ctx ends. Notifications sent while
// the connection is down are lost: NOTIFY is not a queue.
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, handle func(ctx context.Context, payload string), logger *slog.Logger) {
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, channel, handle)
		if err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "listener lost its connection", "channel", channel, "error", err)
		}

		select {
		case <-ctx.Done():
		case <-time.After(reconnectDelay):
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, channel string, handle func(ctx context.Context, payload string)) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, fmt.Sprintf("LISTEN %s", channel))
	if err != nil {
		return err
	}

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}

		handle(ctx, notification.Payload)
	}
}
