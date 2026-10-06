package fieldmapping_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	aipordering "github.com/AndreiCocan/golang-aip/ordering"

	"github.com/AndreiCocan/golang-aip-postgres/fieldmapping"
)

// bookPattern is the resource name pattern of a book.
const bookPattern = "books/{book}"

type book struct {
	Name       string            `aip:"name,filter,order,search"`
	UID        string            `aip:"uid,filter,order,search"`
	Title      string            `aip:"title,filter,order,search"`
	Pages      int64             `aip:"page_count,filter,order"`
	Labels     map[string]string `aip:"labels,filter"`
	CreateTime time.Time         `aip:"create_time,filter,order"`
	DeleteTime time.Time         `aip:"delete_time,filter,order"`
	Notes      string            `aip:"notes"`
	Revision   int64
}

var bookMapping = fieldmapping.New(func(b *book) []fieldmapping.Entry {
	return []fieldmapping.Entry{
		fieldmapping.ResourceNameColumns(&b.Name, bookPattern, "book_id"),
		fieldmapping.UUIDColumn(&b.UID, "uid"),
		fieldmapping.Column(&b.Title, "title"),
		fieldmapping.Column(&b.Pages, "page_count"),
		fieldmapping.JSONBColumn(&b.Labels, "labels"),
		fieldmapping.Column(&b.CreateTime, "create_time"),
		fieldmapping.Column(&b.DeleteTime, "delete_time"),
	}
})

// event has a field that the server sorts by, and that the order_by of a
// request cannot name: it has no order option.
type event struct {
	Sequence int64 `aip:"sequence"`
}

var eventMapping = fieldmapping.New(func(e *event) []fieldmapping.Entry {
	return []fieldmapping.Entry{fieldmapping.Column(&e.Sequence, "sequence")}
})

// chapterPattern is the resource name pattern of a chapter, which has
// parents in its name.
const chapterPattern = "publishers/{publisher}/books/{book}/chapters/{chapter}/text"

// chapter is a resource with parents in its name.
type chapter struct {
	Name string `aip:"name,filter,order"`
}

type singleton struct {
	Name string `aip:"name,filter"`
}

var chapterMapping = fieldmapping.New(func(c *chapter) []fieldmapping.Entry {
	return []fieldmapping.Entry{
		fieldmapping.ResourceNameColumns(
			&c.Name,
			chapterPattern,
			"p.publisher_id",
			"b.book_id",
			"c.chapter_id",
		),
	}
})

func TestMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mapping     *fieldmapping.Mapping
		wantColumns []string
		wantExpr    string
		wantOK      bool
	}{
		{
			name:        "name",
			mapping:     bookMapping,
			wantColumns: []string{`"book_id"`},
			wantExpr:    `('books/' || "book_id")`,
			wantOK:      true,
		},
		{
			name:        "uid",
			mapping:     bookMapping,
			wantColumns: []string{`"uid"`},
			wantExpr:    `"uid"::text`,
			wantOK:      true,
		},
		{
			name:        "title",
			mapping:     bookMapping,
			wantColumns: []string{`"title"`},
			wantExpr:    `"title"`,
			wantOK:      true,
		},
		{
			name:        "page_count",
			mapping:     bookMapping,
			wantColumns: []string{`"page_count"`},
			wantExpr:    `"page_count"`,
			wantOK:      true,
		},
		{
			name:        "labels",
			mapping:     bookMapping,
			wantColumns: []string{`"labels"`},
			wantExpr:    `"labels"`,
			wantOK:      true,
		},
		{
			name:        "delete_time",
			mapping:     bookMapping,
			wantColumns: []string{`"delete_time"`},
			wantExpr:    `"delete_time"`,
			wantOK:      true,
		},
		{name: "notes", mapping: bookMapping},
		{name: "Revision", mapping: bookMapping},
		{
			name:        "name",
			mapping:     chapterMapping,
			wantColumns: []string{`"p"."publisher_id"`, `"b"."book_id"`, `"c"."chapter_id"`},
			wantExpr: `('publishers/' || "p"."publisher_id" || '/books/' || "b"."book_id" || ` +
				`'/chapters/' || "c"."chapter_id" || '/text')`,
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			columns, ok := tt.mapping.Columns(tt.name)
			if ok != tt.wantOK {
				t.Errorf("Columns(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			}

			if diff := cmp.Diff(tt.wantColumns, columns); diff != "" {
				t.Errorf("Columns(%q) mismatch (-want +got):\n%s", tt.name, diff)
			}

			expr, ok := tt.mapping.FilterExpr(tt.name)
			if expr != tt.wantExpr || ok != tt.wantOK {
				t.Errorf("FilterExpr(%q) = %q, %v, want %q, %v",
					tt.name, expr, ok, tt.wantExpr, tt.wantOK)
			}
		})
	}

	t.Run("search", func(t *testing.T) {
		t.Parallel()

		want := []string{`('books/' || "book_id")`, `"uid"::text`, `"title"`}
		if diff := cmp.Diff(want, bookMapping.SearchExprs()); diff != "" {
			t.Errorf("SearchExprs() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestMappingEqualCondition(t *testing.T) {
	t.Parallel()

	const uid = "00000000-0000-4000-8000-000000000001"

	tests := []struct {
		name     string
		mapping  *fieldmapping.Mapping
		field    string
		value    string
		wantSQL  string
		wantArgs []any
		wantOK   bool
	}{
		{"text", bookMapping, "title", "War", `"title" = $1`, []any{"War"}, true},
		{"name", bookMapping, "name", "books/b1", `"book_id" = $1`, []any{"b1"}, true},
		{"name of another pattern", bookMapping, "name", "shelves/b1", `FALSE`, nil, true},
		{"name with too few segments", bookMapping, "name", "books", `FALSE`, nil, true},
		{"name with too many segments", bookMapping, "name", "books/b1/b2", `FALSE`, nil, true},
		{"name with a variable segment", bookMapping, "name", "books/{b1}", `FALSE`, nil, true},
		{"full name", bookMapping, "name", "//library.example.com/books/b1", `FALSE`, nil, true},
		{"uuid", bookMapping, "uid", uid, `"uid" = $1::uuid`, []any{uid}, true},
		{
			"uppercase uuid",
			bookMapping,
			"uid",
			"00000000-0000-4000-8000-00000000000A",
			`FALSE`,
			nil,
			true,
		},
		{"short uuid", bookMapping, "uid", "0000", `FALSE`, nil, true},
		{
			"name with parents", chapterMapping, "name", "publishers/p/books/b/chapters/c/text",
			`("p"."publisher_id" = $1 AND "b"."book_id" = $2 AND "c"."chapter_id" = $3)`,
			[]any{"p", "b", "c"},
			true,
		},
		{
			"name with a wrong literal",
			chapterMapping,
			"name",
			"publishers/p/books/b/chapters/c/html",
			`FALSE`,
			nil,
			true,
		},
		{"unknown field", bookMapping, "notes", "x", "", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var args []any

			bind := func(v any) string {
				args = append(args, v)

				return "$" + strconv.Itoa(len(args))
			}

			sql, ok := tt.mapping.EqualCondition(tt.field, tt.value, bind)
			if sql != tt.wantSQL || ok != tt.wantOK {
				t.Errorf("EqualCondition(%q, %q) = %q, %v, want %q, %v",
					tt.field, tt.value, sql, ok, tt.wantSQL, tt.wantOK)
			}

			if diff := cmp.Diff(tt.wantArgs, args); diff != "" {
				t.Errorf(
					"EqualCondition(%q, %q) args mismatch (-want +got):\n%s",
					tt.field,
					tt.value,
					diff,
				)
			}
		})
	}
}

func TestMappingSortKeyValues(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	war := &book{
		Name:       "books/war",
		UID:        "00000000-0000-4000-8000-000000000001",
		Title:      "War",
		Pages:      1225,
		CreateTime: created,
	}

	tests := []struct {
		name    string
		mapping *fieldmapping.Mapping
		path    string
		value   any
		want    []any
		wantOK  bool
	}{
		{"name", bookMapping, "name", war, []any{"war"}, true},
		{
			"name with parents",
			chapterMapping,
			"name",
			&chapter{Name: "publishers/p/books/b/chapters/c/text"},
			[]any{"p", "b", "c"},
			true,
		},
		{"uuid", bookMapping, "uid", war, []any{"00000000-0000-4000-8000-000000000001"}, true},
		{"text", bookMapping, "title", war, []any{"War"}, true},
		{"integer", bookMapping, "page_count", war, []any{int64(1225)}, true},
		{"set time", bookMapping, "create_time", war, []any{created}, true},
		{"zero time", bookMapping, "delete_time", war, []any{nil}, true},
		{
			"name that does not match the pattern",
			bookMapping,
			"name",
			&book{Name: "shelves/war"},
			nil,
			false,
		},
		{"empty name", bookMapping, "name", &book{}, nil, false},
		{
			"field without the order option",
			eventMapping,
			"sequence",
			&event{Sequence: 7},
			[]any{int64(7)},
			true,
		},
		{"jsonb map", bookMapping, "labels", war, nil, false},
		{"unknown field", bookMapping, "notes", war, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.mapping.SortKeyValues(tt.path, tt.value)
			if ok != tt.wantOK {
				t.Errorf("SortKeyValues(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("SortKeyValues(%q) mismatch (-want +got):\n%s", tt.path, diff)
			}
		})
	}
}

func TestMappingSortKeyValuesPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"nil pointer", (*book)(nil)},
		{"value instead of a pointer", book{Name: "books/war"}},
		{"pointer to another type", &chapter{Name: "books/war"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("SortKeyValues() did not panic")
				}
			}()

			bookMapping.SortKeyValues("name", tt.value)
		})
	}
}

