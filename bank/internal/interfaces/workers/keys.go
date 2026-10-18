// Package workers runs the background loops of the worker role.
package workers

import (
	"context"
	"log/slog"
	"time"
)

const (
	keyCleanupInterval = 5 * time.Minute
	keyCleanupBatch    = 1_000
)

// KeyCleaner deletes one batch of expired idempotency keys.
type KeyCleaner interface {
	DeleteExpiredKeys(ctx context.Context, batch int) (int64, error)
}

// CleanKeys deletes expired keys every few minutes until ctx ends. Two
// workers can run it at once: SKIP LOCKED gives them different batches.
func CleanKeys(ctx context.Context, cleaner KeyCleaner, logger *slog.Logger) {
	ticker := time.NewTicker(keyCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		deleted, err := cleaner.DeleteExpiredKeys(ctx, keyCleanupBatch)
		if err != nil {
			logger.WarnContext(ctx, "cleaning idempotency keys", "error", err)

			continue
		}

		if deleted > 0 {
			logger.InfoContext(ctx, "expired idempotency keys deleted", "count", deleted)
		}
	}
}
