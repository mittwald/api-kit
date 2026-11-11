package engine

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// ResolveID returns the ID of the item of a resource that value names. A
// value that looks like an ID is returned as it is. Any other value is
// looked up: through the ID aliases and the ID search of the resource, and
// by asking the API for the item, which may know it by the value and tell
// its ID.
//
// A value that names no item, or that names several, is an error of kind
// [apikit.KindNotFound]. An alias that names a field counts only for an
// item whose field equals the value, not for one that merely mentions it:
// a change must reach the item that was meant.
func (e *Engine) ResolveID(ctx context.Context, resource, value string) (string, error) {
	id, err := e.alias(ctx, resource, value, true)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", e.notFound(resource, value)
	}
	if e.native(id) {
		return id, nil
	}

	item, err := e.get(ctx, Ref{Resource: resource, ID: id}, nil)
	if err != nil {
		failure, ok := errors.AsType[*HTTPError](err)
		switch {
		case !ok:
			return "", err
		case failure.Status == http.StatusNotFound:
			return "", e.notFound(resource, value)
		}
		// The API does not hand out the item this way. The value may
		// still be good where it is used.
		return id, nil
	}
	if object, ok := item.(*apikit.Object); ok {
		found, _ := e.idValue(resource, object)
		if text := idText(found); text != "" {
			return text, nil
		}
	}
	return id, nil
}

// IDOf returns the ID of an item of a resource, and whether the item has a
// field for it: the ID field of the resource, or the first of the ID
// fields of the definition that the item has.
func (e *Engine) IDOf(resource string, item *apikit.Object) (string, bool) {
	value, ok := e.idValue(resource, item)
	return apikit.Text(value), ok
}

// idValue returns the value of the field that holds the ID of an item.
func (e *Engine) idValue(resource string, item *apikit.Object) (any, bool) {
	described := e.doc.Resource(resource)
	if described.IDField != "" {
		if value, ok := item.Get(described.IDField); ok {
			return value, true
		}
	}
	for _, field := range e.doc.Identifiers.Fields {
		field = strings.ReplaceAll(field, "${singular}", described.Singular)
		if value, ok := item.Get(field); ok {
			return value, true
		}
	}
	return nil, false
}

// idText returns the value of an ID field as an ID. A value that counts as
// nothing, such as an empty string or 0, is no ID and yields "".
func idText(value any) string {
	if apikit.Falsy(value) {
		return ""
	}
	return apikit.Text(value)
}

// native reports whether value is an ID as the API hands them out.
func (e *Engine) native(value string) bool {
	pattern := e.doc.Identifiers.Native
	return pattern != nil && pattern.MatchString(value)
}

// alias returns the ID that value names through the ID aliases and the ID
// search of a resource. A value that is an ID, and one that no rule is
// for, is returned as it is. The first rule that is for the value decides:
// the result is empty when the rule does not find exactly one item.
//
// exact makes an alias that names its field count only the items whose
// field equals the value.
func (e *Engine) alias(ctx context.Context, resource, value string, exact bool) (string, error) {
	if value == "" || e.native(value) {
		return value, nil
	}
	described := e.doc.Resource(resource)
	for _, rule := range described.IDAliases {
		if !applies(rule.Match, rule.Not, value) {
			continue
		}
		result, err := e.get(ctx, Ref{Resource: resource}, aliasQuery(rule.Query, value))
		if err != nil {
			return "", err
		}
		items, _ := result.([]any)
		if exact && rule.Field != "" {
			items = slices.DeleteFunc(items, func(item any) bool {
				object, ok := item.(*apikit.Object)
				return !ok || apikit.Text(fieldOf(object, rule.Field)) != value
			})
		}
		if len(items) != 1 {
			return "", nil
		}
		object, ok := items[0].(*apikit.Object)
		if !ok {
			return "", nil
		}
		found, _ := e.idValue(resource, object)
		return idText(found), nil
	}
	for _, rule := range described.IDSearch {
		if rule.Column == "" || !applies(rule.Match, rule.Not, value) {
			continue
		}
		result, err := e.getAll(ctx, Ref{Resource: resource}, nil, e.doc.Pagination.PageSize)
		if err != nil {
			return "", err
		}
		items, _ := result.([]any)
		for _, item := range items {
			object, ok := item.(*apikit.Object)
			if !ok {
				continue
			}
			if text, ok := fieldOf(object, rule.Column).(string); !ok || text != value {
				continue
			}
			if found := fieldOf(object, rule.IDColumn); found != nil {
				return idText(found), nil
			}
		}
		return "", nil
	}
	return value, nil
}

// applies reports whether a rule with this pattern is for value. A rule
// without a pattern is for no value.
func applies(match *regexp.Regexp, not bool, value string) bool {
	if match == nil || match.String() == "" {
		return false
	}
	return match.MatchString(value) != not
}

// aliasQuery returns the query an ID alias searches with. The template is
// split into its parameters first and the value is put into each of them
// after that, so a value with a comma or an equals sign stays one value
// and cannot add a filter of its own.
func aliasQuery(template, value string) []Filter {
	lookup := func(name string) (any, bool) {
		return value, name == "id"
	}
	var query []Filter
	for pair := range strings.SplitSeq(template, ",") {
		key, text, _ := strings.Cut(pair, "=")
		text, _ = expr.ParseTemplate(strings.TrimSpace(text)).Render(lookup, expr.RenderOptions{})
		query = append(query, Filter{Key: strings.TrimSpace(key), Value: text})
	}
	return query
}

// fieldOf returns the value of a field of an item. A name with a dot or a
// bracket is a path into the item.
func fieldOf(item *apikit.Object, field string) any {
	if !strings.ContainsAny(field, ".[") {
		value, _ := item.Get(field)
		return value
	}
	path, err := expr.ParsePath(field)
	if err != nil {
		return nil
	}
	return path.Get(item, definition.ExtraKey)
}
