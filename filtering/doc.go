// Package filtering translates checked AIP-160 filters into PostgreSQL
// conditions. It is a PostgreSQL dialect for
// [github.com/AndreiCocan/golang-aip/filtering]: a pure string builder with
// no driver dependency, usable with any database/sql PostgreSQL driver, such
// as pgx.
//
// [Where] returns only the condition. The application writes the rest of
// the query: the selected columns, FROM, other conditions, ORDER BY, and
// LIMIT. Where adds the values of the condition to the arguments of the
// query with a bind function that the application gives.
//
// The columns come from a [fieldmapping.Mapping] of the same domain type as
// the filter schema. Where binds the values of a filter as follows:
//
//   - An integer is an int64 with a bigint cast. Thus a value out of the
//     range of a smaller integer column matches no row.
//   - A timestamp is a [time.Time], a boolean is a bool, and an enum is its
//     name.
//   - A duration is a number of seconds in a make_interval call. Thus a
//     duration column must be of type interval.
//
// A wildcard pattern gives a case-sensitive LIKE match. An unset column
// and a missing map key are SQL NULL. Thus a comparison on an unset field
// never matches, also under != and NOT. This is the AIP semantics for
// unset fields.
//
// The SQL of a filter has the same text for all its values. Thus a driver
// that prepares and caches statements, such as pgx by default, can make
// PostgreSQL use one generic plan for all values. A generic plan cannot
// use a B-tree index for a LIKE prefix such as "War*". If a table gets
// such filters, set plan_cache_mode to force_custom_plan on the
// connection.
//
// A filter function with an expander needs nothing more. For a function
// without an expander, such as a full-text or a geographic test, give its
// SQL to Where with an [SQLFunc].
package filtering
