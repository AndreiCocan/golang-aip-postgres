package fieldmapping_test

import (
	"fmt"
	"strconv"
	"time"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
)

// Map each field with an aip tag to its column, once, next to the domain
// type. The pointers let the compiler check the fields. The filtering,
// ordering, and pagination packages of this module then read the mapping
// to write SQL.
func Example() {
	// bookPattern is the resource name pattern of a book. The resource name
	// helpers of the domain type use the same constant.
	const bookPattern = "books/{book}"

	type Book struct {
		Name       string            `aip:"name,filter,order,search"`
		UID        string            `aip:"uid,filter"`
		Title      string            `aip:"title,filter,order,search"`
		CreateTime time.Time         `aip:"create_time,filter,order"`
		Labels     map[string]string `aip:"labels,filter"`
	}

	mapping := fieldmapping.New(func(b *Book) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(
				&b.Name,
				bookPattern,
				"book_id",
			), // the column holds "123" of "books/123"
			fieldmapping.UUIDColumn(&b.UID, "uid"), // a uuid column
			fieldmapping.Column(
				&b.Title,
				"title",
			), // a column of the same type
			fieldmapping.Column(&b.CreateTime, "create_time"),
			fieldmapping.JSONBColumn(&b.Labels, "labels"), // a jsonb object
		}
	})

	// FilterExpr is what a filter compares, and Columns is what an ORDER BY
	// sorts by.
	for _, name := range []string{"name", "uid", "title", "labels"} {
		columns, _ := mapping.Columns(name)
		expr, _ := mapping.FilterExpr(name)
		fmt.Println(name, columns, expr)
	}

	// SearchExprs gives the expressions of the fields with the search option.
	fmt.Println(mapping.SearchExprs())
	// Output:
	// name ["book_id"] ('books/' || "book_id")
	// uid ["uid"] "uid"::text
	// title ["title"] "title"
	// labels ["labels"] "labels"
	// [('books/' || "book_id") "title"]
}

// When the parent of a resource is in its name, give one column for each
// variable of the pattern. A column can have the qualifier of a joined
// table.
func ExampleResourceNameColumns() {
	const copyPattern = "shelves/{shelf}/copies/{copy}"

	type Copy struct {
		Name string `aip:"name,filter,order"`
	}

	// The shelf ID is in the shelves table, which the query joins as s.
	mapping := fieldmapping.New(func(c *Copy) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&c.Name, copyPattern, "s.shelf_id", "c.copy_id"),
		}
	})

	columns, _ := mapping.Columns("name")
	expr, _ := mapping.FilterExpr("name")

	fmt.Println(columns)
	fmt.Println(expr)
	// Output:
	// ["s"."shelf_id" "c"."copy_id"]
	// ('shelves/' || "s"."shelf_id" || '/copies/' || "c"."copy_id")
}

// EqualCondition compares each column with its part of the value, so an
// index on the columns can find the rows. A value that cannot match gives
// FALSE, and PostgreSQL then reads no rows. The filtering package uses it
// for =.
func ExampleMapping_EqualCondition() {
	const copyPattern = "shelves/{shelf}/copies/{copy}"

	type Copy struct {
		Name string `aip:"name,filter"`
		UID  string `aip:"uid,filter"`
	}

	mapping := fieldmapping.New(func(c *Copy) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&c.Name, copyPattern, "s.shelf_id", "c.copy_id"),
			fieldmapping.UUIDColumn(&c.UID, "c.uid"),
		}
	})

	var args []any

	// bind adds a value to the arguments of the query.
	bind := func(v any) string {
		args = append(args, v)

		return "$" + strconv.Itoa(len(args))
	}

	for _, test := range []struct{ field, value string }{
		{"name", "shelves/fiction/copies/c1"},
		{"name", "books/b1"},
		{"uid", "8a3f1c2e-0000-4000-8000-000000000001"},
		{"uid", "not-a-uuid"},
	} {
		sql, _ := mapping.EqualCondition(test.field, test.value, bind)
		fmt.Println(sql)
	}

	fmt.Println(args)
	// Output:
	// ("s"."shelf_id" = $1 AND "c"."copy_id" = $2)
	// FALSE
	// "c"."uid" = $3::uuid
	// FALSE
	// [fiction c1 8a3f1c2e-0000-4000-8000-000000000001]
}

// SortKeyValues gives what a domain value stores in the columns of a sort
// key, in the order of Columns. A keyset cursor holds these values for the
// last row of a page.
func ExampleMapping_SortKeyValues() {
	const instancePattern = "tenants/{tenant}/instances/{instance}"

	type Instance struct {
		Name        string    `aip:"name,order"`
		DisplayName string    `aip:"display_name,order"`
		DeleteTime  time.Time `aip:"delete_time,order"`
	}

	mapping := fieldmapping.New(func(i *Instance) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(
				&i.Name,
				instancePattern,
				"t.tenant_id",
				"i.instance_id",
			),
			fieldmapping.Column(&i.DisplayName, "i.display_name"),
			fieldmapping.Column(&i.DeleteTime, "i.delete_time"),
		}
	})

	// A live instance: its delete time is zero.
	db1 := Instance{Name: "tenants/acme/instances/db1", DisplayName: "Orders"}

	for _, path := range []string{"name", "display_name", "delete_time"} {
		columns, _ := mapping.Columns(path)
		values, _ := mapping.SortKeyValues(path, &db1)
		fmt.Println(path, columns, values)
	}
	// Output:
	// name ["t"."tenant_id" "i"."instance_id"] [acme db1]
	// display_name ["i"."display_name"] [Orders]
	// delete_time ["i"."delete_time"] [<nil>]
}

// ValidSortKeyValues reports whether values have the form that
// SortKeyValues gives: one value for each column, of the right type.
func ExampleMapping_ValidSortKeyValues() {
	const tenantPattern = "tenants/{tenant}"

	type Tenant struct {
		Name       string    `aip:"name,order"`
		DeleteTime time.Time `aip:"delete_time,order"`
	}

	mapping := fieldmapping.New(func(t *Tenant) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&t.Name, tenantPattern, "tenant_id"),
			fieldmapping.Column(&t.DeleteTime, "delete_time"),
		}
	})

	for _, test := range []struct {
		path   string
		values []any
	}{
		{"name", []any{"acme"}},
		{"name", []any{"acme", "x"}}, // too many values
		{"name", []any{42}},          // not a string
		{"delete_time", []any{nil}},  // an unset time
		{"delete_time", []any{time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}},
		{"delete_time", []any{"yesterday"}}, // not a time
	} {
		fmt.Println(test.path, test.values, mapping.ValidSortKeyValues(test.path, test.values))
	}
	// Output:
	// name [acme] true
	// name [acme x] false
	// name [42] false
	// delete_time [<nil>] true
	// delete_time [2026-01-02 00:00:00 +0000 UTC] true
	// delete_time [yesterday] false
}
