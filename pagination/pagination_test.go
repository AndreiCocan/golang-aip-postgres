package pagination_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"
	aippagination "github.com/AndreiCocan/golang-aip/pagination"
	"github.com/AndreiCocan/golang-aip/resourcename"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
	"github.com/AndreiCocan/golang-aip-postgres/internal/pgtest"
	"github.com/AndreiCocan/golang-aip-postgres/ordering"
	"github.com/AndreiCocan/golang-aip-postgres/pagination"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

// bookPattern is the resource name pattern of a book, which has its shelf
// in its name.
const bookPattern = "shelves/{shelf}/books/{book}"

// book is the domain type of the tests.
type book struct {
	Name        string    `aip:"name,order"`
	UID         string    `aip:"uid,order"`
	Title       string    `aip:"title,order"`
	Pages       int64     `aip:"pages,order"`
	PublishTime time.Time `aip:"publish_time,order"`
	// Summary has no order option: the order_by of a request cannot name
	// it, but the server can sort by it.
	Summary string `aip:"summary,filter"`
	// Tags is a jsonb map, which has no order, so it is not a sort key.
	Tags map[string]string `aip:"tags,filter"`
}

var (
	bookSchema  = aipordering.SchemaFromTags(book{})
	bookMapping = fieldmapping.New(func(b *book) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&b.Name, bookPattern, "shelf_id", "book_id"),
			fieldmapping.UUIDColumn(&b.UID, "uid"),
			fieldmapping.Column(&b.Title, "title"),
			fieldmapping.Column(&b.Pages, "pages"),
			fieldmapping.Column(&b.PublishTime, "publish_time"),
			fieldmapping.Column(&b.Summary, "summary"),
			fieldmapping.JSONBColumn(&b.Tags, "tags"),
		}
	})
)

// compile returns the checked form of orderBy, and fails t when orderBy is
// not valid.
func compile(t *testing.T, orderBy string) *aipordering.CheckedOrderBy {
	t.Helper()

	checked, err := aipordering.Compile(orderBy, bookSchema)
	if err != nil {
		t.Fatalf("Compile(%q) error = %v", orderBy, err)
	}

	return checked
}

// binder returns a bind function and the arguments that it collects.
func binder() (func(any) string, *[]any) {
	args := &[]any{}

	return func(v any) string {
		*args = append(*args, v)

		return "$" + strconv.Itoa(len(*args))
	}, args
}

