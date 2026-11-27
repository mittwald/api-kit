package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"

	apikit "github.com/mittwald/api-kit"
)

// fetch gets what ref names: an item, the listing of a resource, or the
// sub-resource of an item. The ID of ref may be an alias. One that names
// no item is an error of kind [apikit.KindNotFound].
func (e *Engine) fetch(ctx context.Context, ref Ref, query []Filter) (any, error) {
	ref, err := e.resolved(ctx, ref)
	if err != nil {
		return nil, err
	}
	return e.get(ctx, ref, query)
}

// fetchAll gets every page of what ref names, as [Engine.getAll] does. The
// ID of ref may be an alias, as for [Engine.fetch].
func (e *Engine) fetchAll(ctx context.Context, ref Ref, query []Filter, pageSize int) (any, error) {
	ref, err := e.resolved(ctx, ref)
	if err != nil {
		return nil, err
	}
	return e.getAll(ctx, ref, query, pageSize)
}

// resolved returns ref with the ID that its ID names through the aliases
// of the resource.
func (e *Engine) resolved(ctx context.Context, ref Ref) (Ref, error) {
	if ref.ID == "" {
		return ref, nil
	}
	id, err := e.alias(ctx, ref.Resource, ref.ID, false)
	if err != nil {
		return ref, err
	}
	if id == "" {
		return ref, e.notFound(ref.Resource, ref.ID)
	}
	ref.ID = id
	return ref, nil
}

// get sends the GET for what ref names. The ID of ref is taken as it is.
// get fetches one page of what ref names: an item, or a listing put in the
// order of its resource.
func (e *Engine) get(ctx context.Context, ref Ref, query []Filter) (any, error) {
	// A listing that is sorted or numbered here is fetched whole, wherever
	// it is needed.
	if listsItems(ref) && e.pagination() && e.arranges(described(ref)) {
		return e.getAll(ctx, ref, query, e.doc.Pagination.PageSize)
	}
	value, err := e.page(ctx, ref, query)
	if err != nil {
		return nil, err
	}
	// Included fields come first: the listing may be sorted by one.
	if value, err = e.include(ctx, described(ref), value); err != nil || !listsItems(ref) {
		return value, err
	}
	return e.arrange(described(ref), value), nil
}

// page fetches what ref names as the API answers it, taken out of the
// field of the response that holds it.
func (e *Engine) page(ctx context.Context, ref Ref, query []Filter) (any, error) {
	resource := e.doc.Resource(described(ref))
	if listsItems(ref) {
		for _, fixed := range resource.Query {
			if !slices.ContainsFunc(query, func(filter Filter) bool { return filter.Key == fixed.Key }) {
				query = append(query, Filter{Key: fixed.Key, Value: fixed.Value})
			}
		}
	}
	target, err := e.url("", ref, query)
	if err != nil {
		return nil, err
	}
	header, err := e.headers(ctx, ref)
	if err != nil {
		return nil, err
	}
	value, err := e.request(ctx, call{method: http.MethodGet, url: target, header: header, resource: ref.Resource, id: ref.ID})
	if err != nil {
		return nil, err
	}
	return unwrap(value, resource.ResponseField), nil
}

// pagination reports whether the API pages its listings.
func (e *Engine) pagination() bool { return e.doc.Pagination.LimitParam != "" }

// arranges reports whether listings of the resource are sorted or numbered
// here.
func (e *Engine) arranges(resource string) bool {
	r := e.doc.Resource(resource)
	return len(r.Sort) > 0 || r.Number != ""
}

// listsItems reports whether ref names a listing: of a resource, or of the
// sub-resource of an item.
func listsItems(ref Ref) bool { return ref.ID == "" || ref.Sub != "" }

// described returns the resource whose definition describes what ref
// names.
func described(ref Ref) string {
	if ref.Sub != "" {
		return ref.Sub
	}
	return ref.Resource
}

// unwrap returns the value under field of an object the API wrapped its
// answer in. A value without that field is returned as it is.
func unwrap(value any, field string) any {
	if field == "" {
		return value
	}
	object, ok := value.(*apikit.Object)
	if !ok {
		return value
	}
	if inner, ok := object.Get(field); ok {
		return inner
	}
	return value
}

// arrange puts a complete listing in the order the resource asks for and
// numbers its items. Anything but a list is returned as it is.
func (e *Engine) arrange(resource string, value any) any {
	items, ok := value.([]any)
	r := e.doc.Resource(resource)
	if !ok || len(r.Sort) == 0 && r.Number == "" {
		return value
	}
	items = slices.Clone(items)
	if len(r.Sort) > 0 {
		sortItems(items, r.Sort)
	}
	if r.Number != "" {
		for i, item := range items {
			object, ok := item.(*apikit.Object)
			if !ok {
				continue
			}
			// The listing may be cached and shared: the item is copied
			// rather than changed.
			numbered := apikit.NewObject()
			for key, value := range object.All() {
				numbered.Set(key, value)
			}
			numbered.Set(r.Number, json.Number(strconv.Itoa(i+1)))
			items[i] = numbered
		}
	}
	return items
}

