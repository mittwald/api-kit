package engine

import (
	"encoding/json"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/expr"
)

// values are the named values of one call: its parameters, the variables
// they set and what its steps kept. A template writes a name with dashes or
// with underscores, so a value is filed under one spelling of its name. A
// name that was not given is absent: no value is ever nil.
type values map[string]any

// variableKey returns the spelling a name is filed under.
func variableKey(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

// get returns the value of name, of whatever type, and whether it has one.
// It is the lookup of the templates that pass values on: payloads and the
// parameters of steps.
func (v values) get(name string) (any, bool) {
	value, ok := v[variableKey(name)]
	return value, ok
}

// has reports whether name has a value.
func (v values) has(name string) bool {
	_, ok := v.get(name)
	return ok
}

// set gives name a value.
func (v values) set(name string, value any) {
	v[variableKey(name)] = value
}

// scalar returns the value of name if it is a string or a number. It is the
// lookup of the templates that become part of a URL, a credential or a
// message, where a flag or a list has no spelling of its own: a placeholder
// for one stays as it is written.
func (v values) scalar(name string) (any, bool) {
	switch value, _ := v.get(name); value.(type) {
	case string, json.Number:
		return value, true
	}
	return nil, false
}

// renderWith returns a template of a definition with the values lookup
// knows. A placeholder without a value stays as it is written, so that a
// text which merely looks like one passes through.
func renderWith(template string, lookup func(name string) (any, bool)) string {
	out, _ := expr.ParseTemplate(template).Render(lookup, expr.RenderOptions{})
	return out
}

// optionList names parameters by the options they are given with, sorted.
func optionList(names []string) string {
	options := make([]string, len(names))
	for i, name := range names {
		options[i] = "--" + name
	}
	slices.Sort(options)
	return strings.Join(slices.Compact(options), ", ")
}

// itemObjects returns the items of what a request for a listing answered.
// A list holds them; anything else is one item. Only objects are items, and
// an object without a field is none: an API that has nothing to tell may
// answer with one.
func itemObjects(value any) []*apikit.Object {
	list, ok := value.([]any)
	if !ok {
		if object, isObject := value.(*apikit.Object); isObject && !apikit.Falsy(object) {
			return []*apikit.Object{object}
		}
		return nil
	}
	var items []*apikit.Object
	for _, element := range list {
		if object, isObject := element.(*apikit.Object); isObject {
			items = append(items, object)
		}
	}
	return items
}

// splitFilters reads the filters of a step or a lookup: "key=value" is sent
// to the API, "key!=value" drops the items with that value afterwards. A
// filter is taken apart before its placeholders are filled in, so a value
// with an operator in it stays a value.
func splitFilters(filters []string, lookup func(name string) (any, bool)) (query, excluded []Filter) {
	part := func(template string) string {
		return strings.TrimSpace(renderWith(template, lookup))
	}
	for _, filter := range filters {
		if key, value, found := strings.Cut(filter, "!="); found {
			excluded = append(excluded, Filter{Key: part(key), Value: part(value)})
			continue
		}
		key, value, _ := strings.Cut(filter, "=")
		query = append(query, Filter{Key: part(key), Value: part(value)})
	}
	return query, excluded
}

// withoutExcluded returns the items that have none of the excluded values.
// A value is compared as [apikit.Text] shows it.
func withoutExcluded(items []*apikit.Object, excluded []Filter) []*apikit.Object {
	return slices.DeleteFunc(items, func(item *apikit.Object) bool {
		return slices.ContainsFunc(excluded, func(filter Filter) bool {
			return apikit.Text(fieldOf(item, filter.Key)) == filter.Value
		})
	})
}
