// Package fieldmapping maps the fields of a domain type to the columns of a
// PostgreSQL table. The filtering, ordering, and pagination packages of
// this module read a [Mapping] to write SQL.
//
// Build a Mapping once with [New], next to the domain type. The
// function that you give to New receives a pointer to a zero value of
// the domain type, and returns one [Entry] for each mapped field. Make each
// Entry with the constructor of its column shape:
//
//   - [Column]: one column of a type that compares with the Go value, such
//     as text for a string or timestamptz for a [time.Time].
//   - [UUIDColumn]: one uuid column for a string field.
//   - [JSONBColumn]: one jsonb column for a map[string]string field.
//   - [ResourceNameColumns]: one text column for each variable of a
//     resource name pattern.
//
// An Entry points to its field. Thus the compiler checks the field names
// and types, and the domain type needs no db tags. The path of each field,
// and the List features that it supports, come from its aip tag (see
// [github.com/AndreiCocan/golang-aip/aiptag]).
//
// A Mapping gives, for the path of a field:
//
//   - [Mapping.FilterExpr]: the SQL expression that a filter compares.
//   - [Mapping.EqualCondition]: an equality test that an index can use.
//   - [Mapping.Columns]: the columns that an ORDER BY sorts by.
//   - [Mapping.SortKeyValues]: the values of a domain value in those
//     columns, for a keyset cursor.
//
// Row scanning is not part of the mapping: the application reads its rows.
package fieldmapping
