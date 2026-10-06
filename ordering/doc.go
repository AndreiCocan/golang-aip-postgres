// Package ordering translates checked order_by expressions into PostgreSQL
// ORDER BY lists. It is a PostgreSQL dialect for
// [github.com/AndreiCocan/golang-aip/ordering]: a pure string builder with
// no driver dependency, usable with any database/sql PostgreSQL driver, such
// as pgx.
//
// [OrderBy] returns the list of sort keys, without the ORDER BY keyword.
// The columns come from a [fieldmapping.Mapping] of the same domain type as
// the order_by schema. The application writes the rest of the query.
//
// The natural comparators of PostgreSQL apply. Numeric columns sort by
// number. Text columns sort by their collation. NULL sorts as the largest
// value: last in ascending order, and first in descending order.
package ordering