func TestMappingCopyField(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	war := book{
		Name:       "books/war",
		Title:      "War",
		Labels:     map[string]string{"genre": "novel"},
		CreateTime: created,
	}

	tests := []struct {
		name   string
		path   string
		want   book
		wantOK bool
	}{
		{"name", "name", book{Name: "books/war"}, true},
		{"time", "create_time", book{CreateTime: created}, true},
		{"map", "labels", book{Labels: map[string]string{"genre": "novel"}}, true},
		{"unknown field", "notes", book{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got book

			if ok := bookMapping.CopyField(tt.path, &got, &war); ok != tt.wantOK {
				t.Errorf("CopyField(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("CopyField(%q) mismatch (-want +got):\n%s", tt.path, diff)
			}
		})
	}
}

func TestMappingCopyFieldPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dst, src any
	}{
		{"nil destination", nil, &book{}},
		{"nil source", &book{}, nil},
		{"value instead of a pointer", book{}, &book{}},
		{"pointer to another type", &book{}, &chapter{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("CopyField() did not panic")
				}
			}()

			bookMapping.CopyField("title", tt.dst, tt.src)
		})
	}
}

func TestMappingValidSortKeyValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mapping *fieldmapping.Mapping
		path    string
		values  []any
		want    bool
	}{
		{"name", bookMapping, "name", []any{"war"}, true},
		{"name with parents", chapterMapping, "name", []any{"p", "b", "c"}, true},
		{"name with too few values", chapterMapping, "name", []any{"p", "b"}, false},
		{"name with too many values", bookMapping, "name", []any{"war", "peace"}, false},
		{"name that is not a string", bookMapping, "name", []any{42}, false},
		{"text", bookMapping, "title", []any{"War"}, true},
		{"text of another type", bookMapping, "title", []any{int64(1)}, false},
		{"nil text", bookMapping, "title", []any{nil}, false},
		{"integer", bookMapping, "page_count", []any{int64(1225)}, true},
		{"integer of another type", bookMapping, "page_count", []any{1225}, false},
		{"time", bookMapping, "create_time", []any{time.Now()}, true},
		{"nil time", bookMapping, "delete_time", []any{nil}, true},
		{"time as text", bookMapping, "delete_time", []any{"yesterday"}, false},
		{"uuid", bookMapping, "uid", []any{"00000000-0000-4000-8000-000000000001"}, true},
		{
			"uppercase uuid",
			bookMapping,
			"uid",
			[]any{"00000000-0000-4000-8000-00000000000A"},
			false,
		},
		{"malformed uuid", bookMapping, "uid", []any{"not-a-uuid"}, false},
		{"no values", bookMapping, "title", nil, false},
		{"field without the order option", eventMapping, "sequence", []any{int64(7)}, true},
		{"jsonb map", bookMapping, "labels", []any{map[string]string{}}, false},
		{"unknown field", bookMapping, "notes", []any{"x"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mapping.ValidSortKeyValues(tt.path, tt.values); got != tt.want {
				t.Errorf(
					"ValidSortKeyValues(%q, %v) = %v, want %v",
					tt.path,
					tt.values,
					got,
					tt.want,
				)
			}
		})
	}
}

type untagged struct {
	Title string
}

type invalidTag struct {
	Title string `aip:"title,sort"`
}

