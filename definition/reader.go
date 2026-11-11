package definition

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"

	apikit "github.com/mittwald/api-kit"
)

// loader collects the problems found while a document is decoded.
type loader struct {
	source   string
	reserved Reserved
	problems []Diagnostic
}

// problemf records a mistake at path.
func (l *loader) problemf(path, format string, args ...any) {
	l.problems = append(l.problems, Diagnostic{
		Severity: Problem,
		Source:   l.source,
		Path:     path,
		Message:  fmt.Sprintf(format, args...),
	})
}

// mapping reads the keys of one mapping of the document and remembers which
// ones were asked for, so that finish can report the rest as unknown.
type mapping struct {
	l      *loader
	path   string
	object *apikit.Object
	used   map[string]bool
}

// mapping returns a reader for v, which must be a mapping. For anything
// else it records a problem and returns false.
func (l *loader) mapping(path string, v any) (mapping, bool) {
	object, ok := v.(*apikit.Object)
	if !ok {
		l.problemf(path, "must be a mapping, not %s", kindOf(v))
		return mapping{}, false
	}
	return mapping{l: l, path: path, object: object, used: map[string]bool{}}, true
}

// at returns the path of key.
func (m mapping) at(key string) string { return join(m.path, key) }

// get returns the value of key as it is.
func (m mapping) get(key string) (any, bool) {
	m.used[key] = true
	return m.object.Get(key)
}

// has reports whether key is present, without marking it as known.
func (m mapping) has(key string) bool { return m.object.Has(key) }

// finish reports every key that was never asked for.
func (m mapping) finish() {
	for _, key := range m.object.Keys() {
		if !m.used[key] {
			m.l.problemf(m.at(key), "unknown key")
		}
	}
}

// text returns the string under key, or def when the key is absent.
func (m mapping) text(key, def string) string {
	value, ok := m.get(key)
	if !ok {
		return def
	}
	text, ok := value.(string)
	if !ok {
		m.l.problemf(m.at(key), "must be a string, not %s", kindOf(value))
		return def
	}
	return text
}

// flag returns the boolean under key, or def when the key is absent.
func (m mapping) flag(key string, def bool) bool {
	value, ok := m.get(key)
	if !ok {
		return def
	}
	flag, ok := value.(bool)
	if !ok {
		m.l.problemf(m.at(key), "must be true or false, not %s", kindOf(value))
		return def
	}
	return flag
}

// number returns the number under key, which must be at least minimum, or
// def when the key is absent.
func (m mapping) number(key string, def, minimum float64) float64 {
	value, ok := m.get(key)
	if !ok {
		return def
	}
	number, ok := value.(json.Number)
	if !ok {
		m.l.problemf(m.at(key), "must be a number, not %s", kindOf(value))
		return def
	}
	parsed, err := number.Float64()
	if err != nil || math.IsInf(parsed, 0) || parsed < minimum {
		m.l.problemf(m.at(key), "must be a number of at least %s", strconv.FormatFloat(minimum, 'g', -1, 64))
		return def
	}
	return parsed
}

// integer returns the whole number under key, which must be at least
// minimum, or def when the key is absent.
func (m mapping) integer(key string, def, minimum int) int {
	value, ok := m.get(key)
	if !ok {
		return def
	}
	number, ok := value.(json.Number)
	if !ok {
		m.l.problemf(m.at(key), "must be a whole number, not %s", kindOf(value))
		return def
	}
	parsed, err := strconv.Atoi(number.String())
	if err != nil || parsed < minimum {
		m.l.problemf(m.at(key), "must be a whole number of at least %d", minimum)
		return def
	}
	return parsed
}

// items returns the list under key and whether the key holds one. An absent
// key is an empty list.
func (m mapping) items(key string) []any {
	value, ok := m.get(key)
	if !ok {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		m.l.problemf(m.at(key), "must be a list, not %s", kindOf(value))
		return nil
	}
	return list
}

// texts returns the list of strings under key.
func (m mapping) texts(key string) []string {
	return m.l.texts(m.at(key), m.items(key))
}

// texts converts a list to strings, reporting every entry that is none.
func (l *loader) texts(path string, list []any) []string {
	var out []string
	for i, item := range list {
		text, ok := item.(string)
		if !ok {
			l.problemf(index(path, i), "must be a string, not %s", kindOf(item))
			continue
		}
		out = append(out, text)
	}
	return out
}

// integers returns the list of whole numbers under key, or def when the key
// is absent.
func (m mapping) integers(key string, def []int) []int {
	if !m.has(key) {
		return def
	}
	var out []int
	for i, item := range m.items(key) {
		number, ok := item.(json.Number)
		parsed, err := strconv.Atoi(number.String())
		if !ok || err != nil {
			m.l.problemf(index(m.at(key), i), "must be a whole number")
			continue
		}
		out = append(out, parsed)
	}
	return out
}

// child returns a reader for the mapping under key, or false when the key
// is absent or holds something else.
func (m mapping) child(key string) (mapping, bool) {
	value, ok := m.get(key)
	if !ok {
		return mapping{}, false
	}
	return m.l.mapping(m.at(key), value)
}

// pairs returns the mapping of strings under key as pairs in document
// order.
func (m mapping) pairs(key string) []Pair {
	child, ok := m.child(key)
	if !ok {
		return nil
	}
	var out []Pair
	for name, value := range child.object.All() {
		child.used[name] = true
		text, ok := value.(string)
		if !ok {
			m.l.problemf(child.at(name), "must be a string, not %s", kindOf(value))
			continue
		}
		out = append(out, Pair{Key: name, Value: text})
	}
	return out
}

// pattern returns the regular expression under key, or nil when the key is
// absent.
func (m mapping) pattern(key string) *regexp.Regexp {
	if !m.has(key) {
		return nil
	}
	return m.l.pattern(m.at(key), m.text(key, ""))
}

// pattern compiles a regular expression, reporting one that does not
// compile. Definitions use the RE2 syntax of Go, which has no look-around
// and no back-references.
func (l *loader) pattern(path, source string) *regexp.Regexp {
	compiled, err := regexp.Compile(source)
	if err != nil {
		l.problemf(path, "%q is not a regular expression: %v", source, err)
		return nil
	}
	return compiled
}
