package fieldmapping

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/AndreiCocan/golang-aip/aiptag"
	aipordering "github.com/AndreiCocan/golang-aip/ordering"
	"github.com/AndreiCocan/golang-aip/resourcename"
)

// kind is how a filter compares the columns of an entry.
type kind int

const (
	// kindColumn compares the column with the value: column op value.
	kindColumn kind = iota
	// kindUUID compares the column as text.
	kindUUID
	// kindJSONB compares the keys and values of a jsonb object.
	kindJSONB
	// kindResourceName compares the full resource name that the columns
	// give.
	kindResourceName
)

// Entry maps one field of a domain type to one or more columns. Make it
// with [Column], [UUIDColumn], [JSONBColumn], or [ResourceNameColumns], and
// give it to [New].
type Entry struct {
	// ptr points to the field of the domain type.
	ptr any
	// columns holds the unquoted columns, with optional table qualifiers.
	columns []string
	// kind is how a filter compares the columns.
	kind kind
	// pattern is the resource name pattern of a [ResourceNameColumns]
	// entry, or "".
	pattern string
}

// ResourceNameColumns maps the string field p to text columns, one for each
// variable of pattern, in the order of pattern. p holds the full resource
// name, such as "tenants/acme/instances/db1". Each column holds the ID of
// its variable, such as "acme" or "db1".
//
// Give the pattern constant of the domain type, which its resource name
// helpers also use. Then the pattern has one source. The aip tag does not
// hold the pattern.
//
// A column can have a table qualifier, for a table that the query joins.
// A column must hold IDs without "/". A filter compares the full resource
// name that pattern and the columns give. An ORDER BY sorts by the
// columns, in the order of pattern. This order is total and stable, but it
// can be different from the order of the full names as strings, for
// example when an ID contains a character that sorts before "/".
func ResourceNameColumns(p *string, pattern string, columns ...string) Entry {
	return Entry{ptr: p, columns: columns, kind: kindResourceName, pattern: pattern}
}

// UUIDColumn maps the string field p to the uuid column column. A filter
// compares the column as text, so that a pattern or a malformed value
// cannot cause a SQL error.
func UUIDColumn(p *string, column string) Entry {
	return Entry{ptr: p, columns: []string{column}, kind: kindUUID}
}

// JSONBColumn maps the map field p to the jsonb column column, which holds
// the map as a JSON object. A filter can name any key of the map. The map
// has no order, so it is not a sort key.
func JSONBColumn(p *map[string]string, column string) Entry {
	return Entry{ptr: p, columns: []string{column}, kind: kindJSONB}
}

// Column maps the field p to the column of a type that compares with the
// Go value of the field, such as a text column for a string field, a
// bigint column for an int64 field, a timestamptz column for a [time.Time]
// field, or an interval column for a [time.Duration] field. The type of p
// must not be a map or a slice.
//
// The column can be NULL for an unset field, such as a zero time. A NULL
// column matches no comparison, also under != and NOT, and sorts after all
// other values in ascending order.
func Column[V any](p *V, column string) Entry {
	return Entry{ptr: p, columns: []string{column}, kind: kindColumn}
}

// Mapping maps the fields of a domain type to columns, by the path of each
// field. Build it with [New]. It is immutable and safe for concurrent
// use.
type Mapping struct {
	// entries holds the compiled entry of each field, by its path.
	entries map[string]*compiledEntry
	// searchExprs holds the filter expressions of the fields with the
	// search option, in the order of the entries.
	searchExprs []string
	// domainType is *T, the type of the domain values of
	// [Mapping.SortKeyValues] and [Mapping.CopyField].
	domainType reflect.Type
}

// compiledEntry is the compiled form of an [Entry].
type compiledEntry struct {
	// index is the index of the struct field, for
	// [reflect.Value.FieldByIndex].
	index []int
	// typ is the type of the struct field.
	typ reflect.Type
	// zeroIsNull reports whether the zero value of the field is NULL in its
	// column. It is true for a [time.Time].
	zeroIsNull bool
	// columns holds the quoted columns.
	columns []string
	// filterExpr is the SQL expression that a filter compares.
	filterExpr string
	// pattern is the resource name pattern, or "".
	pattern string
	// uuid reports whether the columns are of type uuid.
	uuid bool
}

