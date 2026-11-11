package engine

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// ProjectOptions say what [Engine.Project] lays out and fetches.
type ProjectOptions struct {
	// AllColumns lays out every field of the items instead of the columns
	// the definition names.
	AllColumns bool
	// Joins fetches the values of joined resources.
	Joins bool
	// JoinMany includes the joins marked "many". They have no place in a
	// table.
	JoinMany bool
	// Links works out the link of every item.
	Links bool
}

// Table is a result laid out in columns, with the values as they are.
type Table struct {
	// Columns are the label and the paths of each column, in display
	// order.
	Columns []definition.Column
	// Rows hold one value per column for each item: a scalar, a list or
	// an object. A column that has nothing to show for an item holds the
	// empty string. What a join found is the very value that is stored
	// in the item, not a copy of it.
	Rows [][]any
	// Links are the links of the rows. It is nil when the resource has no
	// link template, and when no links were asked for.
	Links []string
}

// Project lays items out in columns and fetches the values of joined
// resources. It also completes the items themselves: what a column shows
// that the item does not hold is stored in the item under
// [definition.ExtraKey], and so is the link, under "link".
//
// The columns are those of the definition. With AllColumns, or for a
// resource that has none, they are the joins, every field of the items
// that holds a string, a number or a boolean, and the columns of the
// definition, with the fields first that the definition and convention
// take for the most telling ones.
//
// A column shows what its path leads to in the item. A path that names a
// join, or leads on from one as in "owner.city", shows what the join
// finds. An object shows as its name, and a list as the names of its
// elements. A column with several paths shows what all of them find, as
// one text. An item that is no object has no fields: its cells are empty.
//
// The requests of joins run at the same time, as many as the definition
// allows, and progress is reported at [StageJoin]. A related item that
// the API does not hand out leaves its cell empty. Project fails when the
// run ends, when there is no usable credential, and when the API cannot
// be reached; the items are then left as they were.
func (e *Engine) Project(ctx context.Context, resource string, items []any, o ProjectOptions) (*Table, error) {
	described := e.doc.Resource(resource)
	columns := e.columns(described, items, o)

	// A column with several paths shows what all of them find, so every
	// path of every column gets a place of its own in the row.
	width := 0
	for _, column := range columns {
		width += len(column.Paths)
	}
	found := make([][]any, len(items))
	var lookups []lookup
	for row, item := range items {
		object, _ := item.(*apikit.Object)
		found[row] = make([]any, 0, width)
		for _, column := range columns {
			for _, path := range column.Paths {
				var join *definition.Join
				var rest string
				if o.Joins {
					join, rest = joinFor(described, path)
				}
				if join != nil && object != nil && (o.JoinMany || !join.Many) {
					lookups = append(lookups, lookup{row: row, slot: len(found[row]), e: e, item: object, join: join, below: rest})
				}
				found[row] = append(found[row], own(object, path, join != nil))
			}
		}
	}
	if err := e.join(ctx, lookups, found); err != nil {
		return nil, err
	}

	table := &Table{Columns: columns, Rows: make([][]any, len(items))}
	linked := o.Links && described.URLTemplate != nil
	for row, item := range items {
		object, _ := item.(*apikit.Object)
		cells, rest := make([]any, len(columns)), found[row]
		for i, column := range columns {
			paths := len(column.Paths)
			if paths == 1 {
				cells[i] = rest[0]
			} else {
				cells[i] = merged(rest[:paths])
			}
			rest = rest[paths:]
			if object != nil && !object.Has(column.Label) {
				complete(object, column.Label, cells[i])
			}
		}
		table.Rows[row] = cells
		if linked {
			// The link comes last: its template may name what the columns
			// have just added to the item.
			link := e.Link(resource, object)
			table.Links = append(table.Links, link)
			if object != nil {
				complete(object, linkKey, link)
			}
		}
	}
	return table, nil
}

// columns returns the columns items of a resource are laid out in.
func (e *Engine) columns(resource *definition.Resource, items []any, o ProjectOptions) []definition.Column {
	if !o.AllColumns && len(resource.Columns) > 0 {
		return slices.Clone(resource.Columns)
	}
	var columns []definition.Column
	taken := map[string]bool{}
	add := func(name string) {
		columns = append(columns, definition.Column{Label: name, Paths: []string{name}})
		taken[name] = true
	}
	if o.Joins {
		for _, join := range resource.Joins {
			// A join that is not fetched would be a column of nothing.
			if o.JoinMany || !join.Many {
				add(join.Name)
			}
		}
	}
	for _, item := range items {
		object, _ := item.(*apikit.Object)
		for key, value := range object.All() {
			switch value.(type) {
			case string, json.Number, bool:
				if !taken[key] {
					add(key)
				}
			}
		}
	}
	for _, column := range resource.Columns {
		same := func(other definition.Column) bool {
			return other.Label == column.Label && slices.Equal(other.Paths, column.Paths)
		}
		if !slices.ContainsFunc(columns, same) {
			columns = append(columns, column)
		}
	}
	// Columns of one rank keep the order they were found in.
	slices.SortStableFunc(columns, func(a, b definition.Column) int {
		return strings.Compare(e.rank(resource, b), e.rank(resource, a))
	})
	return columns
}

