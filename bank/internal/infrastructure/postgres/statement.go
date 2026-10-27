package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// step names the statement in every error it returns.
type statement struct {
	step string
	sql  string
	args []any
}

func newStatement(step, sql string, values ...any) statement {
	return statement{step: step, sql: sql, args: values}
}

func execute(ctx context.Context, db querier, st statement) (int64, error) {
	tag, err := db.Exec(ctx, st.sql, st.args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", st.step, err)
	}

	return tag.RowsAffected(), nil
}

// A missing row stays pgx.ErrNoRows under the wrap, for the caller to map.
func scanRow(ctx context.Context, db querier, st statement, dest ...any) error {
	err := db.QueryRow(ctx, st.sql, st.args...).Scan(dest...)
	if err != nil {
		return fmt.Errorf("%s: %w", st.step, err)
	}

	return nil
}

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
