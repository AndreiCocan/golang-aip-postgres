package filtering_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	aipfiltering "github.com/AndreiCocan/golang-aip/filtering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
	"github.com/AndreiCocan/golang-aip-postgres/filtering"
	"github.com/AndreiCocan/golang-aip-postgres/internal/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

// book is the domain type of the tests.
type book struct {
	Name        string            `aip:"name,filter,order,search"`
	UID         string            `aip:"uid,filter"`
	DisplayName string            `aip:"display_name,filter,order,search"`
	PageCount   int64             `aip:"page_count,filter"`
	Rating      float64           `aip:"rating,filter"`
	Published   bool              `aip:"published,filter"`
	CreateTime  time.Time         `aip:"create_time,filter"`
	ReadTime    time.Duration     `aip:"read_time,filter"`
	AuthorName  string            `aip:"author_name,filter,search"`
	Labels      map[string]string `aip:"labels,filter"`
}

var (
	bookSchema = aipfiltering.SchemaFromTags(book{},
		aipfiltering.Func("hasPrefix",
			aipfiltering.FuncArgs(aipfiltering.KindString, aipfiltering.KindString),
			aipfiltering.FuncReturns(aipfiltering.KindBool),
		),
		aipfiltering.Func("lower",
			aipfiltering.FuncArgs(aipfiltering.KindString),
			aipfiltering.FuncReturns(aipfiltering.KindString),
		),
	)
	// bookFuncs is the SQL of the functions of bookSchema.
	bookFuncs = []filtering.SQLFunc{
		{Name: "hasPrefix", SQL: func(args []string) string {
			return "starts_with(" + args[0] + ", " + args[1] + ")"
		}},
		{Name: "lower", SQL: func(args []string) string {
			return "lower(" + args[0] + ")"
		}},
	}
	bookMapping = fieldmapping.New(func(b *book) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&b.Name, "books/{book}", "book_id"),
			fieldmapping.UUIDColumn(&b.UID, "uid"),
			fieldmapping.Column(&b.DisplayName, "display_name"),
			fieldmapping.Column(&b.PageCount, "page_count"),
			fieldmapping.Column(&b.Rating, "rating"),
			fieldmapping.Column(&b.Published, "published"),
			fieldmapping.Column(&b.CreateTime, "create_time"),
			fieldmapping.Column(&b.ReadTime, "read_time"),
			fieldmapping.Column(&b.AuthorName, "author_name"),
			fieldmapping.JSONBColumn(&b.Labels, "labels"),
		}
	})
)

// noSearch is a domain type without search fields.
type noSearch struct {
	Title string `aip:"title,filter"`
}

// args is the argument list of one query.
type args []any

// bind adds v to the arguments and returns its placeholder.
func (a *args) bind(v any) string {
	*a = append(*a, v)

	return "$" + strconv.Itoa(len(*a))
}

// compile returns the checked form of filter, and fails t when filter is
// not valid.
func compile(t *testing.T, filter string, schema *aipfiltering.Schema) *aipfiltering.CheckedFilter {
	t.Helper()

	checked, err := aipfiltering.Compile(filter, schema)
	if err != nil {
		t.Fatalf("Compile(%q) error = %v", filter, err)
	}

	return checked
}

