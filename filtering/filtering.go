package filtering

import (
	"fmt"
	"slices"
	"strings"

	aipfiltering "github.com/AndreiCocan/golang-aip/filtering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
)

// Where returns the checked filter as a PostgreSQL condition on the columns
// of mapping. bind adds a value to the arguments of the query and returns
// its placeholder, such as "$3". An empty or nil filter gives TRUE, so the
// result can always follow WHERE or AND.
//
// Each part of a filter gives this SQL:
//
//   - An equality test on a string field, not under NOT, compares the
//     columns with [fieldmapping.Mapping.EqualCondition]. Thus an index on
//     the columns can find the rows.
//   - A wildcard pattern gives a case-sensitive LIKE match.
//   - A duration gives a make_interval call.
//   - A null comparison or a presence test gives IS NULL or IS NOT NULL.
//   - A key of a [fieldmapping.JSONBColumn] map is a bind value. labels:env
//     and labels.env:* test that the key exists. labels.env = "prod"
//     compares the value of the key, with containment (@>) when not under
//     NOT.
//
// A comparison on a NULL column does not match, also under != and NOT.
// This is the AIP semantics for unset fields.
//
// A GIN index with the default jsonb_ops operator class can find the rows
// of a key test and of a containment test. The jsonb_path_ops class
// supports only containment.
//
// A bare search term matches when it is a case-insensitive substring of at
// least one field with the search option. A * in the term matches any
// characters. Only a trigram index of the pg_trgm extension can find these
// rows. Without a search field, a search term matches nothing, also under
// NOT.
//
// The schema of checked and mapping must come from the same domain type,
// with [aipfiltering.SchemaFromTags] and [fieldmapping.New], so that each
// field of the filter has a column. Where panics when a field of the
// filter has no column, or when the filter calls a function that has no
// expander and is not in funcs. These are programming errors, not bad
// client input.
func Where(
	checked *aipfiltering.CheckedFilter,
	mapping *fieldmapping.Mapping,
	bind func(any) string,
	funcs ...SQLFunc,
) string {
	if checked == nil || checked.Expr == nil {
		return "TRUE"
	}

	w := &writer{mapping: mapping, bind: bind, funcs: funcs}
	w.writeExpr(checked.Expr)

	return w.sql.String()
}

// SQLFunc is the SQL of a filter function that the filter schema declares
// without an expander, such as a full-text or a geographic test. Give it to
// [Where].
//
// A call that returns bool can be a condition by itself, such as
// hasPrefix(title, "War"). Then Where compares the call with TRUE.
type SQLFunc struct {
	// Name is the name of the function in the filter schema.
	Name string
	// SQL returns the SQL of a call. args holds the SQL of each argument in
	// order: the expression of a field, the placeholder of a value, or the
	// SQL of a nested call.
	SQL func(args []string) string
}

// writer writes the SQL of a checked filter.
type writer struct {
	// mapping maps the fields of the filter to columns.
	mapping *fieldmapping.Mapping
	// bind adds a value to the arguments of the query and returns its
	// placeholder.
	bind func(any) string
	// funcs holds the SQL of the functions without an expander.
	funcs []SQLFunc
	// sql holds the SQL that the writer wrote.
	sql strings.Builder
	// negated reports whether the current node is under an odd number of
	// NOTs. Elsewhere FALSE and NULL both reject the row.
	negated bool
}

// writeExpr writes the node e and its children. It panics on a node type that
// the checker does not make.
func (w *writer) writeExpr(e aipfiltering.Expr) {
	switch e := e.(type) {
	case *aipfiltering.And:
		w.writeJunction(e.Operands, " AND ")
	case *aipfiltering.Or:
		w.writeJunction(e.Operands, " OR ")
	case *aipfiltering.Not:
		w.sql.WriteString("NOT (")
		w.negated = !w.negated
		w.writeExpr(e.Operand)
		w.negated = !w.negated
		w.sql.WriteString(")")
	case *aipfiltering.Comparison:
		w.writeComparison(e)
	case *aipfiltering.Search:
		w.writeSearch(e)
	default:
		panic(fmt.Sprintf("filtering: Where: unknown filter node %T", e))
	}
}

// writeJunction writes operands in parentheses, with sep, such as " AND ",
// between them.
func (w *writer) writeJunction(operands []aipfiltering.Expr, sep string) {
	w.sql.WriteString("(")

	for i, op := range operands {
		if i > 0 {
			w.sql.WriteString(sep)
		}

		w.writeExpr(op)
	}

	w.sql.WriteString(")")
}