// New returns the mapping of the entries that fn returns. fn receives a
// pointer to a zero T and returns one [Entry] for each mapped field of it.
//
// A column name can have a table qualifier, such as "t.tenant_id". New
// quotes each part of it.
//
// The mapping is programmer input, so New panics instead of returning
// an error. Call it when the program starts. It panics when:
//
//   - T is not a struct.
//   - An entry does not point to a field of T.
//   - The field of an entry has no valid aip tag, is not exported, or has
//     more than one entry.
//   - A field of T has the filter, order, or search option and has no
//     entry.
//   - The field of a [Column] entry is a map or a slice.
//   - The pattern of a [ResourceNameColumns] entry is not valid or has no
//     variables.
//   - An entry does not have one column for each variable of its pattern,
//     or one column when it has no pattern.
func New[T any](fn func(*T) []Entry) *Mapping {
	var v T

	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("fieldmapping: New: %v is not a struct", t))
	}

	base := reflect.ValueOf(&v).Pointer()
	m := &Mapping{entries: make(map[string]*compiledEntry), domainType: reflect.PointerTo(t)}
	mapped := make(map[string]bool)

	for _, e := range fn(&v) {
		sf := structField(t, base, e.ptr)

		tag, ok, err := aiptag.Parse(sf)
		if err != nil {
			panic("fieldmapping: New: " + err.Error())
		}

		if !ok {
			panic(fmt.Sprintf("fieldmapping: New: field %s has no aip tag", sf.Name))
		}

		if mapped[sf.Name] {
			panic(fmt.Sprintf("fieldmapping: New: field %s is mapped twice", sf.Name))
		}

		mapped[sf.Name] = true

		// SortKeyValues and CopyField use reflection, which cannot read or
		// set an unexported field.
		if !sf.IsExported() {
			panic(fmt.Sprintf("fieldmapping: New: field %s is not exported", sf.Name))
		}

		if e.kind == kindColumn &&
			(sf.Type.Kind() == reflect.Map || sf.Type.Kind() == reflect.Slice) {
			panic(fmt.Sprintf("fieldmapping: New: field %s is a %v", sf.Name, sf.Type.Kind()))
		}

		columns := make([]string, len(e.columns))
		for i, column := range e.columns {
			columns[i] = quoteIdent(column)
		}

		if e.kind == kindResourceName {
			if err := resourcename.ValidatePattern(e.pattern); err != nil {
				panic(fmt.Sprintf("fieldmapping: New: field %s: %v", sf.Name, err))
			}
		}

		segments := patternSegments(e.pattern)
		if e.kind == kindResourceName && countVariables(segments) == 0 {
			// The name would be a constant, so no column could hold it.
			panic(fmt.Sprintf(
				"fieldmapping: New: field %s has the pattern %q, which has no variables",
				sf.Name,
				e.pattern,
			))
		}

		if len(columns) != max(1, countVariables(segments)) {
			panic(fmt.Sprintf(
				"fieldmapping: New: field %s has %d columns for the pattern %q",
				sf.Name,
				len(columns),
				e.pattern,
			))
		}

		expr := filterExpr(e.kind, columns, segments)

		m.entries[tag.Name] = &compiledEntry{
			index:      sf.Index,
			typ:        sf.Type,
			zeroIsNull: sf.Type == reflect.TypeFor[time.Time](),
			columns:    columns,
			filterExpr: expr,
			pattern:    e.pattern,
			uuid:       e.kind == kindUUID,
		}

		if tag.Searchable {
			m.searchExprs = append(m.searchExprs, expr)
		}
	}

	for sf := range t.Fields() {
		tag, ok, err := aiptag.Parse(sf)
		if err != nil {
			panic("fieldmapping: New: " + err.Error())
		}

		if ok && (tag.Filterable || tag.Orderable || tag.Searchable) && !mapped[sf.Name] {
			panic(fmt.Sprintf(
				"fieldmapping: New: field %s has an aip tag and is not mapped",
				sf.Name,
			))
		}
	}

	return m
}

