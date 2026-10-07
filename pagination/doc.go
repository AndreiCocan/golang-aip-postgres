// Package pagination writes the keyset seek of a PostgreSQL list: the
// condition of the rows after the last row of the page before. It is a
// pure string builder with no driver dependency, usable with any
// database/sql PostgreSQL driver, such as pgx.
//
// A [Cursor] is the position of a row in the sort of a list: a copy of the
// row with only its sort fields set. [CursorOf] makes the cursor of a row.
// A cursor is a plain Go value of the domain type, so a page token of
// [github.com/AndreiCocan/golang-aip/pagination] can hold it. [After]
// returns the condition of the rows after a cursor. For a cursor that does
// not fit the sort, such as a cursor that a client changed, it returns
// ErrInvalidPageToken of [github.com/AndreiCocan/golang-aip/pagination],
// the same error as for any other page token that is not valid.
//
// The columns and the values come from a [fieldmapping.Mapping] of the same
// domain type as the order_by schema, as for the ordering package of this
// module. A resource name with more than one variable has one column
// for each variable. Thus a list across parents, such as
// "shelves/-/books", compares the parent column and the ID column.
//
// The sort must give each row a unique position. Thus the last key of the
// sort must be a field with a unique value for each row, such as the name.
// NULL sorts as the largest value, as in an ORDER BY without NULLS FIRST or
// NULLS LAST: last in ascending order, and first in descending order.
package pagination
