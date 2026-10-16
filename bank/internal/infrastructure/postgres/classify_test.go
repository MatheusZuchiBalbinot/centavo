package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{name: "serialization failure", err: &pgconn.PgError{Code: serializationFailure}, want: application.ErrTransient},
		{name: "deadlock", err: &pgconn.PgError{Code: deadlockDetected}, want: application.ErrTransient},
		{name: "statement timeout", err: &pgconn.PgError{Code: queryCanceled}, want: application.ErrUnavailable},
		{name: "lock timeout", err: &pgconn.PgError{Code: lockNotAvailable}, want: application.ErrUnavailable},
		{name: "connection dropped mid-commit", err: fmt.Errorf("commit: %w", io.ErrUnexpectedEOF), want: application.ErrOutcomeUnknown},
		{name: "deadline while waiting for the commit", err: fmt.Errorf("commit: %w", context.DeadlineExceeded), want: application.ErrOutcomeUnknown},
		{name: "could not connect", err: &pgconn.ConnectError{}, want: application.ErrTransient},
	}

	for _, tc := range cases {
		got := classify(tc.err)
		if !errors.Is(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	untouched := []error{nil, ledger.ErrInsufficientFunds, &pgconn.PgError{Code: "23505"}}
	for _, err := range untouched {
		got := classify(err)

		isClassified := errors.Is(got, application.ErrTransient) || errors.Is(got, application.ErrOutcomeUnknown) || errors.Is(got, application.ErrUnavailable)
		if isClassified || !errors.Is(got, err) {
			t.Errorf("%v should pass through unchanged, got %v", err, got)
		}
	}
}
