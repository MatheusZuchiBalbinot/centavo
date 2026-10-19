package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// statement is one SQL statement of a store: the text, its parameters and
// the step it performs, which names it in every error it returns.
type statement struct {
	step string
	sql  string
	args []any
}

// newStatement binds a statement's parameters.
func newStatement(step, sql string, values ...any) statement {
	return statement{step: step, sql: sql, args: values}
}

// execute runs a write and reports how many rows it touched.
func execute(ctx context.Context, db querier, st statement) (int64, error) {
	tag, err := db.Exec(ctx, st.sql, st.args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", st.step, err)
	}

	return tag.RowsAffected(), nil
}

// executeOne runs a write that must touch exactly one row; any other count is
// errNone, the store's own error for "that row is not there".
func executeOne(ctx context.Context, db querier, st statement, errNone error) error {
	touched, err := execute(ctx, db, st)
	if err != nil {
		return err
	}

	if touched != 1 {
		return errNone
	}

	return nil
}

// scanRow reads the one row a query must return. A missing row stays
// pgx.ErrNoRows under the wrap, for the caller to map.
func scanRow(ctx context.Context, db querier, st statement, dest ...any) error {
	err := db.QueryRow(ctx, st.sql, st.args...).Scan(dest...)
	if err != nil {
		return fmt.Errorf("%s: %w", st.step, err)
	}

	return nil
}

// scanOptionalRow reads a row that may be missing, and reports whether it was
// there.
func scanOptionalRow(ctx context.Context, db querier, st statement, dest ...any) (bool, error) {
	err := scanRow(ctx, db, st, dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}

// queryRows reads every row of a query with scan.
func queryRows[T any](ctx context.Context, db querier, st statement, scan pgx.RowToFunc[T]) ([]T, error) {
	rows, err := db.Query(ctx, st.sql, st.args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.step, err)
	}

	collected, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.step, err)
	}

	return collected, nil
}
