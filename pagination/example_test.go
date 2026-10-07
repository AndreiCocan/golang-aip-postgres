package pagination_test

import (
	"fmt"
	"strconv"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
	"github.com/AndreiCocan/golang-aip-postgres/pagination"
)

// Make the cursor of the last row of a page, and put it in the page token.
// For the next page, read the cursor from the token, and put the condition
// of After in the query. Each column of a resource name has its own value,
// so a list across shelves resumes at the right shelf.
func Example() {
	type Book struct {
		Name  string `aip:"name,order"`
		Title string `aip:"title,order"`
	}

	schema := aipordering.SchemaFromTags(Book{})
	mapping := fieldmapping.New(func(b *Book) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(
				&b.Name,
				"shelves/{shelf}/books/{book}",
				"shelf_id",
				"book_id",
			),
			fieldmapping.Column(&b.Title, "title"),
		}
	})

	// The order ends with the name, so that each row has a unique position.
	order, err := aipordering.Compile("title desc, name", schema)
	if err != nil {
		fmt.Println(err)

		return
	}

	last := Book{Name: "shelves/fiction/books/war", Title: "War"}
	cursor := pagination.CursorOf(order, mapping, &last)

	var args []any

	bind := func(v any) string {
		args = append(args, v)

		return "$" + strconv.Itoa(len(args))
	}

	seek, err := pagination.After(order, mapping, cursor, bind)
	if err != nil {
		fmt.Println(err) // A changed cursor: return INVALID_ARGUMENT.

		return
	}

	fmt.Println(seek)
	fmt.Println(args)
	// Output:
	// (("title" < $1) OR ("title" = $1 AND ("shelf_id" > $2 OR "shelf_id" IS NULL)) OR ("title" = $1 AND "shelf_id" = $2 AND ("book_id" > $3 OR "book_id" IS NULL)))
	// [War fiction war]
}
