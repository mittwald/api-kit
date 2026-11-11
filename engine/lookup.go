package engine

import (
	"context"
	"maps"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// lookupItems returns the items a value of a parameter with a lookup may
// name: the listing the lookup names, its filters applied.
func (r *callRun) lookupItems(ctx context.Context, lookup *definition.Lookup) ([]*apikit.Object, error) {
	query, excluded := splitFilters(lookup.Filters, r.args.scalar)
	ref := Ref{
		Resource: renderWith(lookup.Get, r.args.scalar),
		ID:       renderWith(lookup.ID, r.args.scalar),
		Sub:      lookup.Sub,
	}
	listing, err := r.e.fetchAll(ctx, ref, query, r.e.doc.Pagination.PageSize)
	if err != nil {
		return nil, err
	}
	return withoutExcluded(itemObjects(listing), excluded), nil
}

// resolveLookups replaces the value of every parameter with a lookup by
// what the lookup selects from the one item the value names: an email
// address becomes the ID of its user, say. The value must equal one of the
// fields the lookup matches, whatever its case, in exactly one item. A
// lookup sees what earlier ones resolved.
func (r *callRun) resolveLookups(ctx context.Context, setters []*definition.Setter) error {
	asGiven := maps.Clone(r.args)
	for _, setter := range setters {
		for _, parameter := range setter.Parameters {
			lookup := parameter.Lookup
			value, _ := asGiven.get(parameter.Name)
			text, _ := value.(string)
			if lookup == nil || text == "" {
				continue
			}
			items, err := r.lookupItems(ctx, lookup)
			if err != nil {
				return err
			}
			var selected []string
			for _, item := range items {
				matches := slices.ContainsFunc(lookup.Match, func(field string) bool {
					return strings.EqualFold(apikit.Text(fieldOf(item, field)), text)
				})
				// An item without the field to select cannot be chosen.
				id := apikit.Text(fieldOf(item, lookup.Select))
				if matches && id != "" && !slices.Contains(selected, id) {
					selected = append(selected, id)
				}
			}
			if len(selected) != 1 {
				count, listing := "none", lookup.Sub
				if len(selected) > 1 {
					count = "more than one"
				}
				if listing == "" {
					listing = renderWith(lookup.Get, r.args.scalar)
				}
				return apikit.Errorf(apikit.KindUsage, "--%s %s matches %s of the %s that may be chosen",
					parameter.Name, text, count, listing)
			}
			r.args.set(parameter.Name, selected[0])
		}
	}
	return nil
}

// CompleteLookup returns the values that shell completion offers for a
// parameter whose value is looked up in a listing: the field the lookup
// names for completion, of the items the parameters given so far lead to,
// sorted. params are the parameters given so far, as [CallInput] has them;
// prefix is what was typed of the value.
//
// It returns nothing, and no error, for a parameter without a lookup and
// while a parameter the lookup depends on is not given yet. Completion
// must not disturb the command line, so its caller offers nothing on an
// error.
func (e *Engine) CompleteLookup(ctx context.Context, resource, call, parameter string, params map[string]any, prefix string) ([]string, error) {
	described, ok := e.doc.Call(resource, call)
	if !ok {
		return nil, apikit.Errorf(apikit.KindUsage, "%s is not available for %s", call, resource)
	}
	completed, ok := described.Parameter(parameter)
	if !ok || completed.Lookup == nil {
		return nil, nil
	}
	lookup := completed.Lookup
	args, err := givenParameters(described, params)
	if err != nil {
		return nil, err
	}
	r := e.newCallRun(resource, described, args)
	if err := r.resolveIDs(ctx, described.Setters); err != nil {
		return nil, err
	}
	r.setVariables(described.Setters)

	for _, template := range slices.Concat([]string{lookup.Get, lookup.ID}, lookup.Filters) {
		for _, placeholder := range expr.ParseTemplate(template).Placeholders() {
			if _, known := r.args.scalar(placeholder.Name); placeholder.Braced && !known {
				return nil, nil
			}
		}
	}
	items, err := r.lookupItems(ctx, lookup)
	if err != nil {
		return nil, err
	}
	field := lookup.Complete
	if field == "" {
		field = lookup.Match[0]
	}
	var offered []string
	for _, item := range items {
		value := fieldOf(item, field)
		if text := apikit.Text(value); !apikit.Falsy(value) && strings.HasPrefix(text, prefix) {
			offered = append(offered, text)
		}
	}
	slices.Sort(offered)
	return slices.Compact(offered), nil
}