// writeSearch writes a Search node: each term must match at least one search
// field, so terms join with AND and, within a term, fields with OR. Each
// term is bound once and used by all the fields. A term on a NULL field
// gives SQL NULL, so the field does not match, also under NOT. Without
// search fields, the search is NULL for the same reason.
func (w *writer) writeSearch(s *aipfiltering.Search) {
	targets := w.mapping.SearchExprs()
	if len(targets) == 0 {
		w.sql.WriteString("NULL::boolean")

		return
	}

	if len(s.Terms) > 1 {
		w.sql.WriteString("(")
	}

	for i, term := range s.Terms {
		if i > 0 {
			w.sql.WriteString(" AND ")
		}

		w.writeSearchTerm(term, targets)
	}

	if len(s.Terms) > 1 {
		w.sql.WriteString(")")
	}
}

// writeSearchTerm writes one term as an OR across targets, each a substring
// ILIKE match. The LIKE metacharacters of the term are escaped, and each *
// becomes the % wildcard.
func (w *writer) writeSearchTerm(term string, targets []string) {
	parts := strings.Split(term, "*")
	for i, part := range parts {
		parts[i] = likeEscape(part)
	}

	placeholder := w.bind("%" + strings.Join(parts, "%") + "%")

	if len(targets) > 1 {
		w.sql.WriteString("(")
	}

	for i, target := range targets {
		if i > 0 {
			w.sql.WriteString(" OR ")
		}

		w.sql.WriteString(target)
		w.sql.WriteString(" ILIKE ")
		w.sql.WriteString(placeholder)
	}

	if len(targets) > 1 {
		w.sql.WriteString(")")
	}
}

// writeComparison writes c, whose left side is a field or a function call. It
// panics on another left side.
func (w *writer) writeComparison(c *aipfiltering.Comparison) {
	switch left := c.Left.(type) {
	case *aipfiltering.Field:
		w.writeFieldComparison(left, c)
	case *aipfiltering.FuncCall:
		w.writeOperation(w.callSQL(left), c)
	default:
		panic(fmt.Sprintf("filtering: Where: comparison on %T, which has no SQL", c.Left))
	}
}

// writeFieldComparison writes c on field: a map comparison for a map
// field, a [fieldmapping.Mapping.EqualCondition] test where it is correct,
// and an operation on the filter expression in the other cases.
func (w *writer) writeFieldComparison(field *aipfiltering.Field, c *aipfiltering.Comparison) {
	first := field.Segments[0]

	if first.Type.Kind == aipfiltering.KindMap {
		w.writeMapComparison(w.mapColumn(first.Name), field, c)

		return
	}

	// EqualCondition lets an index find the rows, but can give FALSE where the
	// expression gives NULL, so it is only correct outside NOT.
	if !w.negated && c.Op == aipfiltering.OpEquals && c.Right.Kind == aipfiltering.KindString &&
		len(field.Segments) == 1 {
		if equal, ok := w.mapping.EqualCondition(first.Name, c.Right.Text, w.bind); ok {
			w.sql.WriteString(equal)

			return
		}
	}

	w.writeOperation(w.fieldExprSQL(field), c)
}

// fieldExprSQL returns the SQL expression of the value of field: the
// expression of a mapped field, or the text of a key of a jsonb map.
func (w *writer) fieldExprSQL(field *aipfiltering.Field) string {
	first := field.Segments[0]

	if first.Type.Kind == aipfiltering.KindMap && len(field.Segments) == 2 {
		return "(" + w.mapColumn(first.Name) + " ->> " + w.bind(field.Segments[1].Name) + ")"
	}

	expr, ok := w.mapping.FilterExpr(first.Name)
	if !ok || len(field.Segments) > 1 {
		panic(fmt.Sprintf("filtering: Where: field %q has no column", field.Path()))
	}

	return expr
}

// mapColumn returns the column of the jsonb map field name.
func (w *writer) mapColumn(name string) string {
	columns, ok := w.mapping.Columns(name)
	if !ok {
		panic(fmt.Sprintf("filtering: Where: field %q has no column", name))
	}

	return columns[0]
}

// callSQL returns the SQL of the function call c, from the [SQLFunc] of
// its name.
func (w *writer) callSQL(c *aipfiltering.FuncCall) string {
	i := slices.IndexFunc(w.funcs, func(f SQLFunc) bool { return f.Name == c.Name })
	if i == -1 {
		panic(fmt.Sprintf("filtering: Where: function %q has no SQL", c.Name))
	}

	args := make([]string, len(c.Args))

	for j, a := range c.Args {
		switch a := a.(type) {
		case *aipfiltering.Field:
			args[j] = w.fieldExprSQL(a)
		case aipfiltering.Value:
			args[j] = w.valueSQL(a)
		case *aipfiltering.FuncCall:
			args[j] = w.callSQL(a)
		default:
			panic(fmt.Sprintf("filtering: Where: function argument %T has no SQL", a))
		}
	}

	return w.funcs[i].SQL(args)
}

