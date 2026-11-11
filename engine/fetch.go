package engine

import (
	"context"
	"errors"
	"net/http"
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
func (e *Engine) get(ctx context.Context, ref Ref, query []Filter) (any, error) {
	target, err := e.url("", ref, query)
	if err != nil {
		return nil, err
	}
	header, err := e.headers(ctx, ref)
	if err != nil {
		return nil, err
	}
	return e.request(ctx, call{method: http.MethodGet, url: target, header: header, resource: ref.Resource, id: ref.ID})
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
	query = upsert(query, pagination.LimitParam, strconv.Itoa(pageSize))
	fetchPage := func(ctx context.Context, skip int) (any, error) {
		return e.get(ctx, ref, upsert(query, pagination.SkipParam, strconv.Itoa(skip)))
	}

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