// headers returns the headers a request for one item carries. The
// definition names them for the resource, each with a sub-resource of the
// item and the field of it that holds the value. A sub-resource the API
// does not hand out, or one that holds nothing, leaves its header out.
func (e *Engine) headers(ctx context.Context, ref Ref) (http.Header, error) {
	if ref.ID == "" || ref.Sub != "" {
		return nil, nil
	}
	var header http.Header
	for _, rule := range e.doc.Resource(ref.Resource).Headers {
		source, err := e.get(ctx, Ref{Resource: ref.Resource, ID: ref.ID, Sub: rule.Resource}, nil)
		if err != nil {
			if _, refused := errors.AsType[*HTTPError](err); refused {
				continue
			}
			return nil, err
		}
		object, ok := source.(*apikit.Object)
		if !ok || apikit.Falsy(object) {
			continue
		}
		value, _ := object.Get(rule.Column)
		if header == nil {
			header = http.Header{}
		}
		header.Set(rule.Name, apikit.Text(value))
	}
	return header, nil
}

// page is one page of a listing that was fetched ahead.
type page struct {
	skip  int
	value any
	err   error
}

// getAll gets every page of the listing ref names and returns the items of
// all pages as one list. The ID of ref is taken as it is. Each page is
// asked for with pageSize as its limit, whatever query says.
//
// The listing ends with the first page that is empty or shorter than
// pageSize. To get there sooner, pages are fetched ahead, as many at the
// same time as the definition allows; those beyond the end are dropped. A
// page that is no list is not part of a listing: it is the result as it
// is. An API that answers the first request with something other than a
// list is asked no further.
func (e *Engine) getAll(ctx context.Context, ref Ref, query []Filter, pageSize int) (any, error) {
	pagination := e.doc.Pagination
	// An API that takes no page size answers with every item at once.
	if pagination.LimitParam == "" {
		return e.get(ctx, ref, query)
	}
	query = upsert(query, pagination.LimitParam, strconv.Itoa(pageSize))
	fetchPage := func(ctx context.Context, skip int) (any, error) {
		return e.page(ctx, ref, upsert(query, pagination.SkipParam, strconv.Itoa(skip)))
	}
	items, err := e.pages(ctx, fetchPage, pageSize)
	if err != nil {
		return nil, err
	}
	if items, err = e.include(ctx, described(ref), items); err != nil {
		return nil, err
	}
	return e.arrange(described(ref), items), nil
}

// pages fetches the pages of a listing with fetchPage until one comes back
// short, several at a time, and returns their items in order.
func (e *Engine) pages(ctx context.Context, fetchPage func(context.Context, int) (any, error), pageSize int) (any, error) {

	e.fetched(0)
	first, err := fetchPage(ctx, 0)
	if err != nil {
		return nil, err
	}
	if apikit.Falsy(first) {
		return []any{}, nil
	}
	items, ok := first.([]any)
	if !ok {
		return first, nil
	}
	// Without a page size there is no telling a full page from the last.
	if len(items) < pageSize || pageSize < 1 {
		return items, nil
	}

	// Leaving stops the pages still under way and waits for them, in this
	// order.
	var running sync.WaitGroup
	defer running.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := max(e.doc.HTTP.PaginationMaxWorkers, 1)
	arrived := make(chan page, workers)
	waiting := map[int]page{}
	// next is the page to ask for next, due the one to add to the items
	// next. Pages arrive in any order and are added in theirs.
	next, due := pageSize, pageSize
	underWay, ended := 0, false
	for {
		// Once any page shows that the listing ends, no page after it is
		// of use; all pages before it have been asked for already.
		for ; !ended && underWay < workers; underWay++ {
			skip := next
			next += pageSize
			running.Go(func() {
				value, err := fetchPage(ctx, skip)
				arrived <- page{skip: skip, value: value, err: err}
			})
		}
		// A page is always under way here: the one that is due.
		got := <-arrived
		underWay--
		waiting[got.skip] = got
		ended = ended || got.err != nil || last(got.value, pageSize)

		for {
			current, ok := waiting[due]
			if !ok {
				break
			}
			delete(waiting, due)
			if current.err != nil {
				return nil, current.err
			}
			if apikit.Falsy(current.value) {
				return items, nil
			}
			batch, ok := current.value.([]any)
			if !ok {
				return current.value, nil
			}
			items = append(items, batch...)
			e.fetched(len(items))
			if len(batch) < pageSize {
				return items, nil
			}
			due += pageSize
		}
	}
}

// last reports whether a page ends its listing: it is empty, shorter than
// a full page, or no list at all.
func last(value any, pageSize int) bool {
	batch, ok := value.([]any)
	return !ok || len(batch) < pageSize
}
