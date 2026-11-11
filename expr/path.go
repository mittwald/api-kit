package expr

import (
	"errors"
	"fmt"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// A Path selects a value inside a JSON value. It is parsed once by
// [ParsePath]. The zero Path has no segments and selects the value itself.
//
// The grammar is
//
//	path    = segment { "." segment } .
//	segment = field [ "[" key "=" value "]" ] .
//	field   = one or more characters other than ".", "[" and "]" .
//	key     = one or more characters other than "=" and "]" .
//	value   = any number of characters other than "]" .
//
// A dot between brackets belongs to the filter, so items[version=1.2].name
// has two segments. Nothing is trimmed and nothing can be escaped: white
// space is part of the field, key or value it stands in, and a field with a
// dot or a bracket in its name cannot be written.
type Path struct {
	source   string
	segments []segment
}

// A segment is one step of a path: a field, and optionally a filter that
// keeps what has the given value under the given key.
type segment struct {
	field    string
	filtered bool
	key      string
	value    string
}

// ParsePath parses s as a [Path]. It returns an error for an empty path, an
// empty segment (a dot at either end or two dots in a row), a bracket
// without its counterpart, a filter that has no field before it, no = or
// nothing before the =, and for text between a filter and the next dot.
func ParsePath(s string) (Path, error) {
	if s == "" {
		return Path{}, errors.New("empty path")
	}
	path := Path{source: s}
	fail := func(problem string) (Path, error) {
		return Path{}, fmt.Errorf("path %q: %s", s, problem)
	}
	rest := s
	for {
		end := strings.IndexAny(rest, ".[]")
		if end < 0 {
			end = len(rest)
		}
		current := segment{field: rest[:end]}
		rest = rest[end:]

		switch {
		case strings.HasPrefix(rest, "]"):
			return fail("] without [")
		case current.field == "" && strings.HasPrefix(rest, "["):
			return fail("filter without a field before it")
		case current.field == "":
			return fail("empty segment")
		}

		if strings.HasPrefix(rest, "[") {
			closing := strings.IndexByte(rest, ']')
			if closing < 0 {
				return fail("[ without ]")
			}
			filter := rest[1:closing]
			key, value, found := strings.Cut(filter, "=")
			switch {
			case !found:
				return fail("filter [" + filter + "] has no =, expected [key=value]")
			case key == "":
				return fail("filter [" + filter + "] has no key, expected [key=value]")
			}
			current.filtered, current.key, current.value = true, key, value
			rest = rest[closing+1:]
			switch {
			case strings.HasPrefix(rest, "]"):
				return fail("] without [")
			case rest != "" && rest[0] != '.':
				return fail("a filter ends its segment, expected a dot after ]")
			}
		}

		path.segments = append(path.segments, current)
		if rest == "" {
			return path, nil
		}
		rest = rest[1:] // The dot.
	}
}

// String returns the path as written.
func (p Path) String() string { return p.source }

// Get returns the value the path selects in item, nil when there is none.
// item is a value of the types [apikit.Decode] produces. The result is not
// a copy: a list or an object that is found in item is returned as it is.
//
// The segments are applied one after the other, each to the result of the
// one before it:
//
//   - A segment applied to an object yields the value of its field, nil
//     when the object has no such field.
//   - A filter then narrows that value down. Of a list it yields the first
//     element that is an object with the given value under the given key,
//     nil when there is none. An object is treated like a list of that one
//     object. Anything else yields nil. The value under the key is compared
//     as [apikit.Text] shows it, so [id=7] finds the number 7, [done=true]
//     the boolean, and [note=] an element whose note is empty, null or
//     absent.
//   - A segment applied to a list is applied to each element that is an
//     object. The result is a list of the same length, with nil in the
//     place of every other element.
//   - A segment applied to anything else, a string or a number in the
//     middle of a path for example, yields nil.
//
// extraKey names a second place to look for the first segment: when item is
// an object that lacks the first field, but has an object under extraKey
// that has it, the path is applied to that object instead. An empty
// extraKey switches this off.
func (p Path) Get(item any, extraKey string) any {
	value := item
	for i, current := range p.segments {
		if i == 0 && extraKey != "" {
			value = fallback(value, current.field, extraKey)
		}
		value = current.apply(value)
	}
	return value
}

// fallback returns the object under extraKey when item is an object that
// lacks field and that object has it, and item otherwise.
func fallback(item any, field, extraKey string) any {
	object, ok := item.(*apikit.Object)
	if !ok || object.Has(field) {
		return item
	}
	inner, _ := object.Get(extraKey)
	if extra, ok := inner.(*apikit.Object); ok && extra.Has(field) {
		return extra
	}
	return item
}

// apply returns what the segment selects in value.
func (s segment) apply(value any) any {
	switch value := value.(type) {
	case *apikit.Object:
		return s.of(value)
	case []any:
		selected := make([]any, len(value))
		for i, element := range value {
			if object, ok := element.(*apikit.Object); ok {
				selected[i] = s.of(object)
			}
		}
		return selected
	}
	return nil
}

// of returns what the segment selects in an object.
func (s segment) of(object *apikit.Object) any {
	value, _ := object.Get(s.field)
	if !s.filtered {
		return value
	}
	switch value := value.(type) {
	case []any:
		for _, element := range value {
			if s.matches(element) {
				return element
			}
		}
	case *apikit.Object:
		if s.matches(value) {
			return value
		}
	}
	return nil
}

// matches reports whether element passes the filter of the segment.
func (s segment) matches(element any) bool {
	object, ok := element.(*apikit.Object)
	if !ok {
		return false
	}
	value, _ := object.Get(s.key)
	return apikit.Text(value) == s.value
}
