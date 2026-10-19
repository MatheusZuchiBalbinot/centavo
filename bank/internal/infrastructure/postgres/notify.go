package postgres

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/domain/transfer"
)

// PixOutChannel is the NOTIFY channel of new PIX out.
const PixOutChannel = "pix_out"

// reconnectDelay paces the listener after a lost connection.
const reconnectDelay = time.Second

// NotifyPublisher publishes a transfer with NOTIFY: the naive publication,
// which nobody receives while no worker is listening.
type NotifyPublisher struct {
	store LedgerStore
}

// Listener holds a connection with LISTEN on Channel and calls Handle for
// every notification.
type Listener struct {
	Pool    *pgxpool.Pool
	Channel string
	Handle  func(ctx context.Context, payload string)
	Logger  *slog.Logger
}

// NewNotifyPublisher wraps a pool.
func NewNotifyPublisher(pool *pgxpool.Pool) NotifyPublisher {
	return NotifyPublisher{store: NewLedgerStore(pool)}
}

// Publish sends the transfer id on the pix_out channel.
func (p NotifyPublisher) Publish(ctx context.Context, id transfer.ID) error {
	return p.store.Notify(ctx, PixOutChannel, id.String())
}

// Run listens, reconnecting until ctx ends. Notifications sent while the
// connection is down are lost: NOTIFY is not a queue.
func (l Listener) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := listenOnce(ctx, l.Pool, l.Channel, l.Handle)

		isConnectionLost := err != nil && ctx.Err() == nil
		if isConnectionLost {
			l.Logger.WarnContext(ctx, "listener lost its connection", "channel", l.Channel, "error", err)
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

	// LISTEN takes no parameters: the channel is quoted as an identifier.
	_, err = conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize())
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
