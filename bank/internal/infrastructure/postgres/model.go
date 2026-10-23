package postgres

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// A row model is a struct that mirrors one table: each field carries the
// column it maps to in a `db` tag, and, when the column has to be read
// through an expression (an enum as text, a nullable as its zero), that
// expression in a `read` tag. Row models live only in this package; the
// stores turn them into domain values and back.
//
//	type transitionRow struct {
//		Seq  int    `db:"seq"`
//		From string `db:"from_status" read:"coalesce(from_status::text, '')"`
//	}

// column is one field of a row model.
type column struct {
	name  string
	read  string
	field []int
}

// columnCache keeps each row model's columns; there is one entry per row
// model type, a fixed and small set.
var columnCache sync.Map

// columnsOf lists a row model's columns in field order.
func columnsOf[T any]() []column {
	var zero T

	model := reflect.TypeOf(zero)

	cached, isCached := columnCache.Load(model)
	if isCached {
		columns, _ := cached.([]column)

		return columns
	}

	columns := readColumns(model)
	columnCache.Store(model, columns)

	return columns
}

func readColumns(model reflect.Type) []column {
	if model.Kind() != reflect.Struct {
		panic(fmt.Sprintf("row model %s is not a struct", model))
	}

	columns := make([]column, 0, model.NumField())

	for index := range model.NumField() {
		columns = append(columns, columnsOfField(model, model.Field(index))...)
	}

	return columns
}

// columnsOfField is a field's column, or, for an embedded row model, its
// columns, as pgx flattens it when scanning.
func columnsOfField(model reflect.Type, field reflect.StructField) []column {
	isEmbedded := field.Anonymous && field.Type.Kind() == reflect.Struct
	if isEmbedded {
		inner := readColumns(field.Type)

		for k := range inner {
			inner[k].field = append([]int{field.Index[0]}, inner[k].field...)
		}

		return inner
	}

	name := field.Tag.Get("db")
	if name == "" {
		panic(fmt.Sprintf("row model %s: field %s has no db tag", model, field.Name))
	}

	read := field.Tag.Get("read")
	if read == "" {
		read = name
	}

	return []column{{name: name, read: read, field: field.Index}}
}

// selectList is the SELECT list of a row model, each expression named after
// its column so the rows scan back by name.
func selectList[T any]() string {
	columns := columnsOf[T]()
	parts := make([]string, len(columns))

	for k, col := range columns {
		parts[k] = col.read

		if col.read != col.name {
			parts[k] = col.read + " AS " + col.name
		}
	}

	return strings.Join(parts, ", ")
}

// valuesOf reads a row model's fields as named arguments, one per column.
func valuesOf[T any](row T) map[string]any {
	value := reflect.ValueOf(row)
	columns := columnsOf[T]()
	values := make(map[string]any, len(columns))

	for _, col := range columns {
		values[col.name] = value.FieldByIndex(col.field).Interface()
	}

	return values
}
