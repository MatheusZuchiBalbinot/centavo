package postgres

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The query builders write the plain statements of the stores (read rows
// of a table, insert one, update some) from row models and named
// arguments, so a store reads like what it does. Statements that do more
// (CTEs, locks, functions) stay as SQL text in a statement.
//
// Conditions are SQL fragments with @name placeholders, never values
// spliced into the text: every value travels as an argument.

type args = pgx.NamedArgs

type selectQuery[T any] struct {
	step     string
	from     string
	where    []string
	args     args
	orderBy  string
	hasLimit bool
	isLock   bool
}

func selectFrom[T any](step, table string) selectQuery[T] {
	return selectQuery[T]{step: step, from: table, args: args{}}
}

// Several conditions are joined with AND.
func (q selectQuery[T]) Where(condition string, values args) selectQuery[T] {
	q.where = append(append([]string(nil), q.where...), condition)
	q.args = mergeArgs(q.args, values)

	return q
}

func (q selectQuery[T]) OrderBy(expression string) selectQuery[T] {
	q.orderBy = expression

	return q
}

// The cap travels as an argument, so the SQL text stays the same whatever
// the cap.
func (q selectQuery[T]) Limit(limit int) selectQuery[T] {
	q.hasLimit = true
	q.args = mergeArgs(q.args, args{"limit": limit})

	return q
}

func (q selectQuery[T]) ForUpdate() selectQuery[T] {
	q.isLock = true

	return q
}

func (q selectQuery[T]) SQL() string {
	var text strings.Builder

	text.WriteString("SELECT " + selectList[T]() + " FROM " + q.from)
	writeWhere(&text, q.where)

	if q.orderBy != "" {
		text.WriteString(" ORDER BY " + q.orderBy)
	}

	if q.hasLimit {
		text.WriteString(" LIMIT @limit")
	}

	if q.isLock {
		text.WriteString(" FOR UPDATE")
	}

	return text.String()
}

func (q selectQuery[T]) All(ctx context.Context, db querier) ([]T, error) {
	return queryRows(ctx, db, q.statement(), pgx.RowToStructByName[T])
}

// A missing row stays pgx.ErrNoRows under the wrap, for the store to map.
func (q selectQuery[T]) One(ctx context.Context, db querier) (T, error) {
	rows, err := q.All(ctx, db)
	if err != nil {
		var zero T

		return zero, err
	}

	if len(rows) == 0 {
		var zero T

		return zero, fmt.Errorf("%s: %w", q.step, pgx.ErrNoRows)
	}

	return rows[0], nil
}

func (q selectQuery[T]) First(ctx context.Context, db querier) (T, bool, error) {
	row, err := q.One(ctx, db)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}

	return row, err == nil, err
}

func (q selectQuery[T]) statement() statement {
	return newStatement(q.step, q.SQL(), q.args)
}

type insertQuery struct {
	step      string
	into      string
	columns   []string
	args      args
	conflict  string
	returning []string
}

func insertRow[T any](step, table string, row T) insertQuery {
	columns := columnsOf[T]()
	names := make([]string, len(columns))

	for k, col := range columns {
		names[k] = col.name
	}

	return insertQuery{step: step, into: table, columns: names, args: valuesOf(row)}
}

func (q insertQuery) OnConflictDoNothing(target string) insertQuery {
	q.conflict = target

	return q
}

func (q insertQuery) Returning(columns ...string) insertQuery {
	q.returning = columns

	return q
}

func (q insertQuery) SQL() string {
	placeholders := make([]string, len(q.columns))

	for k, name := range q.columns {
		placeholders[k] = "@" + name
	}

	text := "INSERT INTO " + q.into + " (" + strings.Join(q.columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"

	if q.conflict != "" {
		text += " ON CONFLICT " + q.conflict + " DO NOTHING"
	}

	if len(q.returning) > 0 {
		text += " RETURNING " + strings.Join(q.returning, ", ")
	}

	return text
}

func (q insertQuery) Exec(ctx context.Context, db querier) (int64, error) {
	return execute(ctx, db, newStatement(q.step, q.SQL(), q.args))
}

func (q insertQuery) Scan(ctx context.Context, db querier, dest ...any) error {
	return scanRow(ctx, db, newStatement(q.step, q.SQL(), q.args), dest...)
}

// For an insert that may write nothing, such as one ON CONFLICT DO NOTHING.
func (q insertQuery) ScanOptional(ctx context.Context, db querier, dest ...any) (bool, error) {
	return scanOptionalRow(ctx, db, newStatement(q.step, q.SQL(), q.args), dest...)
}

type updateQuery struct {
	step      string
	table     string
	set       []string
	where     []string
	args      args
	returning []string
}

func update(step, table string) updateQuery {
	return updateQuery{step: step, table: table, args: args{}}
}

func (q updateQuery) Set(column string, value any) updateQuery {
	q.set = append(append([]string(nil), q.set...), column+" = @"+column)
	q.args = mergeArgs(q.args, args{column: value})

	return q
}

func (q updateQuery) SetSQL(assignment string, values args) updateQuery {
	q.set = append(append([]string(nil), q.set...), assignment)
	q.args = mergeArgs(q.args, values)

	return q
}

func (q updateQuery) Where(condition string, values args) updateQuery {
	q.where = append(append([]string(nil), q.where...), condition)
	q.args = mergeArgs(q.args, values)

	return q
}

func (q updateQuery) Returning(columns ...string) updateQuery {
	q.returning = columns

	return q
}

func (q updateQuery) SQL() string {
	var text strings.Builder

	text.WriteString("UPDATE " + q.table + " SET " + strings.Join(q.set, ", "))
	writeWhere(&text, q.where)

	if len(q.returning) > 0 {
		text.WriteString(" RETURNING " + strings.Join(q.returning, ", "))
	}

	return text.String()
}

func (q updateQuery) Exec(ctx context.Context, db querier) (int64, error) {
	st, err := q.statement()
	if err != nil {
		return 0, err
	}

	return execute(ctx, db, st)
}

// Any count but one is errNone, the store's own error for "that row is not
// there".
func (q updateQuery) ExecOne(ctx context.Context, db querier, errNone error) error {
	touched, err := q.Exec(ctx, db)
	if err != nil {
		return err
	}

	if touched != 1 {
		return errNone
	}

	return nil
}

func (q updateQuery) Scan(ctx context.Context, db querier, dest ...any) error {
	st, err := q.statement()
	if err != nil {
		return err
	}

	return scanRow(ctx, db, st, dest...)
}

// statement refuses an update without a condition: that is a bug, never a
// wish to touch every row.
func (q updateQuery) statement() (statement, error) {
	isIncomplete := len(q.set) == 0 || len(q.where) == 0
	if isIncomplete {
		return statement{}, fmt.Errorf("%s: an update needs a Set and a Where", q.step)
	}

	return newStatement(q.step, q.SQL(), q.args), nil
}

func writeWhere(text *strings.Builder, conditions []string) {
	if len(conditions) == 0 {
		return
	}

	text.WriteString(" WHERE " + strings.Join(conditions, " AND "))
}

// A name given twice keeps the newer value.
func mergeArgs(current, values args) args {
	merged := make(args, len(current)+len(values))
	maps.Copy(merged, current)
	maps.Copy(merged, values)

	return merged
}