func TestWhere(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		filter   string
		wantSQL  string
		wantArgs args
	}{
		{
			name:     "empty filter",
			filter:   "",
			wantSQL:  "TRUE",
			wantArgs: nil,
		},
		{
			name:     "string equality",
			filter:   `display_name = "war and peace"`,
			wantSQL:  `"display_name" = $1`,
			wantArgs: args{"war and peace"},
		},
		{
			name:     "wildcard",
			filter:   `display_name = "war*"`,
			wantSQL:  `"display_name" LIKE $1`,
			wantArgs: args{"war%"},
		},
		{
			name:     "negated wildcard escapes LIKE metacharacters",
			filter:   `display_name != "50%_*"`,
			wantSQL:  `"display_name" NOT LIKE $1`,
			wantArgs: args{`50\%\_%`},
		},
		{
			name:     "resource name compares the column",
			filter:   `name = "books/b1"`,
			wantSQL:  `"book_id" = $1`,
			wantArgs: args{"b1"},
		},
		{
			name:     "resource name of another pattern",
			filter:   `name = "shelves/b1"`,
			wantSQL:  `FALSE`,
			wantArgs: nil,
		},
		{
			name:    "resource name with too many segments",
			filter:  `name = "books/b1/b2"`,
			wantSQL: `FALSE`,
		},
		{
			name:     "resource name under not",
			filter:   `NOT name = "books/b1"`,
			wantSQL:  `NOT (('books/' || "book_id") = $1)`,
			wantArgs: args{"books/b1"},
		},
		{
			name:     "resource name not equal",
			filter:   `name != "books/b1"`,
			wantSQL:  `('books/' || "book_id") <> $1`,
			wantArgs: args{"books/b1"},
		},
		{
			name:     "uuid compares the column",
			filter:   `uid = "00000000-0000-4000-8000-000000000001"`,
			wantSQL:  `"uid" = $1::uuid`,
			wantArgs: args{"00000000-0000-4000-8000-000000000001"},
		},
		{
			name:     "uuid not as postgres writes it",
			filter:   `uid = "00000000-0000-4000-8000-00000000000A"`,
			wantSQL:  `FALSE`,
			wantArgs: nil,
		},
		{
			name:     "uuid under double not",
			filter:   `NOT (NOT uid = "00000000-0000-4000-8000-000000000001")`,
			wantSQL:  `NOT (NOT ("uid" = $1::uuid))`,
			wantArgs: args{"00000000-0000-4000-8000-000000000001"},
		},
		{
			name:     "uuid as text",
			filter:   `uid = "0000*"`,
			wantSQL:  `"uid"::text LIKE $1`,
			wantArgs: args{"0000%"},
		},
		{
			name:     "and with typed arguments",
			filter:   `page_count > 100 AND published = true AND rating <= 4.5`,
			wantSQL:  `("page_count" > $1::bigint AND "published" = $2 AND "rating" <= $3)`,
			wantArgs: args{int64(100), true, 4.5},
		},
		{
			name:     "or binds tighter than and",
			filter:   `published = true AND page_count = 1 OR page_count = 2`,
			wantSQL:  `("published" = $1 AND ("page_count" = $2::bigint OR "page_count" = $3::bigint))`,
			wantArgs: args{true, int64(1), int64(2)},
		},
		{
			name:     "not and minus",
			filter:   `NOT page_count = 1 -published = true`,
			wantSQL:  `(NOT ("page_count" = $1::bigint) AND NOT ("published" = $2))`,
			wantArgs: args{int64(1), true},
		},
		{
			name:     "timestamp",
			filter:   `create_time > "2021-01-01T00:00:00Z"`,
			wantSQL:  `"create_time" > $1`,
			wantArgs: args{time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
		{
			name:     "null timestamp",
			filter:   `create_time = null`,
			wantSQL:  `"create_time" IS NULL`,
			wantArgs: nil,
		},
		{
			name:     "duration",
			filter:   `read_time < 1.5s`,
			wantSQL:  `"read_time" < make_interval(secs => $1)`,
			wantArgs: args{1.5},
		},
		{
			name:     "map key presence",
			filter:   `labels:env`,
			wantSQL:  `("labels" ? $1)`,
			wantArgs: args{"env"},
		},
		{
			name:     "map key presence with star",
			filter:   `labels.env:*`,
			wantSQL:  `("labels" ? $1)`,
			wantArgs: args{"env"},
		},
		{
			name:     "map not empty",
			filter:   `labels:*`,
			wantSQL:  `("labels" <> '{}'::jsonb)`,
			wantArgs: nil,
		},
		{
			name:     "map value equality",
			filter:   `labels.env = "prod"`,
			wantSQL:  `("labels" @> jsonb_build_object($1::text, $2::text))`,
			wantArgs: args{"env", "prod"},
		},
		{
			name:     "map value equality under not",
			filter:   `NOT labels.env = "prod"`,
			wantSQL:  `NOT (("labels" ->> $1) = $2)`,
			wantArgs: args{"env", "prod"},
		},
		{
			name:     "map value has",
			filter:   `labels.env:prod`,
			wantSQL:  `("labels" @> jsonb_build_object($1::text, $2::text))`,
			wantArgs: args{"env", "prod"},
		},
		{
			name:     "map value wildcard",
			filter:   `labels.env != "p*"`,
			wantSQL:  `("labels" ->> $1) NOT LIKE $2`,
			wantArgs: args{"env", "p%"},
		},
		{
			name:   "search binds each term once",
			filter: `Tolstoy peace`,
			wantSQL: `((('books/' || "book_id") ILIKE $1 OR "display_name" ILIKE $1` +
				` OR "author_name" ILIKE $1)` +
				` AND (('books/' || "book_id") ILIKE $2 OR "display_name" ILIKE $2` +
				` OR "author_name" ILIKE $2))`,
			wantArgs: args{"%Tolstoy%", "%peace%"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got args

			gotSQL := filtering.Where(compile(t, tt.filter, bookSchema), bookMapping, got.bind)
			if gotSQL != tt.wantSQL {
				t.Errorf("Where(%q) SQL = %s, want %s", tt.filter, gotSQL, tt.wantSQL)
			}

			if diff := cmp.Diff(tt.wantArgs, got); diff != "" {
				t.Errorf("Where(%q) args mismatch (-want +got):\n%s", tt.filter, diff)
			}
		})
	}
}

func TestWhere_argumentsBefore(t *testing.T) {
	t.Parallel()

	a := args{"tenant-a"}
	got := filtering.Where(compile(t, `labels.env = "prod"`, bookSchema), bookMapping, a.bind)

	if want := `("labels" @> jsonb_build_object($2::text, $3::text))`; got != want {
		t.Errorf("Where() SQL = %s, want %s", got, want)
	}

	if diff := cmp.Diff(args{"tenant-a", "env", "prod"}, a); diff != "" {
		t.Errorf("Where() args mismatch (-want +got):\n%s", diff)
	}
}

func TestWhere_searchWithoutSearchFields(t *testing.T) {
	t.Parallel()

	mapping := fieldmapping.New(func(n *noSearch) []fieldmapping.Entry {
		return []fieldmapping.Entry{fieldmapping.Column(&n.Title, "title")}
	})

	// NULL, not FALSE, so that a negated term matches nothing too.
	for filter, want := range map[string]string{
		"Tolstoy":  "NULL::boolean",
		"-Tolstoy": "NOT (NULL::boolean)",
	} {
		var a args

		checked := compile(t, filter, aipfiltering.SchemaFromTags(noSearch{}))
		if got := filtering.Where(checked, mapping, a.bind); got != want || a != nil {
			t.Errorf("Where(%q) = %s, %v, want %s, no arguments", filter, got, a, want)
		}
	}
}

func TestWhere_nilChecked(t *testing.T) {
	t.Parallel()

	var a args

	if got := filtering.Where(nil, bookMapping, a.bind); got != "TRUE" || a != nil {
		t.Errorf("Where(nil) = %s, %v, want TRUE, no arguments", got, a)
	}
}

func TestWhere_functions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		filter   string
		wantSQL  string
		wantArgs args
	}{
		{
			name:     "condition",
			filter:   `hasPrefix(display_name, "war")`,
			wantSQL:  `starts_with("display_name", $1) = $2`,
			wantArgs: args{"war", true},
		},
		{
			name:     "comparison",
			filter:   `lower(display_name) = "war games"`,
			wantSQL:  `lower("display_name") = $1`,
			wantArgs: args{"war games"},
		},
		{
			name:     "comparison with a wildcard",
			filter:   `lower(display_name) = "war*"`,
			wantSQL:  `lower("display_name") LIKE $1`,
			wantArgs: args{"war%"},
		},
		{
			name:     "nested call",
			filter:   `hasPrefix(lower(display_name), "war")`,
			wantSQL:  `starts_with(lower("display_name"), $1) = $2`,
			wantArgs: args{"war", true},
		},
		{
			name:     "resource name argument",
			filter:   `hasPrefix(name, "books/b")`,
			wantSQL:  `starts_with(('books/' || "book_id"), $1) = $2`,
			wantArgs: args{"books/b", true},
		},
		{
			name:     "map key argument",
			filter:   `hasPrefix(labels.env, "p")`,
			wantSQL:  `starts_with(("labels" ->> $1), $2) = $3`,
			wantArgs: args{"env", "p", true},
		},
		{
			name:     "literal with a star",
			filter:   `hasPrefix(display_name, "a*b")`,
			wantSQL:  `starts_with("display_name", $1) = $2`,
			wantArgs: args{"a*b", true},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got args

			gotSQL := filtering.Where(
				compile(t, tt.filter, bookSchema),
				bookMapping,
				got.bind,
				bookFuncs...)
			if gotSQL != tt.wantSQL {
				t.Errorf("Where(%q) SQL = %s, want %s", tt.filter, gotSQL, tt.wantSQL)
			}

			if diff := cmp.Diff(tt.wantArgs, got); diff != "" {
				t.Errorf("Where(%q) args mismatch (-want +got):\n%s", tt.filter, diff)
			}
		})
	}
}

