package ordering_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
	"github.com/AndreiCocan/golang-aip-postgres/ordering"
)

// event is a domain type whose server sorts by fields that the order_by of
// a request cannot name: only title has the order option.
type event struct {
	Name       string            `aip:"name"`
	Title      string            `aip:"title,order"`
	CreateTime time.Time         `aip:"create_time"`
	Labels     map[string]string `aip:"labels,filter"`
}

var (
	eventRequestSchema = aipordering.SchemaFromTags(event{})
	eventMapping       = fieldmapping.New(func(e *event) []fieldmapping.Entry {
		return []fieldmapping.Entry{
			fieldmapping.ResourceNameColumns(&e.Name, "events/{event}", "event_id"),
			fieldmapping.Column(&e.Title, "title"),
			fieldmapping.Column(&e.CreateTime, "create_time"),
			fieldmapping.JSONBColumn(&e.Labels, "labels"),
		}
	})
)

// key returns the ordering key of path, descending when desc is true.
func key(path string, desc bool) aipordering.Key {
	return aipordering.Key{Segments: []string{path}, Desc: desc}
}

func TestServerOrderRulesCombineOrderBy(t *testing.T) {
	t.Parallel()

	newestFirst := ordering.NewServerOrderRules(eventMapping, "create_time desc", "name")

	tests := []struct {
		name             string
		rules            ordering.ServerOrderRules
		requestedOrderBy string
		want             []aipordering.Key
	}{
		{
			name:             "no order_by gives the default order_by and the tie-breaker",
			rules:            newestFirst,
			requestedOrderBy: "",
			want:             []aipordering.Key{key("create_time", true), key("name", false)},
		},
		{
			name:             "requested order_by replaces the default order_by",
			rules:            newestFirst,
			requestedOrderBy: "title desc",
			want:             []aipordering.Key{key("title", true), key("name", false)},
		},
		{
			name:             "default order_by with the tie-breaker",
			rules:            ordering.NewServerOrderRules(eventMapping, "name", "name"),
			requestedOrderBy: "",
			want:             []aipordering.Key{key("name", false)},
		},
		{
			name:             "no default order_by gives the tie-breaker only",
			rules:            ordering.NewServerOrderRules(eventMapping, "", "name desc"),
			requestedOrderBy: "",
			want:             []aipordering.Key{key("name", true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			requestedOrderBy, err := aipordering.Compile(tt.requestedOrderBy, eventRequestSchema)
			if err != nil {
				t.Fatalf("Compile(%q) error = %v", tt.requestedOrderBy, err)
			}

			got := tt.rules.CombineOrderBy(requestedOrderBy)
			if diff := cmp.Diff(&aipordering.CheckedOrderBy{Keys: tt.want}, got); diff != "" {
				t.Errorf("CombineOrderBy() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestServerOrderRulesCombineOrderByKeepsTheTieBreakerOfTheRequest(t *testing.T) {
	t.Parallel()

	rules := ordering.NewServerOrderRules(eventMapping, "", "name")

	// A request schema in which name is orderable.
	requestedOrderBy, err := aipordering.Compile(
		"name desc, title",
		aipordering.NewSchema("name", "title"),
	)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	want := &aipordering.CheckedOrderBy{
		Keys: []aipordering.Key{key("name", true), key("title", false)},
	}
	if diff := cmp.Diff(want, rules.CombineOrderBy(requestedOrderBy)); diff != "" {
		t.Errorf("CombineOrderBy() mismatch (-want +got):\n%s", diff)
	}
}

func TestServerOrderRulesCombineOrderByNilRequest(t *testing.T) {
	t.Parallel()

	rules := ordering.NewServerOrderRules(eventMapping, "create_time desc", "name")

	if diff := cmp.Diff(rules.DefaultFullOrderBy(), rules.CombineOrderBy(nil)); diff != "" {
		t.Errorf("CombineOrderBy(nil) mismatch with DefaultFullOrderBy (-want +got):\n%s", diff)
	}
}

func TestServerOrderRulesCombineOrderByKeepsTheRequestedOrderBy(t *testing.T) {
	t.Parallel()

	rules := ordering.NewServerOrderRules(eventMapping, "", "name")

	// The spare capacity lets an append write into the array of the
	// requested order_by, unless CombineOrderBy copies it.
	requestedOrderBy := &aipordering.CheckedOrderBy{Keys: make([]aipordering.Key, 1, 2)}
	requestedOrderBy.Keys[0] = key("title", false)

	rules.CombineOrderBy(requestedOrderBy)

	want := &aipordering.CheckedOrderBy{Keys: []aipordering.Key{key("title", false)}}
	if diff := cmp.Diff(want, requestedOrderBy); diff != "" {
		t.Errorf("requested order_by changed (-want +got):\n%s", diff)
	}

	if spare := requestedOrderBy.Keys[:2][1]; len(spare.Segments) != 0 {
		t.Errorf("spare capacity of the requested order_by = %v, want unchanged", spare)
	}
}

func TestServerOrderRulesDefaultFullOrderBy(t *testing.T) {
	t.Parallel()

	rules := ordering.NewServerOrderRules(
		eventMapping,
		"create_time desc, create_time desc",
		"name",
	)

	// The duplicate key of the default order_by counts once.
	want := &aipordering.CheckedOrderBy{Keys: []aipordering.Key{
		key("create_time", true),
		key("name", false),
	}}
	if diff := cmp.Diff(want, rules.DefaultFullOrderBy()); diff != "" {
		t.Errorf("DefaultFullOrderBy() mismatch (-want +got):\n%s", diff)
	}
}

func TestNewServerOrderRulesPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		defaultOrderBy string
		tieBreaker     string
	}{
		{"default order_by that does not parse", "name asc", "name"},
		{"default order_by with an unmapped path", "create_tme desc", "name"},
		{"default order_by with a jsonb map", "labels", "name"},
		{"default order_by in both directions", "title, title desc", "name"},
		{"no tie-breaker", "title", ""},
		{"tie-breaker with two keys", "title", "name, create_time"},
		{"tie-breaker with an unmapped path", "title", "uid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("NewServerOrderRules() did not panic")
				}
			}()

			ordering.NewServerOrderRules(eventMapping, tt.defaultOrderBy, tt.tieBreaker)
		})
	}
}
