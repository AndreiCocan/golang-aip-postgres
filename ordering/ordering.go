package ordering

import (
	"fmt"
	"strings"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
)

// OrderBy returns the checked order_by as a PostgreSQL ORDER BY list on the
// columns of mapping, without the ORDER BY keyword, such as
// "display_name" DESC, "create_time". A descending key has DESC. An
// ascending key has no suffix. A resource name with more than one variable
// gives one key for each of its columns, in the order of its pattern. NULL
// has the PostgreSQL default place: last in ascending order, and first in
// descending order. An empty or nil order_by gives an empty list.
//
// The schema of checked and mapping must come from the same domain type,
// with [aipordering.SchemaFromTags] and [fieldmapping.New], so that each
// key has a column. OrderBy panics when a key has no column, because this
// is a programming error, not bad client input.
func OrderBy(checked *aipordering.CheckedOrderBy, mapping *fieldmapping.Mapping) string {
	if checked == nil {
		return ""
	}

	keys := make([]string, 0, len(checked.Keys))

	for _, f := range checked.Keys {
		columns, ok := mapping.Columns(f.Path())
		if !ok {
			panic(fmt.Sprintf("ordering: OrderBy: field %q has no column", f.Path()))
		}

		for _, column := range columns {
			if f.Desc {
				column += " DESC"
			}

			keys = append(keys, column)
		}
	}

	return strings.Join(keys, ", ")
}