func TestWhere_panics(t *testing.T) {
	t.Parallel()

	otherMapping := fieldmapping.New(func(n *noSearch) []fieldmapping.Entry {
		return []fieldmapping.Entry{fieldmapping.Column(&n.Title, "title")}
	})

	for _, tt := range []struct {
		name    string
		filter  string
		mapping *fieldmapping.Mapping
	}{
		{
			name:    "function without SQL",
			filter:  `hasPrefix(display_name, "war")`,
			mapping: bookMapping,
		},
		{
			name:    "field without a column",
			filter:  `display_name = "war"`,
			mapping: otherMapping,
		},
		{
			name:    "map without a column",
			filter:  `labels.env = "prod"`,
			mapping: otherMapping,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checked := compile(t, tt.filter, bookSchema)

			defer func() {
				if recover() == nil {
					t.Error("Where() did not panic")
				}
			}()

			var a args

			filtering.Where(checked, tt.mapping, a.bind)
		})
	}
}

// TestWhere_execution runs filters against PostgreSQL and asserts which
// fixture rows match, proving that the SQL means what the filter means,
// not just that it looks right.
func TestWhere_execution(t *testing.T) {
	t.Parallel()

	db := seedBooks(t)
	all := []string{"b1", "b2", "b3", "b4", "b5"}

	for _, tt := range []struct {
		name    string
		filter  string
		wantIDs []string
	}{
		{name: "empty filter matches everything", filter: "", wantIDs: all},
		{name: "wildcard is case-sensitive", filter: `display_name = "war*"`, wantIDs: []string{"b1"}},
		{name: "LIKE metacharacters match literally", filter: `display_name = "*_*"`, wantIDs: []string{"b4"}},
		{name: "negated wildcard", filter: `display_name != "war*"`, wantIDs: []string{"b2", "b3", "b4", "b5"}},
		{name: "resource name", filter: `name = "books/b2"`, wantIDs: []string{"b2"}},
		{name: "resource name wildcard", filter: `name = "books/b*"`, wantIDs: all},
		{name: "ID is not a resource name", filter: `name = "b2"`, wantIDs: nil},
		{name: "uuid", filter: `uid = "00000000-0000-4000-8000-000000000003"`, wantIDs: []string{"b3"}},
		{name: "malformed uuid", filter: `uid = "not-a-uuid"`, wantIDs: nil},
		{name: "uppercase uuid", filter: `uid = "00000000-0000-4000-8000-00000000000A"`, wantIDs: nil},
		{name: "not uuid", filter: `NOT uid = "00000000-0000-4000-8000-000000000003"`, wantIDs: []string{"b1", "b2", "b4", "b5"}},
		{name: "not resource name", filter: `name != "books/b2"`, wantIDs: []string{"b1", "b3", "b4", "b5"}},
		{name: "resource name of another pattern", filter: `name = "shelves/b2"`, wantIDs: nil},
		{name: "uuid wildcard", filter: `uid = "*0003"`, wantIDs: []string{"b3"}},
		{name: "unset float never matches less", filter: `rating < 3`, wantIDs: []string{"b4", "b5"}},
		{name: "unset float never matches not equal", filter: `rating != 2`, wantIDs: []string{"b1", "b2", "b5"}},
		{name: "null timestamp", filter: `create_time = null`, wantIDs: []string{"b3"}},
		{name: "not null timestamp", filter: `create_time != null`, wantIDs: []string{"b1", "b2", "b4", "b5"}},
		{
			name:    "timestamp comparison",
			filter:  `create_time > "2020-12-31T00:00:00Z"`,
			wantIDs: []string{"b2", "b4"},
		},
		{name: "int comparison", filter: `page_count >= 656`, wantIDs: []string{"b1", "b2"}},
		{name: "int above the column range", filter: `page_count = 3000000000`, wantIDs: nil},
		{name: "int below the column range", filter: `page_count > -3000000000`, wantIDs: all},
		{name: "duration comparison", filter: `read_time >= 5400s`, wantIDs: []string{"b1", "b2"}},
		{name: "boolean", filter: `published = true`, wantIDs: []string{"b1", "b2"}},
		{name: "negated boolean", filter: `-published = true`, wantIDs: []string{"b3", "b4", "b5"}},
		{name: "presence", filter: `author_name:*`, wantIDs: []string{"b1", "b2", "b5"}},
		{name: "map key presence", filter: `labels:env`, wantIDs: []string{"b1", "b2"}},
		{name: "map key presence with star", filter: `labels.tier:*`, wantIDs: []string{"b5"}},
		{name: "map value", filter: `labels.env = "prod"`, wantIDs: []string{"b1"}},
		{name: "map value has", filter: `labels.tier:gold`, wantIDs: []string{"b5"}},
		{name: "map value wildcard", filter: `labels.env = "p*"`, wantIDs: []string{"b1"}},
		{
			name:    "missing map key never matches not equal",
			filter:  `labels.env != "prod"`,
			wantIDs: []string{"b2"},
		},
		{
			name:    "missing map key never matches under not",
			filter:  `NOT labels.env = "prod"`,
			wantIDs: []string{"b2"},
		},
		{name: "map not empty", filter: `labels:*`, wantIDs: []string{"b1", "b2", "b5"}},
		{name: "search ignores case", filter: `war`, wantIDs: []string{"b1", "b5"}},
		{name: "search terms all match", filter: `WAR peace`, wantIDs: []string{"b1"}},
		{name: "search with wildcard", filter: `clan*y`, wantIDs: []string{"b2"}},
		{name: "search on the author", filter: `unknown`, wantIDs: []string{"b5"}},
		{name: "search on the resource name", filter: `books/b3`, wantIDs: []string{"b3"}},
		{name: "function", filter: `hasPrefix(display_name, "war")`, wantIDs: []string{"b1"}},
		{name: "nested function", filter: `hasPrefix(lower(display_name), "war")`, wantIDs: []string{"b1", "b5"}},
		{name: "function comparison", filter: `lower(display_name) = "war games"`, wantIDs: []string{"b5"}},
		{name: "function on a map key", filter: `hasPrefix(labels.env, "p")`, wantIDs: []string{"b1"}},
		{
			name:    "function on a NULL field never matches under not",
			filter:  `NOT hasPrefix(author_name, "T")`,
			wantIDs: []string{"b2", "b5"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var a args

			where := filtering.Where(
				compile(t, tt.filter, bookSchema),
				bookMapping,
				a.bind,
				bookFuncs...)
			query := `SELECT book_id FROM books WHERE ` + where + ` ORDER BY book_id`

			got := queryIDs(t, db, query, a...)
			if diff := cmp.Diff(tt.wantIDs, got); diff != "" {
				t.Errorf(
					"filter %q matched IDs mismatch (-want +got):\n%s\nSQL: %s",
					tt.filter,
					diff,
					where,
				)
			}
		})
	}
}