// rank returns a text that sorts the higher the further to the left a
// column belongs when all fields are shown. It has one digit per rule, 1
// for a rule the column meets: first the fields the definition puts in
// front, in its order, then any field with "name" in its name, then any
// with "id". So a column that meets an earlier rule comes before every
// column that does not, whatever later rules those meet.
func (e *Engine) rank(resource *definition.Resource, column definition.Column) string {
	paths := make([]string, len(column.Paths))
	for i, path := range column.Paths {
		paths[i] = strings.ToLower(path)
	}
	holds := func(part string) bool {
		return slices.ContainsFunc(paths, func(path string) bool { return strings.Contains(path, part) })
	}
	var rank []byte
	meets := func(rule bool) {
		if rule {
			rank = append(rank, '1')
		} else {
			rank = append(rank, '0')
		}
	}
	for _, field := range e.doc.Output.ColumnOrder {
		field = strings.ReplaceAll(field, "${singular}", resource.Singular)
		meets(slices.Contains(paths, strings.ToLower(field)))
	}
	meets(holds("name"))
	meets(holds("id"))
	return string(rank)
}

// own returns what an item itself shows for one path of a column. joined
// says that the path names a join: the item is then asked only for a
// field of that very name which holds an object or a list, to be shown
// where the join finds nothing.
func own(item *apikit.Object, path string, joined bool) any {
	if item == nil {
		return ""
	}
	// A field whose name is the whole path shows where the path leads
	// nowhere, so that a field with a dot in its name can be a column.
	value, has := item.Get(path)
	if !joined && strings.ContainsAny(path, ".[") {
		switch deep := at(item, path).(type) {
		case nil:
		case *apikit.Object, []any:
			value = deep
		default:
			if deep != "" {
				return deep
			}
		}
	}
	switch value := value.(type) {
	case *apikit.Object:
		return names(value)
	case []any:
		return listed(value)
	}
	if joined || !has {
		return ""
	}
	return value
}

// names returns an object as the text of a cell: the values of its fields
// with "name" in their name, with spaces between them. An object without
// such a field shows those of the objects it holds, with a comma between
// one object and the next.
func names(object *apikit.Object) string {
	if direct := named(object); len(direct) > 0 {
		return strings.Join(direct, " ")
	}
	var inner []string
	for _, value := range object.All() {
		if child, ok := value.(*apikit.Object); ok {
			if parts := named(child); len(parts) > 0 {
				inner = append(inner, strings.Join(parts, " "))
			}
		}
	}
	return strings.Join(inner, ", ")
}

// named returns the values of the fields of an object that have "name" in
// their name, as text.
func named(object *apikit.Object) []string {
	var values []string
	for key, value := range object.All() {
		if nameLike(key) {
			values = append(values, apikit.Text(value))
		}
	}
	return values
}

// nameLike reports whether a field is taken to hold a name.
func nameLike(field string) bool {
	return strings.Contains(strings.ToLower(field), "name")
}

// listed returns a list as the text of a cell, with commas between its
// elements: texts as they are, and of objects the field that names them,
// which is the last field of the first object with "name" in its name.
// Any other list shows nothing.
func listed(list []any) string {
	if len(list) == 0 {
		return ""
	}
	texts := make([]string, len(list))
	switch first := list[0].(type) {
	case string:
		for i, element := range list {
			texts[i] = apikit.Text(element)
		}
	case *apikit.Object:
		field := ""
		for key := range first.All() {
			if nameLike(key) {
				field = key
			}
		}
		if field == "" {
			return ""
		}
		for i, element := range list {
			object, _ := element.(*apikit.Object)
			value, _ := object.Get(field)
			texts[i] = apikit.Text(value)
		}
	default:
		return ""
	}
	return strings.Join(texts, ", ")
}

// merged returns what the paths of one column found as one text: every
// value that says something, once, with commas between them.
func merged(values []any) string {
	var texts []string
	for _, value := range values {
		text := strings.TrimSpace(apikit.Text(value))
		if text != "" && !slices.Contains(texts, text) {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, ", ")
}

// complete stores a value in the object an item keeps under
// [definition.ExtraKey], which the item gets when it has none. An item
// whose API uses that key for something that is no object keeps it as it
// is.
func complete(item *apikit.Object, key string, value any) {
	extra, ok := item.Get(definition.ExtraKey)
	if !ok {
		extra = apikit.NewObject()
		item.Set(definition.ExtraKey, extra)
	}
	if object, ok := extra.(*apikit.Object); ok {
		object.Set(key, value)
	}
}

// at returns the value a path leads to in a value. As everywhere, the
// path finds what was added to an item under [definition.ExtraKey]. A
// path that cannot be parsed leads nowhere.
func at(value any, path string) any {
	parsed, err := expr.ParsePath(path)
	if err != nil {
		return nil
	}
	return parsed.Get(value, definition.ExtraKey)
}
