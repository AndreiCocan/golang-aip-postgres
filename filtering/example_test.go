package filtering_test

import (
	"fmt"
	"strconv"
	"time"

	aipfiltering "github.com/AndreiCocan/golang-aip/filtering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
	"github.com/AndreiCocan/golang-aip-postgres/filtering"
)

// The usual service path. Build the filter schema and the field mapping
// once, from the same domain type. For each List request, compile the
// filter, then put the condition of Where in your query. Where adds its
// values to the arguments of the query with your bind function, so the
// condition can come after your own arguments.
func Example() {
	type Shelf struct {
		Name  string `aip:"name,filter,search"`
		Theme string `aip:"theme,filter,search"`
	}

	schema := aipfiltering.SchemaFromTags(Shelf{})
	mapping := fieldmapping.New(func(s *Shelf) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&s.Name, "shelves/{shelf}", "shelf_id"),
			fieldmapping.Column(&s.Theme, "theme"),
		}
	})

	checked, err := aipfiltering.Compile(`theme = "sci*"`, schema)
	if err != nil {
		fmt.Println(err) // Return INVALID_ARGUMENT.

		return
	}

	var args []any

	// bind adds a value to the arguments and returns its placeholder.
	bind := func(v any) string {
		args = append(args, v)

		return "$" + strconv.Itoa(len(args))
	}

	query := "SELECT shelf_id FROM shelves WHERE owner = " + bind("alice") +
		" AND " + filtering.Where(checked, mapping, bind)

	// Then: rows, err := db.QueryContext(ctx, query, args...)
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// SELECT shelf_id FROM shelves WHERE owner = $1 AND "theme" LIKE $2
	// [alice sci%]
}

// The SQL that each part of the filter syntax gives. An = test on a
// resource name, a UUID, or a map key uses the columns directly, so an
// index can find the rows. Under NOT, Where uses the full expression
// instead, so that a NULL column never matches.
func ExampleWhere() {
	type Book struct {
		Name       string            `aip:"name,filter,search"`
		UID        string            `aip:"uid,filter"`
		Title      string            `aip:"title,filter,search"`
		Pages      int64             `aip:"page_count,filter"`
		CreateTime time.Time         `aip:"create_time,filter"`
		ReadTime   time.Duration     `aip:"read_time,filter"`
		Labels     map[string]string `aip:"labels,filter"`
	}

	schema := aipfiltering.SchemaFromTags(Book{})
	mapping := fieldmapping.New(func(b *Book) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&b.Name, "books/{book}", "book_id"),
			fieldmapping.UUIDColumn(&b.UID, "uid"),
			fieldmapping.Column(&b.Title, "title"),
			fieldmapping.Column(&b.Pages, "page_count"),
			fieldmapping.Column(&b.CreateTime, "create_time"),
			fieldmapping.Column(&b.ReadTime, "read_time"),
			fieldmapping.JSONBColumn(&b.Labels, "labels"),
		}
	})

	for _, filter := range []string{
		`name = "books/b1"`,
		`NOT name = "books/b1"`,
		`uid = "8a3f1c2e-0000-4000-8000-000000000001"`,
		`title = "War*" OR page_count >= 1000`,
		`create_time = null`,
		`read_time > 1.5s`,
		`labels.env = prod`,
		`labels:env`,
		`Tolstoy`,
	} {
		checked, err := aipfiltering.Compile(filter, schema)
		if err != nil {
			fmt.Println(err)

			continue
		}

		var args []any

		bind := func(v any) string {
			args = append(args, v)

			return "$" + strconv.Itoa(len(args))
		}

		fmt.Println(filtering.Where(checked, mapping, bind), args)
	}
	// Output:
	// "book_id" = $1 [b1]
	// NOT (('books/' || "book_id") = $1) [books/b1]
	// "uid" = $1::uuid [8a3f1c2e-0000-4000-8000-000000000001]
	// ("title" LIKE $1 OR "page_count" >= $2::bigint) [War% 1000]
	// "create_time" IS NULL []
	// "read_time" > make_interval(secs => $1) [1.5]
	// ("labels" @> jsonb_build_object($1::text, $2::text)) [env prod]
	// ("labels" ? $1) [env]
	// (('books/' || "book_id") ILIKE $1 OR "title" ILIKE $1) [%Tolstoy%]
}

// A filter function without an expander needs the SQL of its call. Give
// it to Where as an SQLFunc. Here hasPrefix becomes the PostgreSQL
// function starts_with.
func ExampleSQLFunc() {
	type Book struct {
		Name  string `aip:"name,filter"`
		Title string `aip:"title,filter"`
	}

	schema := aipfiltering.SchemaFromTags(Book{},
		aipfiltering.Func("hasPrefix",
			aipfiltering.FuncArgs(aipfiltering.KindString, aipfiltering.KindString),
			aipfiltering.FuncReturns(aipfiltering.KindBool),
		),
	)
	mapping := fieldmapping.New(func(b *Book) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&b.Name, "books/{book}", "book_id"),
			fieldmapping.Column(&b.Title, "title"),
		}
	})

	// args holds the SQL of each argument: a column, or a placeholder.
	hasPrefix := filtering.SQLFunc{Name: "hasPrefix", SQL: func(args []string) string {
		return "starts_with(" + args[0] + ", " + args[1] + ")"
	}}

	checked, err := aipfiltering.Compile(`hasPrefix(title, "War")`, schema)
	if err != nil {
		fmt.Println(err)

		return
	}

	var args []any

	bind := func(v any) string {
		args = append(args, v)

		return "$" + strconv.Itoa(len(args))
	}

	fmt.Println(filtering.Where(checked, mapping, bind, hasPrefix))
	fmt.Println(args)
	// Output:
	// starts_with("title", $1) = $2
	// [War true]
}
