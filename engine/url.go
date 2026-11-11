package engine

import (
	"cmp"
	"net/url"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// Ref names what a request is about: a resource, one of its items, or a
// sub-resource of one of its items.
type Ref struct {
	// Resource is the name of the resource.
	Resource string
	// ID is the ID of an item, or another value that names one, such as an
	// alias. Empty means the resource as a whole.
	ID string
	// Sub is the name of a sub-resource of the item. Empty means the item
	// itself.
	Sub string
}

// Filter is one query parameter of a request.
type Filter struct {
	// Key is the name of the parameter.
	Key string
	// Value is its value, which may be empty.
	Value string
}

// ParseFilters parses filters as they are written on a command line. Each
// argument holds one or more pairs separated by commas, as in
// "species=cat,ownerId=4711". A pair is split at its first equals sign, so
// a value may contain more of them; a pair without one is a key with an
// empty value. White space around keys and values is dropped. A pair
// without a key is an error of kind [apikit.KindUsage].
func ParseFilters(args []string) ([]Filter, error) {
	var filters []Filter
	for _, arg := range args {
		for pair := range strings.SplitSeq(arg, ",") {
			key, value, _ := strings.Cut(pair, "=")
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if key == "" {
				return nil, apikit.Errorf(apikit.KindUsage, "filter %q has no key, expected key=value", strings.TrimSpace(pair))
			}
			filters = append(filters, Filter{Key: key, Value: value})
		}
	}
	return filters, nil
}

// upsert returns query with key set to value: the first parameter of that
// name gets the value, further ones are dropped, and a new one goes to the
// end. query itself is not changed.
func upsert(query []Filter, key, value string) []Filter {
	updated := make([]Filter, 0, len(query)+1)
	replaced := false
	for _, filter := range query {
		switch {
		case filter.Key != key:
			updated = append(updated, filter)
		case !replaced:
			updated = append(updated, Filter{Key: key, Value: value})
			replaced = true
		}
	}
	if !replaced {
		updated = append(updated, Filter{Key: key, Value: value})
	}
	return updated
}

// encodeQuery returns query as the query of a URL, with its question mark,
// and nothing for an empty query. The parameters are sorted by key, then
// value: their order means nothing to an API, and requests that differ in
// nothing else then share one URL, which is what answers are remembered
// by. Keys and values are escaped as HTML forms are, a space as "+".
func encodeQuery(query []Filter) string {
	if len(query) == 0 {
		return ""
	}
	sorted := slices.Clone(query)
	slices.SortStableFunc(sorted, func(a, b Filter) int {
		return cmp.Or(strings.Compare(a.Key, b.Key), strings.Compare(a.Value, b.Value))
	})
	var b strings.Builder
	for i, filter := range sorted {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(filter.Key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(filter.Value))
	}
	return b.String()
}

// apiName names the API a request goes to: the one the Engine is forced
// to, else the one the request names itself, else that of the sub-resource,
// else that of the resource, else the default for requests with or without
// an ID.
func (e *Engine) apiName(explicit string, ref Ref) string {
	switch {
	case e.forceAPI != "":
		return e.forceAPI
	case explicit != "":
		return explicit
	}
	if ref.Sub != "" {
		if api := e.doc.Resource(ref.Sub).API; api != "" {
			return api
		}
	}
	if api := e.doc.Resource(ref.Resource).API; api != "" {
		return api
	}
	if ref.ID != "" {
		return e.doc.Defaults.ItemAPI
	}
	return e.doc.Defaults.API
}

// base returns the base URL of the API a request goes to, without a
// trailing slash. api is the API the request names itself, or empty.
func (e *Engine) base(api string, ref Ref) (string, error) {
	name := e.apiName(api, ref)
	base, ok := e.doc.API(e.environment, name)
	if !ok {
		return "", apikit.Errorf(apikit.KindDefinition, "the definition has no API %q to reach %s", name, ref.Resource)
	}
	return base, nil
}

// url returns the URL of what ref names: the base URL of its API, the path
// of the resource, the ID, the path of the sub-resource and the query. The
// ID is escaped as one path segment, so that no value given as an ID can
// lead to another path or add to the query. An ID of dots, which is a path
// of its own even when escaped, names no item.
func (e *Engine) url(api string, ref Ref, query []Filter) (string, error) {
	base, err := e.base(api, ref)
	if err != nil {
		return "", err
	}
	if ref.ID == "." || ref.ID == ".." {
		return "", e.notFound(ref.Resource, ref.ID)
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteByte('/')
	b.WriteString(e.doc.Resource(ref.Resource).APIResource)
	if ref.ID != "" {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(ref.ID))
	}
	if ref.Sub != "" {
		b.WriteByte('/')
		b.WriteString(e.doc.Resource(ref.Sub).APIResource)
	}
	b.WriteString(encodeQuery(query))
	return b.String(), nil
}