// Columns returns the quoted columns of the field that path names, such
// as "display_name". Only a resource name with more than one variable has
// more than one column. An ORDER BY sorts by the columns in order. Columns
// reports false when no field has the path. The caller must not change the
// slice.
func (m *Mapping) Columns(path string) ([]string, bool) {
	e, ok := m.entries[path]
	if !ok {
		return nil, false
	}

	return e.columns, true
}

// FilterExpr returns the SQL expression of the API value of the field that
// path names, such as "display_name". A filter compares this expression.
// It is the column, except for these entries:
//
//   - [UUIDColumn]: the column as text, so that a pattern or a malformed
//     value cannot cause a SQL error.
//   - [ResourceNameColumns]: the full resource name, which the literal
//     segments of the pattern and the columns of the variables give.
//
// These expressions do not use an index on the columns. For an equality
// test, use [Mapping.EqualCondition]. FilterExpr reports false when no
// field has the path.
func (m *Mapping) FilterExpr(path string) (string, bool) {
	e, ok := m.entries[path]
	if !ok {
		return "", false
	}

	return e.filterExpr, true
}

// EqualCondition returns an SQL condition that is true when value is the
// API value of the field that path names, such as "display_name". bind
// adds a value to the arguments of the query and returns its placeholder.
// EqualCondition reports false when no field has the path.
//
// The condition compares each column with its part of value, so an index
// on the columns can find the rows. The condition is FALSE when value
// cannot be the value of the field:
//
//   - For a [ResourceNameColumns] entry, when value does not match the
//     pattern, has a variable segment, or has a service name. The columns
//     hold IDs without "/" or braces, so no row can match.
//   - For a [UUIDColumn] entry, when value is not a UUID as PostgreSQL
//     writes it.
//
// When a column is NULL, FilterExpr(path) = value is NULL, but the
// condition can be FALSE. Thus use the condition only where FALSE and NULL
// both reject the row. Do not use it under NOT.
func (m *Mapping) EqualCondition(path, value string, bind func(any) string) (string, bool) {
	e, ok := m.entries[path]
	if !ok {
		return "", false
	}

	values := []string{value}

	if e.pattern != "" {
		var ok bool
		if values, ok = nameIDs(value, e.pattern, len(e.columns)); !ok {
			return sqlFalse, true
		}
	}

	conditions := make([]string, len(values))

	for i, v := range values {
		if !e.uuid {
			conditions[i] = e.columns[i] + " = " + bind(v)

			continue
		}

		// The text of a uuid is always canonical, so no other text is equal.
		if !canonicalUUID(v) {
			return sqlFalse, true
		}

		conditions[i] = e.columns[i] + " = " + bind(v) + "::uuid"
	}

	if len(conditions) == 1 {
		return conditions[0], true
	}

	return "(" + strings.Join(conditions, " AND ") + ")", true
}

// SortKeyValues returns the values of the sort key that path names, as
// the domain value v stores them in the columns of the key. A keyset
// cursor holds these values for the last row of a page, so that the next
// page starts after them. The values are in the order of
// [Mapping.Columns], so the two results pair up.
//
// Each field of the mapping is a sort key, except a [JSONBColumn] map,
// which has no order. A sort key can be in the order_by of a request, or
// in the order that the server sets. The order option of the aip tag does
// not limit the sort keys: it only tells which fields the order_by of a
// request can name, which [aipordering.SchemaFromTags] checks.
//
// path is the API name of a field, such as "display_name". v is a pointer
// to a value of the domain type of [New].
//
// A [ResourceNameColumns] entry gives one ID for each variable of its
// pattern, because each variable has its own column. A zero [time.Time]
// gives nil, because the column of an unset time is NULL. Each other value
// has the type of its field.
//
// SortKeyValues reports false when no sort key has the path, or when a
// resource name does not match its pattern. It panics when v is not a
// non-nil pointer to the domain type of [New].
func (m *Mapping) SortKeyValues(path string, v any) ([]any, bool) {
	e, ok := m.sortKeyEntry(path)
	if !ok {
		return nil, false
	}

	fv := m.domainValue("SortKeyValues", v).FieldByIndex(e.index)

	switch {
	case e.pattern != "":
		ids, ok := nameIDs(fv.String(), e.pattern, len(e.columns))
		if !ok {
			return nil, false
		}

		values := make([]any, len(ids))
		for i, id := range ids {
			values[i] = id
		}

		return values, true
	case e.zeroIsNull && fv.IsZero():
		return []any{nil}, true
	default:
		return []any{fv.Interface()}, true
	}
}

