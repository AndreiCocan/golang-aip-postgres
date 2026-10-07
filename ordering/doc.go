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
// The order of a list has two parts. The order_by of the request, which
// the client chooses, is checked against the schema of the request, from
// the order option of the aip tag. The server sets the rest: the order of
// a request with no order_by, and a tie-breaker that gives each row one
// position. [ServerOrderRules] hold that part, checked against the paths
// that the server can sort by, and [ServerOrderRules.CombineOrderBy] gives
// the full order to pass to OrderBy.
//
// The natural comparators of PostgreSQL apply. Numeric columns sort by
// number. Text columns sort by their collation. NULL sorts as the largest
// value: last in ascending order, and first in descending order.
package ordering
