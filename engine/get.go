package engine

import (
	"context"
	"slices"
	"strconv"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// GetRequest says what [Engine.Get] fetches.
type GetRequest struct {
	// Ref names the resource, and the item or the sub-resource of an item
	// if the request is about one. The ID may be an alias.
	Ref
	// Filters are sent as query parameters. A value that a filter rule of
	// the definition is for is resolved to an ID first.
	Filters []Filter
	// Limit is the number of items to ask for at once. Nil means the page
	// size of the definition.
	Limit *int
	// Page is the number of the page to ask for. Nil asks for none. It
	// cannot be combined with Skip.
	Page *int
	// Skip is the number of items to skip. Nil skips none.
	Skip *int
	// All fetches every page of a listing, Limit items at a time, and
	// leaves Skip aside. It means nothing for a single item.
	All bool
}

// Result is what [Engine.Get] fetched.
type Result struct {
	// Resource is the resource whose definition describes Value: the
	// sub-resource if the request named one, else the resource.
	Resource string
	// Value is what the API answered: a list for a listing, and usually an
	// object for one item.
	Value any
}

// Items returns the value as a list: a list as it is, nil as no items, and
// anything else as one item.
func (r *Result) Items() []any {
	switch value := r.Value.(type) {
	case nil:
		return nil
	case []any:
		return value
	}
	return []any{r.Value}
}

// Get fetches what r names: an item, the listing of a resource, or the
// sub-resource of an item.
//
// A request that cannot be made is an error of kind [apikit.KindUsage]: one
// with Page and Skip, one for a sub-resource without the ID of its item,
// and a listing without the filters its resource requires. The listing of
// a resource that cannot be listed is refused as well. An ID that names no
// item is an error of kind [apikit.KindNotFound].
func (e *Engine) Get(ctx context.Context, r GetRequest) (*Result, error) {
	listing := r.ID == "" || r.Sub != ""
	described := r.Resource
	if r.Sub != "" {
		described = r.Sub
	}
	switch {
	case r.Page != nil && r.Skip != nil:
		return nil, apikit.Errorf(apikit.KindUsage, "page and skip cannot be used together")
	case r.ID == "" && r.Sub != "":
		return nil, apikit.Errorf(apikit.KindUsage, "%s are listed for one %s, whose ID is missing",
			r.Sub, e.doc.Resource(r.Resource).Singular)
	case r.ID == "" && !e.doc.Resource(r.Resource).SupportsListing:
		return nil, apikit.Errorf(apikit.KindFailed, "%s cannot be listed directly", r.Resource)
	}
	if listing {
		var missing []string
		for _, key := range e.doc.Resource(described).RequiredFilters {
			if !slices.ContainsFunc(r.Filters, func(filter Filter) bool { return filter.Key == key }) {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			return nil, apikit.Errorf(apikit.KindUsage, "%s requires filter(s): %s", described, strings.Join(missing, ", "))
		}
	}

	query, err := e.filterIDs(ctx, r.Filters)
	if err != nil {
		return nil, err
	}
	pagination := e.doc.Pagination
	limit := pagination.PageSize
	if r.Limit != nil {
		limit = *r.Limit
	}
	if listing {
		query = upsert(query, pagination.LimitParam, strconv.Itoa(limit))
	}
	if r.Page != nil {
		query = upsert(query, pagination.PageParam, strconv.Itoa(*r.Page))
	}
	if r.Skip != nil {
		query = upsert(query, pagination.SkipParam, strconv.Itoa(*r.Skip))
	}

	var value any
	if r.All && listing {
		value, err = e.fetchAll(ctx, r.Ref, query, limit)
	} else {
		e.fetched(0)
		value, err = e.fetch(ctx, r.Ref, query)
	}
	if err != nil {
		return nil, err
	}
	if r.ID != "" && value == nil {
		return nil, e.notFound(r.Resource, r.ID)
	}
	return &Result{Resource: described, Value: value}, nil
}

// Item fetches one item of a resource. The ID may be an alias. An API that
// answers with a list of one item means that item. An ID that names no
// item, and an answer that is no item, is an error of kind
// [apikit.KindNotFound].
func (e *Engine) Item(ctx context.Context, resource, id string) (*apikit.Object, error) {
	if id == "" {
		return nil, e.notFound(resource, id)
	}
	value, err := e.fetch(ctx, Ref{Resource: resource, ID: id}, nil)
	if err != nil {
		return nil, err
	}
	if list, ok := value.([]any); ok && len(list) == 1 {
		value = list[0]
	}
	item, ok := value.(*apikit.Object)
	if !ok {
		return nil, e.notFound(resource, id)
	}
	return item, nil
}

// filterIDs returns the filters with every value that names an item
// replaced by the ID of that item. The filter rules of the definition say
// which values do: the first rule of a filter that is for the value names
// the resource to look the value up in, as [Engine.ResolveID] does, which
// leaves an ID as it is. An empty value, and one that no rule is for, stay
// as they are, too.
func (e *Engine) filterIDs(ctx context.Context, filters []Filter) ([]Filter, error) {
	resolved := slices.Clone(filters)
	for i, filter := range resolved {
		if filter.Value == "" {
			continue
		}
		for _, rules := range e.doc.FilterIDs {
			if rules.Filter != filter.Key {
				continue
			}
			for _, rule := range rules.Rules {
				if rule.Match != nil && !rule.Match.MatchString(filter.Value) {
					continue
				}
				id, err := e.ResolveID(ctx, rule.Resource, filter.Value)
				if err != nil {
					return nil, err
				}
				resolved[i].Value = id
				break
			}
		}
	}
	return resolved, nil
}