type labelled struct {
	Labels map[string]string `aip:"labels,filter"`
}

type titled struct {
	Title string `aip:"title,filter"`
}

type hidden struct {
	title string `aip:"title,filter"`
}

func TestNewPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func()
	}{
		{"not a struct", func() {
			fieldmapping.New(func(s *string) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.Column(s, "s")}
			})
		}},
		{"pointer outside the struct", func() {
			var other string

			fieldmapping.New(func(*book) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.Column(&other, "other")}
			})
		}},
		{"nil pointer", func() {
			fieldmapping.New(func(*book) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.Column[string](nil, "title")}
			})
		}},
		{"field without a tag", func() {
			fieldmapping.New(func(u *untagged) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.Column(&u.Title, "title")}
			})
		}},
		{"invalid tag", func() {
			fieldmapping.New(func(i *invalidTag) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.Column(&i.Title, "title")}
			})
		}},
		{"invalid tag on an unmapped field", func() {
			fieldmapping.New(func(*invalidTag) []fieldmapping.Entry { return nil })
		}},
		{"field mapped twice", func() {
			fieldmapping.New(func(l *labelled) []fieldmapping.Entry {
				return []fieldmapping.Entry{
					fieldmapping.JSONBColumn(&l.Labels, "labels"),
					fieldmapping.JSONBColumn(&l.Labels, "labels2"),
				}
			})
		}},
		{"tagged field not mapped", func() {
			fieldmapping.New(func(*labelled) []fieldmapping.Entry { return nil })
		}},
		{"value field of a map type", func() {
			fieldmapping.New(func(l *labelled) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.Column(&l.Labels, "labels")}
			})
		}},
		{"one column for a pattern with three variables", func() {
			fieldmapping.New(func(c *chapter) []fieldmapping.Entry {
				return []fieldmapping.Entry{
					fieldmapping.ResourceNameColumns(&c.Name, chapterPattern, "chapter_id"),
				}
			})
		}},
		{"pattern without variables", func() {
			fieldmapping.New(func(s *singleton) []fieldmapping.Entry {
				return []fieldmapping.Entry{
					fieldmapping.ResourceNameColumns(&s.Name, "config", "name"),
				}
			})
		}},
		{"invalid pattern", func() {
			fieldmapping.New(func(t *titled) []fieldmapping.Entry {
				return []fieldmapping.Entry{
					fieldmapping.ResourceNameColumns(&t.Title, "books/{book_id}", "book_id"),
				}
			})
		}},
		{"empty pattern", func() {
			fieldmapping.New(func(t *titled) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.ResourceNameColumns(&t.Title, "", "title")}
			})
		}},
		{"too many columns for a pattern", func() {
			fieldmapping.New(func(c *chapter) []fieldmapping.Entry {
				return []fieldmapping.Entry{
					fieldmapping.ResourceNameColumns(&c.Name, chapterPattern, "a", "b", "c", "d"),
				}
			})
		}},
		{"no columns", func() {
			fieldmapping.New(func(t *titled) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.ResourceNameColumns(&t.Title, bookPattern)}
			})
		}},
		{"unexported field", func() {
			fieldmapping.New(func(h *hidden) []fieldmapping.Entry {
				return []fieldmapping.Entry{fieldmapping.Column(&h.title, "title")}
			})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("New() did not panic")
				}
			}()

			tt.build()
		})
	}
}

func TestMappingSortKeySchema(t *testing.T) {
	t.Parallel()

	schema := bookMapping.SortKeySchema()

	tests := []struct {
		path      string
		wantValid bool
	}{
		{"name", true},
		{"uid", true},
		{"title", true},
		{"page_count", true},
		{"create_time", true},
		{"delete_time", true},
		// A jsonb map has no order.
		{"labels", false},
		// notes has an aip tag, but no mapping.
		{"notes", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			_, err := aipordering.Compile(tt.path, schema)
			if gotValid := err == nil; gotValid != tt.wantValid {
				t.Errorf("Compile(%q) valid = %v, want %v (error %v)",
					tt.path, gotValid, tt.wantValid, err)
			}
		})
	}
}

func TestMappingSortKeySchemaWithoutOrderOption(t *testing.T) {
	t.Parallel()

	// sequence has no order option, but the server can sort by it.
	if _, err := aipordering.Compile("sequence desc", eventMapping.SortKeySchema()); err != nil {
		t.Errorf("Compile() error = %v, want nil", err)
	}
}