// CopyField sets the field that path names in dst to its value in src. dst
// and src are pointers to values of the domain type of [New].
// CopyField reports false when no field has the path. It panics when dst
// or src is not a non-nil pointer to the domain type of [New].
func (m *Mapping) CopyField(path string, dst, src any) bool {
	e, ok := m.entries[path]
	if !ok {
		return false
	}

	to := m.domainValue("CopyField", dst).FieldByIndex(e.index)
	to.Set(m.domainValue("CopyField", src).FieldByIndex(e.index))

	return true
}

// ValidSortKeyValues reports whether values can be the result of
// [Mapping.SortKeyValues] for the sort key that path names. Use it on the
// values of a keyset cursor that comes back from a client, before a query
// binds them. When it reports true, PostgreSQL can read each value as the
// type of its column.
//
// The values are valid when there is one value for each column, and each
// value has the type that SortKeyValues gives. Only a time can be nil. The
// value of a [UUIDColumn] entry must be a UUID as PostgreSQL writes it.
//
// ValidSortKeyValues reports false when no sort key has the path. See
// [Mapping.SortKeyValues] for the sort keys.
func (m *Mapping) ValidSortKeyValues(path string, values []any) bool {
	e, ok := m.sortKeyEntry(path)
	if !ok || len(values) != len(e.columns) {
		return false
	}

	if e.pattern != "" {
		for _, v := range values {
			if _, ok := v.(string); !ok {
				return false
			}
		}

		return true
	}

	v := values[0]

	switch {
	case v == nil:
		return e.zeroIsNull
	case reflect.TypeOf(v) != e.typ:
		return false
	case e.uuid:
		s, ok := v.(string)

		return ok && canonicalUUID(s)
	default:
		return true
	}
}

// SearchExprs returns the SQL expressions of the API values of the fields
// with the search option, in the order of the entries. A bare search term
// of a filter matches these expressions. The caller must not change the
// slice.
func (m *Mapping) SearchExprs() []string {
	return m.searchExprs
}

// SortKeySchema returns the ordering schema of the sort keys of the
// mapping (see [Mapping.SortKeyValues]): the paths that the server can
// sort by. Check the order that the server sets, such as its default
// order_by and its tie-breaker, against it.
//
// It is not the schema of the order_by of a request: that schema, from
// [aipordering.SchemaFromTags], has only the fields with the order option of
// the aip tag.
func (m *Mapping) SortKeySchema() *aipordering.Schema {
	var paths []string

	for _, path := range slices.Sorted(maps.Keys(m.entries)) {
		if _, ok := m.sortKeyEntry(path); ok {
			paths = append(paths, path)
		}
	}

	return aipordering.NewSchema(paths...)
}

// sortKeyEntry returns the entry of the sort key that path names. It
// reports false when no field has the path, and for a [JSONBColumn] map,
// which has no order.
func (m *Mapping) sortKeyEntry(path string) (*compiledEntry, bool) {
	e, ok := m.entries[path]
	if !ok || e.typ.Kind() == reflect.Map {
		return nil, false
	}

	return e, true
}

// domainValue returns the struct value that v points to. It panics when v
// is not a non-nil pointer to the domain type of [New]. method names
// the method in the panic message.
func (m *Mapping) domainValue(method string, v any) reflect.Value {
	r := reflect.ValueOf(v)
	if !r.IsValid() || r.Type() != m.domainType || r.IsNil() {
		panic(
			fmt.Sprintf(
				"fieldmapping: %s: value is a %T, not a non-nil %v",
				method,
				v,
				m.domainType,
			),
		)
	}

	return r.Elem()
}

