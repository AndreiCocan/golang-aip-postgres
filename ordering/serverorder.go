package ordering

import (
	"fmt"
	"slices"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
)

// ServerOrderRules are the parts of the order of a list that the server
// sets: the default order_by, and the tie-breaker. Build them once for each
// table with [NewServerOrderRules], when the program starts. Then
// get the full order of each list request with
// [ServerOrderRules.CombineOrderBy], or with
// [ServerOrderRules.DefaultFullOrderBy] for a list request that has no
// order_by field.
//
// A ServerOrderRules is immutable and safe for concurrent use. Its zero
// value is not valid: it has no tie-breaker.
type ServerOrderRules struct {
	// defaultOrderBy is the order of a list request that has no order_by.
	// It can have no keys.
	defaultOrderBy *aipordering.CheckedOrderBy
	// tieBreaker is a field with a unique value for each row.
	tieBreaker aipordering.Key
}

// NewServerOrderRules returns the rules of the server for the order
// of the lists of the rows that mapping maps.
//
// defaultOrderBy is the order of a list request that has no order_by, such
// as "create_time desc". It can be empty: then such a list is sorted by the
// tie-breaker only. tieBreaker is a field with a unique value for each row,
// such as "name". It sorts the rows that have equal keys, so that each row
// has one position in the list, and pages never skip or repeat a row.
//
// Both have the order_by syntax. NewServerOrderRules compiles them
// as [aipordering.Compile] does: it parses them, and checks them against
// [fieldmapping.Mapping.SortKeySchema], the paths that the server can sort
// by. Thus the server can sort by each key of the rules, also a key that
// the order_by of a request cannot name.
//
// The rules are programmer input, so NewServerOrderRules panics
// instead of returning an error: when an order_by does not parse, when it
// names a path that the server cannot sort by, when it sorts one path in
// both directions, or when tieBreaker does not have exactly one key. Call
// it when the program starts, so that a mistake stops the program before it
// serves a request.
func NewServerOrderRules(
	mapping *fieldmapping.Mapping,
	defaultOrderBy string,
	tieBreaker string,
) ServerOrderRules {
	schema := mapping.SortKeySchema()

	tieBreakerOrderBy := mustCompileServerOrderBy("tieBreaker", tieBreaker, schema)
	if len(tieBreakerOrderBy.Keys) != 1 {
		panic(fmt.Sprintf(
			"ordering: NewServerOrderRules: tieBreaker %q must have exactly one key",
			tieBreaker,
		))
	}

	return ServerOrderRules{
		defaultOrderBy: mustCompileServerOrderBy("defaultOrderBy", defaultOrderBy, schema),
		tieBreaker:     tieBreakerOrderBy.Keys[0],
	}
}

// CombineOrderBy returns the full order of the rows of a list:
// requestedOrderBy, or the default order_by when requestedOrderBy has no
// keys, followed by the tie-breaker unless the order already has its path,
// in either direction.
//
// requestedOrderBy is the order_by of the request, checked with
// [aipordering.Compile] against the schema of the request. A nil
// requestedOrderBy is a request with no order_by. CombineOrderBy does not
// change requestedOrderBy.
func (r ServerOrderRules) CombineOrderBy(
	requestedOrderBy *aipordering.CheckedOrderBy,
) *aipordering.CheckedOrderBy {
	if requestedOrderBy == nil || len(requestedOrderBy.Keys) == 0 {
		return r.DefaultFullOrderBy()
	}

	return r.withTieBreaker(requestedOrderBy.Keys)
}

// DefaultFullOrderBy returns the full order of the rows of a list request
// that has no order_by: the default order_by, followed by the tie-breaker
// unless the default order_by already has its path. Use it for a list
// request that has no order_by field.
func (r ServerOrderRules) DefaultFullOrderBy() *aipordering.CheckedOrderBy {
	return r.withTieBreaker(r.defaultOrderBy.Keys)
}

// withTieBreaker returns keys followed by the tie-breaker, unless keys
// already have its path. It does not change keys.
func (r ServerOrderRules) withTieBreaker(keys []aipordering.Key) *aipordering.CheckedOrderBy {
	tieBreakerPath := r.tieBreaker.Path()

	hasTieBreaker := slices.ContainsFunc(keys, func(key aipordering.Key) bool {
		return key.Path() == tieBreakerPath
	})
	if hasTieBreaker {
		return &aipordering.CheckedOrderBy{Keys: slices.Clone(keys)}
	}

	return &aipordering.CheckedOrderBy{Keys: append(slices.Clone(keys), r.tieBreaker)}
}

// mustCompileServerOrderBy parses orderBy, an order_by of the server, and
// checks it against schema. argument names the argument of
// [NewServerOrderRules] in the panic message.
func mustCompileServerOrderBy(
	argument string,
	orderBy string,
	schema *aipordering.Schema,
) *aipordering.CheckedOrderBy {
	checked, err := aipordering.Compile(orderBy, schema)
	if err != nil {
		panic(fmt.Sprintf("ordering: NewServerOrderRules: %s: %v", argument, err))
	}

	return checked
}
