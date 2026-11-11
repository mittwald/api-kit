package output

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/ohler55/ojg/jp"

	"github.com/mittwald/cli-kit/format"

	apikit "github.com/mittwald/api-kit"
)

// WriteJSONPath writes what a JSONPath expression selects of each item.
// The expression is applied to every item on its own, first to a list
// holding just that item and, when that selects nothing, to the item itself.
// So "$[?(@.status == 'open')].id" and "$[*].name" pick from the items the
// way they would over the whole result, and "$.name" reads a field of each
// item.
//
// When everything selected is a scalar, each value goes on a line of its
// own. As soon as an object or a list is among the selection, it is written
// as JSON: the one value when there is exactly one, a list otherwise. An
// expression that cannot be parsed is an error of kind [apikit.KindUsage].
func WriteJSONPath(w io.Writer, items []any, expression string) error {
	path, err := jp.ParseString(expression)
	if err != nil && strings.Contains(expression, ".[") {
		// Some dialects write "$.[*]" for "$[*]".
		path, err = jp.ParseString(strings.ReplaceAll(expression, ".[", "["))
	}
	if err != nil {
		return apikit.Errorf(apikit.KindUsage, "invalid JSONPath expression %q: %w", expression, err)
	}
	var selected []any
	structured := false
	for _, item := range items {
		node := wrap(item)
		var matches []any
		if _, isObject := item.(*apikit.Object); isObject {
			matches = path.Get([]any{node})
		}
		if len(matches) == 0 {
			matches = path.Get(node)
		}
		for _, match := range matches {
			value := unwrap(match)
			switch value.(type) {
			case *apikit.Object, []any:
				structured = true
			}
			selected = append(selected, value)
		}
	}
	if structured {
		if len(selected) == 1 {
			return format.WriteJSON(w, selected[0])
		}
		return format.WriteJSON(w, selected)
	}
	lines := make([]string, len(selected))
	for i, value := range selected {
		lines[i] = apikit.Text(value)
	}
	return writeLines(w, lines)
}

// node presents an object to the JSONPath library. It keeps the order of
// the keys, which a plain map would lose, and hands out numbers as numbers
// so that filters can compare them.
type node struct {
	object *apikit.Object
}

// ValueForKey returns the value of a key, prepared for the library.
func (n node) ValueForKey(key string) (any, bool) {
	value, ok := n.object.Get(key)
	return wrap(value), ok
}

// SetValueForKey does nothing: expressions only read.
func (n node) SetValueForKey(string, any) {}

// RemoveValueForKey does nothing: expressions only read.
func (n node) RemoveValueForKey(string) {}

// Keys returns the keys in the order of the object.
func (n node) Keys() []string { return n.object.Keys() }

// wrap prepares a value for the JSONPath library.
func wrap(v any) any {
	switch value := v.(type) {
	case *apikit.Object:
		if value == nil {
			return nil
		}
		return node{object: value}
	case []any:
		list := make([]any, len(value))
		for i, item := range value {
			list[i] = wrap(item)
		}
		return list
	case json.Number:
		if whole, err := value.Int64(); err == nil {
			return whole
		}
		if number, err := value.Float64(); err == nil {
			return number
		}
		return value.String()
	}
	return v
}

// unwrap turns what the JSONPath library selected back into the values the
// rest of api-kit works with.
func unwrap(v any) any {
	switch value := v.(type) {
	case node:
		return value.object
	case []any:
		list := make([]any, len(value))
		for i, item := range value {
			list[i] = unwrap(item)
		}
		return list
	case int64:
		return json.Number(strconv.FormatInt(value, 10))
	case float64:
		return json.Number(strconv.FormatFloat(value, 'g', -1, 64))
	}
	return v
}