// bookCopy is a resource with a parent in its name.
type bookCopy struct {
	Name string `aip:"name,filter,search"`
}

// TestWhere_parentInName runs filters on a resource name that has the ID
// of the parent in a joined table.
func TestWhere_parentInName(t *testing.T) {
	t.Parallel()

	db := pgtest.DB(t)

	const create = `
		CREATE TABLE shelves (shelf_pk bigint PRIMARY KEY, shelf_id text);
		CREATE TABLE copies (copy_id text NOT NULL, shelf_pk bigint NOT NULL REFERENCES shelves)`
	if _, err := db.ExecContext(t.Context(), create); err != nil {
		t.Fatalf("creating tables: %v", err)
	}

	t.Cleanup(func() {
		// t.Context is canceled before cleanup runs.
		if _, err := db.ExecContext(
			context.Background(),
			`DROP TABLE copies, shelves`,
		); err != nil {
			t.Errorf("dropping tables: %v", err)
		}
	})

	const insert = `
		INSERT INTO shelves VALUES (1, 'fiction'), (2, 'history'), (3, NULL), (4, 'a/copies');
		INSERT INTO copies VALUES ('c1', 1), ('c2', 1), ('c1', 2), ('c3', 3), ('b', 4)`
	if _, err := db.ExecContext(t.Context(), insert); err != nil {
		t.Fatalf("seeding rows: %v", err)
	}

	schema := aipfiltering.SchemaFromTags(bookCopy{})
	mapping := fieldmapping.New(func(c *bookCopy) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(
				&c.Name,
				"shelves/{shelf}/copies/{copy}",
				"s.shelf_id",
				"c.copy_id",
			),
		}
	})

	for _, tt := range []struct {
		filter    string
		wantNames []string
	}{
		{filter: `name = "shelves/fiction/copies/c1"`, wantNames: []string{"shelves/fiction/copies/c1"}},
		{
			filter:    `name = "shelves/*/copies/c1"`,
			wantNames: []string{"shelves/fiction/copies/c1", "shelves/history/copies/c1"},
		},
		{
			filter:    `name = "shelves/fiction/*"`,
			wantNames: []string{"shelves/fiction/copies/c1", "shelves/fiction/copies/c2"},
		},
		{filter: `story/copies`, wantNames: []string{"shelves/history/copies/c1"}},
		// Equality compares each ID column, and an ID must not contain "/".
		{filter: `name = "shelves/a/copies/copies/b"`, wantNames: nil},
		{filter: `name = "shelves/a/copies"`, wantNames: nil},
		{
			// The copy of the NULL shelf has no name, so it never matches.
			filter: `NOT name = "shelves/fiction/copies/c1"`,
			wantNames: []string{
				"shelves/a/copies/copies/b",
				"shelves/fiction/copies/c2",
				"shelves/history/copies/c1",
			},
		},
		{
			filter: `name != "shelves/fiction/copies/c3"`,
			wantNames: []string{
				"shelves/a/copies/copies/b",
				"shelves/fiction/copies/c1",
				"shelves/fiction/copies/c2",
				"shelves/history/copies/c1",
			},
		},
	} {
		t.Run(tt.filter, func(t *testing.T) {
			t.Parallel()

			var a args

			where := filtering.Where(compile(t, tt.filter, schema), mapping, a.bind)
			query := `SELECT 'shelves/' || s.shelf_id || '/copies/' || c.copy_id AS name
				FROM copies c JOIN shelves s USING (shelf_pk)
				WHERE ` + where + ` ORDER BY name`

			got := queryIDs(t, db, query, a...)
			if diff := cmp.Diff(tt.wantNames, got); diff != "" {
				t.Errorf("filter %q matched names mismatch (-want +got):\n%s\nSQL: %s",
					tt.filter, diff, where)
			}
		})
	}
}

