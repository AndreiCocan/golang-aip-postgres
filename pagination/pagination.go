package pagination

import (
	"fmt"
	"slices"
	"strings"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"
	aippagination "github.com/AndreiCocan/golang-aip/pagination"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
)

// Cursor is the position of a row in the sort of a list: a copy of the row
// with only its sort fields set. Make it with [CursorOf]. T is the domain
// type of the [fieldmapping.Mapping] of the list.
type Cursor[T any] struct {
	// Row is the copy of the row. Its fields that are not in the sort are
	// zero.
	Row T
}

// CursorOf returns the cursor of row in the sort of order. row is a pointer
// to a value of the domain type of mapping.
//
// order is the full order of the list, such as the result of CombineOrderBy
// of [github.com/AndreiCocan/golang-aip-postgres/ordering.ServerOrderRules].
// Each key of order must be a sort key of mapping (see [fieldmapping.Mapping.SortKeyValues]), and
// mapping must come from the domain type of row, with
// [fieldmapping.New]. CursorOf panics when a key of order is not a sort
// key of mapping, or when row has a resource name that does not match its
// pattern. These are programming errors.
func CursorOf[T any](
	order *aipordering.CheckedOrderBy,
	mapping *fieldmapping.Mapping,
	row *T,
) Cursor[T] {
	var c Cursor[T]

	for _, key := range order.Keys {
		if _, ok := mapping.SortKeyValues(key.Path(), row); !ok {
			panic(fmt.Sprintf("pagination: CursorOf: field %q has no sort value", key.Path()))
		}

		mapping.CopyField(key.Path(), &c.Row, row)
	}

	return c
}

// After returns the condition of the rows after c in the sort of order. A
// row is after c when, for some sort column, the row is equal to c on all
// the columns before it, and after c on that column. bind adds a value to
// the arguments of the query and returns its placeholder, such as "$3".
//
// After returns [aippagination.ErrInvalidPageToken] when a sort field of c
// has a value that does not fit its columns: a resource name that does not
// match its pattern, or a value that
// [fieldmapping.Mapping.ValidSortKeyValues] rejects. It returns the same
// error for a key of order that is not a sort key, because [CursorOf] cannot
// make a cursor for such a key. Such a cursor comes from a page token that a
// client changed, or that a different order made, so the error is bad
// client input. Then After does not call bind.
//
// order is the full order of the list, such as the result of
// CombineOrderBy of
// [github.com/AndreiCocan/golang-aip-postgres/ordering.ServerOrderRules],
// and mapping must come from the domain type T, with [fieldmapping.New].
// After panics when order has no keys, or when a key of order is not a
// field of mapping. These are programming errors.
func After[T any](
	order *aipordering.CheckedOrderBy,
	mapping *fieldmapping.Mapping,
	c Cursor[T],
	bind func(any) string,
) (string, error) {
	columns, err := sortColumns(order, mapping, &c.Row)
	if err != nil {
		return "", err
	}

	after := make([]string, len(columns))
	equal := make([]string, len(columns))

	for i, column := range columns {
		after[i], equal[i] = seekConditions(column, bind)
	}

	disjuncts := make([]string, len(columns))
	for i := range columns {
		disjuncts[i] = "(" + strings.Join(slices.Concat(equal[:i], after[i:i+1]), " AND ") + ")"
	}

	return "(" + strings.Join(disjuncts, " OR ") + ")", nil
}

// sortColumn is a sort column and its value in a cursor.
type sortColumn struct {
	// column is the quoted column.
	column string
	// desc reports whether the column sorts in descending order.
	desc bool
	// value is the value of the column in the cursor, or nil for NULL.
	value any
}

// sortColumns returns the sort columns of order with their values in row.
// It returns [aippagination.ErrInvalidPageToken] when a value does not fit
// its column, or when a key is not a sort key. It panics when order has no
// keys, or when a key is not a field of mapping.
func sortColumns(
	order *aipordering.CheckedOrderBy,
	mapping *fieldmapping.Mapping,
	row any,
) ([]sortColumn, error) {
	if order == nil || len(order.Keys) == 0 {
		panic("pagination: After: the order has no keys")
	}

	var sorted []sortColumn

	for _, key := range order.Keys {
		columns, ok := mapping.Columns(key.Path())
		if !ok {
			panic(fmt.Sprintf("pagination: After: field %q has no column", key.Path()))
		}

		// The field exists, so SortKeyValues fails for a field that is not a
		// sort key, or for a resource name that does not match its pattern.
		values, ok := mapping.SortKeyValues(key.Path(), row)
		if !ok || !mapping.ValidSortKeyValues(key.Path(), values) {
			return nil, aippagination.ErrInvalidPageToken
		}

		for i, column := range columns {
			sorted = append(sorted, sortColumn{column: column, desc: key.Desc, value: values[i]})
		}
	}

	return sorted, nil
}

// seekConditions returns two conditions on column: first, the rows after
// the cursor; then, the rows equal to the cursor. NULL sorts after all values
// in ascending order, and before them in descending order.
func seekConditions(column sortColumn, bind func(any) string) (string, string) {
	c := column.column

	if column.value == nil {
		if column.desc {
			return c + " IS NOT NULL", c + " IS NULL"
		}

		return "FALSE", c + " IS NULL"
	}

	p := bind(column.value)

	if column.desc {
		return c + " < " + p, c + " = " + p
	}

	return "(" + c + " > " + p + " OR " + c + " IS NULL)", c + " = " + p
}
