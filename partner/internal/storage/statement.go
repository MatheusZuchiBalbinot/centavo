package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// querier is what both the database and a transaction run statements on.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// statement is one SQL statement of the truth: the text, its parameters and
// the step it performs, which names it in every error it returns.
type statement struct {
	step string
	sql  string
	args []any
}

// newStatement binds a statement's parameters.
func newStatement(step, text string, values ...any) statement {
	return statement{step: step, sql: text, args: values}
}

// execute runs a write and reports how many rows it touched.
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

// scanRow reads the one row a query must return. A missing row stays
// sql.ErrNoRows under the wrap, for the caller to map.
func scanRow(ctx context.Context, db querier, st statement, dest ...any) error {
	err := db.QueryRowContext(ctx, st.sql, st.args...).Scan(dest...)
	if err != nil {
		return fmt.Errorf("%s: %w", st.step, err)
	}

	return nil
}

// queryRows reads every row of a query with scan.
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

// notFoundOr turns a missing row into errNone.
func notFoundOr(err, errNone error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errNone
	}

	return err
}