func TestCursorOf(t *testing.T) {
	t.Parallel()

	published := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	row := &book{
		Name:        "shelves/s1/books/b1",
		UID:         "00000000-0000-4000-8000-000000000001",
		Title:       "War",
		Pages:       1225,
		PublishTime: published,
		Summary:     "A long book.",
	}

	got := pagination.CursorOf(compile(t, "title desc, publish_time, name"), bookMapping, row)

	want := pagination.Cursor[book]{Row: book{
		Name:        "shelves/s1/books/b1",
		Title:       "War",
		PublishTime: published,
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CursorOf() mismatch (-want +got):\n%s", diff)
	}
}

func TestAfter(t *testing.T) {
	t.Parallel()

	published := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	row := &book{Name: "shelves/s1/books/b1", Title: "War", PublishTime: published}

	tests := []struct {
		name     string
		orderBy  string
		row      *book
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "name with a parent",
			orderBy:  "name",
			row:      row,
			wantSQL:  `(((` + `"shelf_id" > $1 OR "shelf_id" IS NULL)) OR ("shelf_id" = $1 AND ("book_id" > $2 OR "book_id" IS NULL)))`,
			wantArgs: []any{"s1", "b1"},
		},
		{
			name:    "descending key, then name",
			orderBy: "title desc, name",
			row:     row,
			wantSQL: `(("title" < $1) OR ("title" = $1 AND ("shelf_id" > $2 OR "shelf_id" IS NULL)) OR ` +
				`("title" = $1 AND "shelf_id" = $2 AND ("book_id" > $3 OR "book_id" IS NULL)))`,
			wantArgs: []any{"War", "s1", "b1"},
		},
		{
			name:    "set time in ascending order",
			orderBy: "publish_time, name desc",
			row:     row,
			wantSQL: `((("publish_time" > $1 OR "publish_time" IS NULL)) OR ` +
				`("publish_time" = $1 AND "shelf_id" < $2) OR ` +
				`("publish_time" = $1 AND "shelf_id" = $2 AND "book_id" < $3))`,
			wantArgs: []any{published, "s1", "b1"},
		},
		{
			name:    "unset time in ascending order",
			orderBy: "publish_time, name",
			row:     &book{Name: "shelves/s1/books/b1"},
			wantSQL: `((FALSE) OR ("publish_time" IS NULL AND ("shelf_id" > $1 OR "shelf_id" IS NULL)) OR ` +
				`("publish_time" IS NULL AND "shelf_id" = $1 AND ("book_id" > $2 OR "book_id" IS NULL)))`,
			wantArgs: []any{"s1", "b1"},
		},
		{
			name:    "unset time in descending order",
			orderBy: "publish_time desc, name",
			row:     &book{Name: "shelves/s1/books/b1"},
			wantSQL: `(("publish_time" IS NOT NULL) OR ("publish_time" IS NULL AND ("shelf_id" > $1 OR "shelf_id" IS NULL)) OR ` +
				`("publish_time" IS NULL AND "shelf_id" = $1 AND ("book_id" > $2 OR "book_id" IS NULL)))`,
			wantArgs: []any{"s1", "b1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			order := compile(t, tt.orderBy)
			bind, args := binder()

			got, err := pagination.After(
				order,
				bookMapping,
				pagination.CursorOf(order, bookMapping, tt.row),
				bind,
			)
			if err != nil {
				t.Fatalf("After() error = %v", err)
			}

			if got != tt.wantSQL {
				t.Errorf("After() =\n%s\nwant\n%s", got, tt.wantSQL)
			}

			if diff := cmp.Diff(tt.wantArgs, *args); diff != "" {
				t.Errorf("After() args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAfterInvalidCursor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		orderBy string
		cursor  pagination.Cursor[book]
	}{
		{"zero cursor", "name", pagination.Cursor[book]{}},
		{
			"name of another pattern",
			"name",
			pagination.Cursor[book]{Row: book{Name: "books/b1"}},
		},
		{
			"name with a service name",
			"title, name",
			pagination.Cursor[book]{Row: book{Name: "//library.example.com/shelves/s1/books/b1"}},
		},
		{
			"uuid that is not canonical",
			"uid, name",
			pagination.Cursor[book]{Row: book{Name: "shelves/s1/books/b1", UID: "not-a-uuid"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bind, args := binder()

			_, err := pagination.After(compile(t, tt.orderBy), bookMapping, tt.cursor, bind)
			if !errors.Is(err, aippagination.ErrInvalidPageToken) {
				t.Errorf("After() error = %v, want %v", err, aippagination.ErrInvalidPageToken)
			}

			if len(*args) > 0 {
				t.Errorf("After() bound %v, want no arguments", *args)
			}
		})
	}
}

// serverOrder returns an order that the server sets, by path ascending. It
// does not go through the ordering schema of a book, so path can be any
// field.
func serverOrder(path string) *aipordering.CheckedOrderBy {
	return &aipordering.CheckedOrderBy{Keys: []aipordering.Key{{Segments: []string{path}}}}
}

func TestAfterServerOrderWithoutOrderOption(t *testing.T) {
	t.Parallel()

	bind, args := binder()

	row := book{Name: "shelves/s1/books/b1", Summary: "A war novel"}
	order := serverOrder("summary")

	got, err := pagination.After(
		order,
		bookMapping,
		pagination.CursorOf(order, bookMapping, &row),
		bind,
	)
	if err != nil {
		t.Fatalf("After() error = %v", err)
	}

	if want := `((("summary" > $1 OR "summary" IS NULL)))`; got != want {
		t.Errorf("After() = %q, want %q", got, want)
	}

	if diff := cmp.Diff([]any{"A war novel"}, *args); diff != "" {
		t.Errorf("After() arguments mismatch (-want +got):\n%s", diff)
	}
}

func TestAfterUnsortableField(t *testing.T) {
	t.Parallel()

	bind, args := binder()

	_, err := pagination.After(
		serverOrder("tags"),
		bookMapping,
		pagination.Cursor[book]{},
		bind,
	)
	if !errors.Is(err, aippagination.ErrInvalidPageToken) {
		t.Errorf("After() error = %v, want %v", err, aippagination.ErrInvalidPageToken)
	}

	if len(*args) > 0 {
		t.Errorf("After() bound %v, want no arguments", *args)
	}
}

func TestPanics(t *testing.T) {
	t.Parallel()

	unsortable := serverOrder("tags")
	unknown := serverOrder("isbn")

	row := &book{Name: "shelves/s1/books/b1"}
	bind, _ := binder()

	tests := []struct {
		name string
		call func()
	}{
		{"CursorOf with a key that is not a sort field", func() {
			pagination.CursorOf(unsortable, bookMapping, row)
		}},
		{"CursorOf with a name that does not match its pattern", func() {
			pagination.CursorOf(compile(t, "name"), bookMapping, &book{Name: "books/b1"})
		}},
		{"After without keys", func() {
			_, _ = pagination.After(compile(t, ""), bookMapping, pagination.Cursor[book]{}, bind)
		}},
		{"After with a key that is not a field", func() {
			_, _ = pagination.After(unknown, bookMapping, pagination.Cursor[book]{}, bind)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("call did not panic")
				}
			}()

			tt.call()
		})
	}
}

// TestPages pages through a table with each sort and page size, and checks
// that the pages give the rows of one ORDER BY query, in the same order.
// Each cursor goes through a page token, as between two requests.
func TestPages(t *testing.T) {
	t.Parallel()

	db := pgtest.DB(t)
	createBooks(t, db)

	for _, orderBy := range []string{
		"name",
		"name desc",
		"title, name",
		"title desc, name",
		"pages desc, title, name",
		"publish_time, name",
		"publish_time desc, name",
		"publish_time desc, title desc, name desc",
	} {
		order := compile(t, orderBy)
		want := listBooks(t, db, order, "TRUE", nil, 100)

		for size := 1; size <= 4; size++ {
			t.Run(fmt.Sprintf("%s by %d", orderBy, size), func(t *testing.T) {
				t.Parallel()

				if diff := cmp.Diff(want, pageThrough(t, db, order, size)); diff != "" {
					t.Errorf("pages mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// pageThrough returns the names of the books of all pages of size rows in
// the sort of order.
func pageThrough(t *testing.T, db *sql.DB, order *aipordering.CheckedOrderBy, size int) []string {
	t.Helper()

	token, err := aippagination.ParseToken("")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	var names []string

	for range 100 {
		var after pagination.Cursor[book]

		seek := "TRUE"

		var args []any

		hasCursor, err := token.Cursor(&after)
		if err != nil {
			t.Fatalf("Cursor() error = %v", err)
		}

		if hasCursor {
			bind := func(v any) string {
				args = append(args, v)

				return "$" + strconv.Itoa(len(args))
			}

			if seek, err = pagination.After(order, bookMapping, after, bind); err != nil {
				t.Fatalf("After() error = %v", err)
			}
		}

		page := listBookRows(t, db, order, seek, args, size)
		for _, b := range page {
			names = append(names, b.Name)
		}

		if len(page) < size {
			return names
		}

		next, err := token.Next(pagination.CursorOf(order, bookMapping, &page[len(page)-1]))
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}

		if token, err = aippagination.ParseToken(next); err != nil {
			t.Fatalf("Parse(next) error = %v", err)
		}
	}

	t.Fatal("the pages do not end")

	return nil
}

// listBooks returns the names of at most limit books that match where, in
// the sort of order.
func listBooks(
	t *testing.T,
	db *sql.DB,
	order *aipordering.CheckedOrderBy,
	where string,
	args []any,
	limit int,
) []string {
	t.Helper()

	books := listBookRows(t, db, order, where, args, limit)

	names := make([]string, 0, len(books))
	for _, b := range books {
		names = append(names, b.Name)
	}

	return names
}

// listBookRows returns at most limit books that match where, in the sort of
// order.
func listBookRows(
	t *testing.T,
	db *sql.DB,
	order *aipordering.CheckedOrderBy,
	where string,
	args []any,
	limit int,
) []book {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), `
		SELECT shelf_id, book_id, uid, title, pages, publish_time
		FROM books
		WHERE `+where+`
		ORDER BY `+ordering.OrderBy(order, bookMapping)+`
		LIMIT `+strconv.Itoa(limit),
		args...,
	)
	if err != nil {
		t.Fatalf("select books: %v", err)
	}

	defer func() { _ = rows.Close() }()

	var books []book

	for rows.Next() {
		var (
			b           book
			shelf, id   string
			publishTime sql.NullTime
		)

		if err := rows.Scan(&shelf, &id, &b.UID, &b.Title, &b.Pages, &publishTime); err != nil {
			t.Fatalf("scan book: %v", err)
		}

		b.Name = resourcename.Sprint(bookPattern, shelf, id)
		b.PublishTime = publishTime.Time
		books = append(books, b)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read books: %v", err)
	}

	return books
}

// createBooks creates the books table with rows that have equal titles,
// pages, and publish times, unset publish times, and three shelves.
func createBooks(t *testing.T, db *sql.DB) {
	t.Helper()

	const create = `
		CREATE TABLE books (
			shelf_id text NOT NULL,
			book_id text NOT NULL,
			uid uuid NOT NULL DEFAULT gen_random_uuid(),
			title text NOT NULL,
			pages bigint NOT NULL,
			publish_time timestamptz,
			PRIMARY KEY (shelf_id, book_id)
		)`
	if _, err := db.ExecContext(t.Context(), create); err != nil {
		t.Fatalf("create table: %v", err)
	}

	t.Cleanup(func() {
		// t.Context is canceled before cleanup runs.
		if _, err := db.ExecContext(context.Background(), `DROP TABLE books`); err != nil {
			t.Errorf("drop table: %v", err)
		}
	})

	const insert = `
		INSERT INTO books (shelf_id, book_id, title, pages, publish_time) VALUES
			('s1', 'b1', 'War', 100, '2026-01-01T00:00:00Z'),
			('s1', 'b2', 'War', 100, NULL),
			('s1', 'b3', 'Peace', 300, '2026-01-01T00:00:00Z'),
			('s2', 'b1', 'Peace', 100, NULL),
			('s2', 'b2', 'Anna', 200, '2026-03-01T00:00:00Z'),
			('s2', 'b3', 'War', 300, '2026-01-01T00:00:00.000001Z'),
			('s3', 'b1', 'Anna', 200, NULL),
			('s3', 'b2', 'Zola', 100, '2025-12-31T23:59:59Z'),
			('s3', 'b3', 'Anna', 300, '2026-03-01T00:00:00Z')`
	if _, err := db.ExecContext(t.Context(), insert); err != nil {
		t.Fatalf("insert books: %v", err)
	}
}
