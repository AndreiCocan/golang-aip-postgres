package ordering_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/go-cmp/cmp"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
	"github.com/AndreiCocan/golang-aip-postgres/internal/pgtest"
	"github.com/AndreiCocan/golang-aip-postgres/ordering"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

// row is the domain type of the tests.
type row struct {
	Name string `aip:"name,order"`
	A    int64  `aip:"a,order"`
	B    string `aip:"b,order"`
	Note string `aip:"note,order"`
}

var (
	rowSchema  = aipordering.SchemaFromTags(row{})
	rowMapping = fieldmapping.New(func(r *row) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&r.Name, "rows/{row}", "id"),
			fieldmapping.Column(&r.A, "a"),
			fieldmapping.Column(&r.B, "b"),
			fieldmapping.Column(&r.Note, "note_text"),
		}
	})
)

// compile returns the checked form of orderBy, and fails t when orderBy is
// not valid.
func compile(t *testing.T, orderBy string) *aipordering.CheckedOrderBy {
	t.Helper()

	checked, err := aipordering.Compile(orderBy, rowSchema)
	if err != nil {
		t.Fatalf("Compile(%q) error = %v", orderBy, err)
	}

	return checked
}

func TestOrderBy(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		orderBy string
		want    string
	}{
		{name: "empty", orderBy: "", want: ""},
		{name: "ascending and descending", orderBy: "a, b desc", want: `"a", "b" DESC`},
		{name: "renamed column", orderBy: "note desc", want: `"note_text" DESC`},
		{name: "resource name sorts by the column", orderBy: "name", want: `"id"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ordering.OrderBy(compile(t, tt.orderBy), rowMapping); got != tt.want {
				t.Errorf("OrderBy(%q) = %s, want %s", tt.orderBy, got, tt.want)
			}
		})
	}
}

func TestOrderBy_parentInName(t *testing.T) {
	t.Parallel()

	type bookCopy struct {
		Name string `aip:"name,order"`
	}

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

	checked, err := aipordering.Compile("name desc", aipordering.SchemaFromTags(bookCopy{}))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	want := `"s"."shelf_id" DESC, "c"."copy_id" DESC`
	if got := ordering.OrderBy(checked, mapping); got != want {
		t.Errorf("OrderBy() = %s, want %s", got, want)
	}
}

func TestOrderBy_nilChecked(t *testing.T) {
	t.Parallel()

	if got := ordering.OrderBy(nil, rowMapping); got != "" {
		t.Errorf("OrderBy(nil) = %q, want empty", got)
	}
}

func TestOrderBy_fieldWithoutColumn(t *testing.T) {
	t.Parallel()

	type other struct {
		Title string `aip:"title,order"`
	}

	mapping := fieldmapping.New(func(o *other) []fieldmapping.Entry {
		return []fieldmapping.Entry{fieldmapping.Column(&o.Title, "title")}
	})
	checked := compile(t, "a")

	defer func() {
		if recover() == nil {
			t.Error("OrderBy() did not panic")
		}
	}()

	ordering.OrderBy(checked, mapping)
}

// TestOrderBy_execution runs order_bys against PostgreSQL and asserts the
// order of the fixture rows, including the place of NULLs.
func TestOrderBy_execution(t *testing.T) {
	t.Parallel()

	db := seedRows(t)

	for _, tt := range []struct {
		orderBy string
		wantIDs []string
	}{
		{orderBy: "a, name", wantIDs: []string{"r1", "r2", "r4", "r3", "r5"}},
		{orderBy: "a desc, name", wantIDs: []string{"r3", "r5", "r4", "r1", "r2"}},
		{orderBy: "b, a desc", wantIDs: []string{"r4", "r1", "r3", "r5", "r2"}},
		{orderBy: "b desc, name desc", wantIDs: []string{"r5", "r2", "r3", "r4", "r1"}},
	} {
		t.Run(tt.orderBy, func(t *testing.T) {
			t.Parallel()

			orderBy := ordering.OrderBy(compile(t, tt.orderBy), rowMapping)

			got := queryIDs(t, db, `SELECT id FROM rows ORDER BY `+orderBy)
			if diff := cmp.Diff(tt.wantIDs, got); diff != "" {
				t.Errorf(
					"order_by %q IDs mismatch (-want +got):\n%s\nSQL: %s",
					tt.orderBy,
					diff,
					orderBy,
				)
			}
		})
	}
}

// seedRows creates the rows table with fixture rows, and drops it when t
// ends. Both keys have duplicates and NULLs.
func seedRows(t *testing.T) *sql.DB {
	t.Helper()

	db := pgtest.DB(t)

	const create = `CREATE TABLE rows (
		id        text COLLATE "C" PRIMARY KEY,
		a         bigint,
		b         text COLLATE "C",
		note_text text
	)`
	if _, err := db.ExecContext(t.Context(), create); err != nil {
		t.Fatalf("creating table: %v", err)
	}

	t.Cleanup(func() {
		// t.Context is canceled before cleanup runs.
		if _, err := db.ExecContext(context.Background(), `DROP TABLE rows`); err != nil {
			t.Errorf("dropping table: %v", err)
		}
	})

	const insert = `INSERT INTO rows (id, a, b) VALUES
		('r1', 1, 'x'), ('r2', 1, NULL), ('r3', NULL, 'y'), ('r4', 2, 'x'), ('r5', NULL, NULL)`
	if _, err := db.ExecContext(t.Context(), insert); err != nil {
		t.Fatalf("seeding rows: %v", err)
	}

	return db
}

// queryIDs runs query, which selects one text column, and returns its
// values.
func queryIDs(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), query)
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
