package ordering_test

import (
	"fmt"
	"time"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
	"github.com/AndreiCocan/golang-aip-postgres/ordering"
)

// The usual service path. Build the order_by schema and the field mapping
// once, from the same domain type. For each List request, compile the
// order_by, then put the list of OrderBy after ORDER BY. A resource name
// with a parent sorts by one column for each part of the name. To add a
// default order and a tie-breaker, see [ServerOrderRules].
func Example() {
	const copyPattern = "shelves/{shelf}/copies/{copy}"

	type Copy struct {
		Name       string    `aip:"name,order"`
		CreateTime time.Time `aip:"create_time,order"`
	}

	schema := aipordering.SchemaFromTags(Copy{})
	mapping := fieldmapping.New(func(c *Copy) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&c.Name, copyPattern, "s.shelf_id", "c.copy_id"),
			fieldmapping.Column(&c.CreateTime, "c.create_time"),
		}
	})

	checked, err := aipordering.Compile("create_time desc, name", schema)
	if err != nil {
		fmt.Println(err) // Return INVALID_ARGUMENT.

		return
	}

	query := "SELECT ... FROM copies c JOIN shelves s USING (shelf_pk)"
	// An empty order_by gives an empty list, and then no ORDER BY.
	if list := ordering.OrderBy(checked, mapping); list != "" {
		query += " ORDER BY " + list
	}

	fmt.Println(query)
	// Output:
	// SELECT ... FROM copies c JOIN shelves s USING (shelf_pk) ORDER BY "c"."create_time" DESC, "s"."shelf_id", "c"."copy_id"
}

// The server sets the order of a list that the order_by of the request
// does not give: the newest copies first by default, and the name to break
// ties. A request can only order by title, but the server sorts by
// create_time and name, which have no order option.
func ExampleServerOrderRules_CombineOrderBy() {
	type Copy struct {
		Name       string    `aip:"name"`
		Title      string    `aip:"title,order"`
		CreateTime time.Time `aip:"create_time"`
	}

	requestSchema := aipordering.SchemaFromTags(Copy{})
	mapping := fieldmapping.New(func(c *Copy) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&c.Name, "copies/{copy}", "copy_id"),
			fieldmapping.Column(&c.Title, "title"),
			fieldmapping.Column(&c.CreateTime, "create_time"),
		}
	})

	// Build the rules when the program starts: a key that the server cannot
	// sort by panics there, not in a request.
	copyOrderRules := ordering.NewServerOrderRules(mapping, "create_time desc", "name")

	for _, orderBy := range []string{"", "title"} {
		requestedOrderBy, err := aipordering.Compile(orderBy, requestSchema)
		if err != nil {
			fmt.Println(err) // Return INVALID_ARGUMENT.

			return
		}

		fullOrderBy := copyOrderRules.CombineOrderBy(requestedOrderBy)
		fmt.Printf("order_by %q: ORDER BY %s\n", orderBy, ordering.OrderBy(fullOrderBy, mapping))
	}
	// Output:
	// order_by "": ORDER BY "create_time" DESC, "copy_id"
	// order_by "title": ORDER BY "title", "copy_id"
}
