package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// step names the statement in every error it returns.
type statement struct {
	step string
	sql  string
	args []any
}

func newStatement(step, text string, values ...any) statement {
	return statement{step: step, sql: text, args: values}
}

func execute(ctx context.Context, db querier, st statement) (int64, error) {
	result, err := db.ExecContext(ctx, st.sql, st.args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", st.step, err)
	}

	touched, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", st.step, err)
	}

	return touched, nil
}

// A missing row stays sql.ErrNoRows under the wrap, for the caller to map.
func scanRow(ctx context.Context, db querier, st statement, dest ...any) error {
	err := db.QueryRowContext(ctx, st.sql, st.args...).Scan(dest...)
	if err != nil {
		return fmt.Errorf("%s: %w", st.step, err)
	}

	return nil
}

func queryRows[T any](ctx context.Context, db querier, st statement, scan func(rows *sql.Rows) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, st.sql, st.args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.step, err)
	}

	defer rows.Close()

	collected := []T{}

	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", st.step, err)
		}

		collected = append(collected, value)
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.step, err)
	}

	return collected, nil
}

func notFoundOr(err, errNone error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errNone
	}

	return err
}