// writeOperation writes the operator and the right side of the comparison c
// on the SQL expression expr.
func (w *writer) writeOperation(expr string, c *aipfiltering.Comparison) {
	w.sql.WriteString(expr)

	switch c.Right.Kind {
	case aipfiltering.KindNull:
		if c.Op == aipfiltering.OpEquals {
			w.sql.WriteString(" IS NULL")
		} else {
			w.sql.WriteString(" IS NOT NULL")
		}
	case aipfiltering.KindPresence:
		w.sql.WriteString(" IS NOT NULL")
	case aipfiltering.KindPattern:
		if c.Op == aipfiltering.OpNotEquals {
			w.sql.WriteString(" NOT LIKE ")
		} else {
			w.sql.WriteString(" LIKE ")
		}

		w.sql.WriteString(w.bind(likePattern(c.Right.Pattern)))
	default:
		w.sql.WriteString(" ")
		w.sql.WriteString(sqlOperator(c.Op))
		w.sql.WriteString(" ")
		w.sql.WriteString(w.valueSQL(c.Right))
	}
}

// valueSQL returns the SQL of the checked value v: a placeholder, or a
// make_interval call on one for a duration.
func (w *writer) valueSQL(v aipfiltering.Value) string {
	switch v.Kind {
	case aipfiltering.KindNull:
		return "NULL"
	case aipfiltering.KindInt:
		// PostgreSQL compares a smaller integer column with bigint and
		// still uses its index. Without the cast, the value takes the type
		// of the column, and a value out of its range is a driver error.
		return w.bind(v.Int) + "::bigint"
	case aipfiltering.KindDuration:
		return "make_interval(secs => " + w.bind(v.Duration.Seconds()) + ")"
	case aipfiltering.KindPattern:
		// A function argument is a literal, so * is just a character.
		parts := make([]string, len(v.Pattern))
		for i, p := range v.Pattern {
			if p.Wildcard {
				parts[i] = "*"
			} else {
				parts[i] = p.Literal
			}
		}

		return w.bind(strings.Join(parts, ""))
	default:
		return w.bind(bindValue(v))
	}
}

// writeMapComparison writes c on the jsonb map column, or on a key of it. The
// map has string values, so a key has no subfields.
func (w *writer) writeMapComparison(
	column string,
	field *aipfiltering.Field,
	c *aipfiltering.Comparison,
) {
	if len(field.Segments) == 1 {
		// The checker allows only the presence test labels:* on the map.
		w.sql.WriteString("(")
		w.sql.WriteString(column)
		w.sql.WriteString(" <> '{}'::jsonb)")

		return
	}

	key := field.Segments[1].Name

	if c.Right.Kind == aipfiltering.KindPresence {
		w.sql.WriteString("(")
		w.sql.WriteString(column)
		w.sql.WriteString(" ? ")
		w.sql.WriteString(w.bind(key))
		w.sql.WriteString(")")

		return
	}

	// Containment lets a GIN index find the rows, but gives FALSE where ->>
	// gives NULL, so it is only correct outside NOT. The map holds strings,
	// so containment and text equality agree.
	if !w.negated && (c.Op == aipfiltering.OpEquals || c.Op == aipfiltering.OpHas) &&
		c.Right.Kind == aipfiltering.KindString {
		w.sql.WriteString("(")
		w.sql.WriteString(column)
		w.sql.WriteString(" @> jsonb_build_object(")
		w.sql.WriteString(w.bind(key))
		w.sql.WriteString("::text, ")
		w.sql.WriteString(w.bind(c.Right.Text))
		w.sql.WriteString("::text))")

		return
	}

	w.writeOperation("("+column+" ->> "+w.bind(key)+")", c)
}

// bindValue converts a scalar checked value to a bind value.
func bindValue(v aipfiltering.Value) any {
	switch v.Kind {
	case aipfiltering.KindInt:
		return v.Int
	case aipfiltering.KindFloat:
		return v.Float
	case aipfiltering.KindBool:
		return v.Bool
	case aipfiltering.KindTimestamp:
		return v.Time
	default: // KindString, KindEnum
		return v.Text
	}
}

// likePattern returns pattern parts as a LIKE pattern: wildcards become %,
// and the LIKE metacharacters of literal parts are escaped with \.
func likePattern(parts []aipfiltering.PatternPart) string {
	var b strings.Builder

	for _, p := range parts {
		if p.Wildcard {
			b.WriteString("%")

			continue
		}

		b.WriteString(likeEscape(p.Literal))
	}

	return b.String()
}

// likeEscape escapes the LIKE metacharacters in s so that it matches
// literally. The escape character is \, the default of LIKE, so the SQL
// needs no ESCAPE clause.
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)

	return strings.ReplaceAll(s, `_`, `\_`)
}

// sqlOperator returns the SQL operator of the comparison operator op.
func sqlOperator(op aipfiltering.Operator) string {
	switch op {
	case aipfiltering.OpNotEquals:
		return "<>"
	case aipfiltering.OpLess:
		return "<"
	case aipfiltering.OpLessEquals:
		return "<="
	case aipfiltering.OpGreater:
		return ">"
	case aipfiltering.OpGreaterEquals:
		return ">="
	default: // OpEquals, and OpHas on map values
		return "="
	}
}
