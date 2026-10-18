package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/jackc/pgx/v5/pgconn"

	"centavo/bank/internal/application"
)

// SQLSTATE codes.
const (
	serializationFailure = "40001"
	deadlockDetected     = "40P01"
	queryCanceled        = "57014"
)

// classify sorts a unit of work's failure. Domain errors pass through
// untouched; only storage failures are classified.
func classify(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return classifyServerError(pgErr, err)
	}

	// Nothing reached the server: a retry on another connection is safe.
	var connectErr *pgconn.ConnectError
	if pgconn.SafeToRetry(err) || errors.As(err, &connectErr) {
		return fmt.Errorf("%w: %w", application.ErrTransient, err)
	}

	if isConnectionLost(err) {
		return fmt.Errorf("%w: %w", application.ErrOutcomeUnknown, err)
	}

	return err
}

func classifyServerError(pgErr *pgconn.PgError, err error) error {
	switch pgErr.Code {
	case serializationFailure, deadlockDetected:
		return fmt.Errorf("%w: %w", application.ErrTransient, err)
	case queryCanceled, lockNotAvailable:
		return fmt.Errorf("%w: %w", application.ErrUnavailable, err)
	default:
		return err
	}
}

// isConnectionLost is the ambiguous case: the request may have reached the
// server, so a COMMIT may have happened.
func isConnectionLost(err error) bool {
	var netErr net.Error

	isNetwork := errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
	isDeadline := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)

	return isNetwork || isDeadline || pgconn.Timeout(err)
}