// seedBooks creates the books table with fixture rows, and drops it when t
// ends. NULLs mark unset fields, matching how a service stores absent
// optional data.
func seedBooks(t *testing.T) *sql.DB {
	t.Helper()

	db := pgtest.DB(t)

	// page_count is an integer column for an int64 field, so that a filter
	// value can be out of the range of the column.
	const schema = `
		CREATE TABLE books (
			book_id      text             PRIMARY KEY,
			uid          uuid             NOT NULL,
			display_name text             NOT NULL,
			page_count   integer          NOT NULL,
			rating       double precision,
			published    boolean          NOT NULL,
			create_time  timestamptz,
			read_time    interval,
			author_name  text,
			labels       jsonb
		)`
	if _, err := db.ExecContext(t.Context(), schema); err != nil {
		t.Fatalf("creating table: %v", err)
	}

	t.Cleanup(func() {
		// t.Context is canceled before cleanup runs.
		if _, err := db.ExecContext(context.Background(), `DROP TABLE books`); err != nil {
			t.Errorf("dropping table: %v", err)
		}
	})

	//nolint:dupword // Rows with unset fields repeat NULL.
	const insert = `INSERT INTO books VALUES
		('b1', '00000000-0000-4000-8000-000000000001', 'war and peace', 1225, 4.5, true,
			'2020-06-01T00:00:00Z', '7200 seconds', 'Tolstoy', '{"env": "prod"}'),
		('b2', '00000000-0000-4000-8000-000000000002', 'hunt for red october', 656, 3.9, true,
			'2021-03-01T00:00:00Z', '5400 seconds', 'Clancy', '{"env": "dev"}'),
		('b3', '00000000-0000-4000-8000-000000000003', 'readme.md', 1, NULL, false,
			NULL, NULL, NULL, NULL),
		('b4', '00000000-0000-4000-8000-000000000004', '50% off_sale', 10, 2.0, false,
			'2022-01-01T00:00:00Z', '60 seconds', NULL, '{}'),
		('b5', '00000000-0000-4000-8000-000000000005', 'War Games', 5, 1.0, false,
			'2019-01-01T00:00:00Z', '30 seconds', 'Unknown', '{"tier": "gold"}')`
	if _, err := db.ExecContext(t.Context(), insert); err != nil {
		t.Fatalf("seeding rows: %v", err)
	}

	return db
}

// queryIDs runs query, which selects one text column, and returns its
// values.
func queryIDs(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatalf("query %s: %v", query, err)
	}

	defer func() { _ = rows.Close() }()

	var ids []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	return ids
}