// nameIDs returns the n IDs of the variables of the resource name name,
// which has the pattern pattern. It reports false when name does not match
// pattern, and for a full resource name with a service name, which no
// column holds.
func nameIDs(name, pattern string, n int) ([]string, bool) {
	// Sscan ignores the service name of a full name, but the columns hold
	// only the relative name.
	if strings.HasPrefix(name, "//") {
		return nil, false
	}

	ids := make([]string, n)

	ptrs := make([]*string, n)
	for i := range ids {
		ptrs[i] = &ids[i]
	}

	if resourcename.Sscan(name, pattern, ptrs...) != nil {
		return nil, false
	}

	return ids, true
}

// sqlFalse is the condition of an equality that no row can satisfy.
const sqlFalse = "FALSE"

// uuidLayout is the text of a UUID as PostgreSQL writes it. Each x is a
// lowercase hexadecimal digit.
const uuidLayout = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"

// canonicalUUID reports whether s is a UUID as PostgreSQL writes it as text,
// in the form of [uuidLayout].
func canonicalUUID(s string) bool {
	if len(s) != len(uuidLayout) {
		return false
	}

	for i := range len(s) {
		c := s[i]

		if uuidLayout[i] == '-' {
			if c != '-' {
				return false
			}

			continue
		}

		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

// structField returns the field of the struct type t that ptr points to.
// base is the address of the struct value. It panics when ptr does not
// point to a field of t.
func structField(t reflect.Type, base uintptr, ptr any) reflect.StructField {
	p := reflect.ValueOf(ptr)
	if p.Kind() != reflect.Pointer || p.IsNil() {
		panic(fmt.Sprintf("fieldmapping: New: %T is not a pointer to a field of %v", ptr, t))
	}

	offset := p.Pointer() - base

	for sf := range t.Fields() {
		if sf.Offset == offset && sf.Type == p.Type().Elem() {
			return sf
		}
	}

	panic(fmt.Sprintf("fieldmapping: New: %T is not a pointer to a field of %v", ptr, t))
}

// patternSegments returns the segments of the resource name pattern
// pattern, or nil for an empty pattern.
func patternSegments(pattern string) []resourcename.Segment {
	var (
		segments []resourcename.Segment
		sc       resourcename.Scanner
	)

	sc.Init(pattern)

	for sc.Scan() {
		segments = append(segments, sc.Segment())
	}

	return segments
}

// countVariables returns the number of variables in segments.
func countVariables(segments []resourcename.Segment) int {
	n := 0

	for _, s := range segments {
		if s.IsVariable() {
			n++
		}
	}

	return n
}

// filterExpr returns the SQL expression that a filter compares for an
// entry of kind k in the quoted columns, with the segments of its resource
// name pattern. Without a pattern, it is the one column. With a pattern,
// it is the full resource name: the literal segments, and one column for
// each variable.
func filterExpr(k kind, columns []string, segments []resourcename.Segment) string {
	if k == kindUUID {
		columns = []string{columns[0] + "::text"}
	}

	if len(segments) == 0 {
		return columns[0]
	}

	var (
		parts   []string
		literal strings.Builder
	)

	next := 0

	for i, s := range segments {
		if i > 0 {
			literal.WriteString("/")
		}

		if !s.IsVariable() {
			literal.WriteString(string(s))

			continue
		}

		if literal.Len() > 0 {
			parts = append(parts, quoteLiteral(literal.String()))
			literal.Reset()
		}

		parts = append(parts, columns[next])
		next++
	}

	if literal.Len() > 0 {
		parts = append(parts, quoteLiteral(literal.String()))
	}

	return "(" + strings.Join(parts, " || ") + ")"
}

// quoteIdent quotes a SQL identifier, with an optional table qualifier.
func quoteIdent(name string) string {
	parts := strings.Split(name, ".")
	for i, part := range parts {
		parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}

	return strings.Join(parts, ".")
}

// quoteLiteral quotes a SQL string literal.
func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}
