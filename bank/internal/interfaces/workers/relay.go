package workers

import (
	"context"
	"log/slog"

	"centavo/bank/internal/domain/transfer"
)

type Relayer interface {
	Relay(ctx context.Context, id transfer.ID) error
}

func RelayNotified(relay Relayer, logger *slog.Logger) func(ctx context.Context, payload string) {
	return func(ctx context.Context, payload string) {
		id, err := transfer.ParseID(payload)
		if err != nil {
			logger.WarnContext(ctx, "ignoring a malformed pix_out notification", "payload", payload)

			return
		}

		err = relay.Relay(ctx, id)
		if err != nil {
			logger.WarnContext(ctx, "relaying a transfer", "transfer_id", id.String(), "error", err)
		}
	}
}
