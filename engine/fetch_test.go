package engine_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

func TestHeadersFromASubResource(t *testing.T) {
	const order = "0f0f0f0f"
	orderURL := mainAPI + "/orders/" + order
	text := func(s string) *string { return &s }

	tests := map[string]struct {
		access apitest.Response
		// header is the value the request for the order carries, nil for
		// a request without the header.
		header *string
	}{
		"the field of the sub-resource":         {apitest.Response{Body: map[string]any{"key": "k-7", "other": "x"}}, text("k-7")},
		"a number as it is spelled":             {apitest.Response{Raw: `{"key": 7.50}`}, text("7.50")},
		"a sub-resource without the field":      {apitest.Response{Body: map[string]any{"other": "x"}}, text("")},
		"a sub-resource that holds nothing":     {apitest.Response{Body: map[string]any{}}, nil},
		"no sub-resource":                       {apitest.Response{Status: 204}, nil},
		"a sub-resource that is a list":         {apitest.Response{Body: []any{map[string]any{"key": "k-7"}}}, nil},
		"a sub-resource the API keeps":          {apitest.Response{Status: 403, Raw: "not yours"}, nil},
		"a sub-resource the API does not know":  {apitest.Response{Status: 404}, nil},
		"a sub-resource the API fails to fetch": {apitest.Response{Status: 500}, nil},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			shop.api.Handle("GET", "/v1/orders/"+order+"/access", test.access)
			shop.api.Handle("GET", "/v1/orders/"+order, apitest.Response{Body: map[string]any{"id": order}})

			got, err := shop.Fetch(t.Context(), engine.Ref{Resource: "orders", ID: order}, nil)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			wantJSON(t, got, `{"id":"`+order+`"}`)
			wantSent(t, shop.api, "GET "+orderURL+"/access", "GET "+orderURL)

			values, sent := shop.api.Requests()[1].Header["X-Order-Key"]
			switch {
			case test.header == nil && sent:
				t.Errorf("the request carries X-Order-Key %q, want none", values)
			case test.header != nil && !slices.Equal(values, []string{*test.header}):
				t.Errorf("the request carries X-Order-Key %q, want %q", values, *test.header)
			}
		})
	}

	t.Run("only a request for one item carries it", func(t *testing.T) {
		shop := open(t, nil)
		shop.api.Handle("GET", "/v1/orders", apitest.Response{Body: []any{}})
		shop.api.Handle("GET", "/v1/orders/"+order+"/access", apitest.Response{Body: map[string]any{"key": "k-7"}})
		for _, ref := range []engine.Ref{{Resource: "orders"}, {Resource: "orders", ID: order, Sub: "access"}} {
			if _, err := shop.Fetch(t.Context(), ref, nil); err != nil {
				t.Fatalf("Fetch(%+v): %v", ref, err)
			}
		}
		wantSent(t, shop.api, "GET "+adminAPI+"/orders", "GET "+orderURL+"/access")
		for _, request := range shop.api.Requests() {
			if _, sent := request.Header["X-Order-Key"]; sent {
				t.Errorf("%s %s carries the header", request.Method, request.Path)
			}
		}
	})

	t.Run("the sub-resource is fetched once", func(t *testing.T) {
		shop := open(t, nil)
		shop.api.Handle("GET", "/v1/orders/"+order+"/access", apitest.Response{Body: map[string]any{"key": "k-7"}})
		shop.api.Handle("GET", "/v1/orders/"+order, apitest.Response{Body: map[string]any{"id": order}})
		shop.api.Handle("GET", "/v1/orders/"+order+"?expand=items", apitest.Response{Body: map[string]any{"id": order}})
		for _, query := range [][]engine.Filter{nil, {{Key: "expand", Value: "items"}}} {
			if _, err := shop.Fetch(t.Context(), engine.Ref{Resource: "orders", ID: order}, query); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
		}
		wantSent(t, shop.api, "GET "+orderURL+"/access", "GET "+orderURL, "GET "+orderURL+"?expand=items")
		if got := shop.api.Requests()[2].Header.Get("X-Order-Key"); got != "k-7" {
			t.Errorf("the second request carries X-Order-Key %q, want k-7", got)
		}
	})

	t.Run("a sub-resource that cannot be reached stops the request", func(t *testing.T) {
		shop := open(t, func(doc *definition.Document, _ *engine.Options) { doc.HTTP.Retries = 0 })
		shop.api.Handle("GET", "/v1/orders/"+order+"/access", apitest.Response{Err: errors.New("connection refused")})
		_, err := shop.Fetch(t.Context(), engine.Ref{Resource: "orders", ID: order}, nil)
		wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch orders", "connection refused")
		wantSent(t, shop.api, "GET "+orderURL+"/access")
	})
}

// scriptPages scripts the pages of the listing of all pets, pageSize items
// at a time, in their order.
func scriptPages(api *apitest.Server, pageSize int, pages ...apitest.Response) {
	for i, page := range pages {
		api.Handle("GET", fmt.Sprintf("/v1/pets?limit=%d&skip=%d", pageSize, i*pageSize), page)
	}
}

// pageOf returns a page of pets with the given IDs.
func pageOf(ids ...string) apitest.Response {
	return apitest.Response{Body: list(ids...)}
}

func TestFetchAllOnTheFirstPage(t *testing.T) {
	tests := map[string]struct {
		first apitest.Response
		want  string
	}{
		// Whatever counts as nothing is an empty listing.
		"no content":      {apitest.Response{Status: 204}, `[]`},
		"null":            {apitest.Response{Raw: `null`}, `[]`},
		"an empty list":   {apitest.Response{Raw: `[]`}, `[]`},
		"an empty object": {apitest.Response{Raw: `{}`}, `[]`},
		"an empty string": {apitest.Response{Raw: `""`}, `[]`},
		"zero":            {apitest.Response{Raw: `0.0`}, `[]`},
		"false":           {apitest.Response{Raw: `false`}, `[]`},
		// Anything else that is no list is the result as it is.
		"an object": {apitest.Response{Raw: `{"id":"a","pets":[]}`}, `{"id":"a","pets":[]}`},
		"a string":  {apitest.Response{Raw: `"closed"`}, `"closed"`},
		"a number":  {apitest.Response{Raw: `7`}, `7`},
		// A page that is not full is the last one.
		"a short page": {pageOf("a", "b"), `[{"id":"a"},{"id":"b"}]`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			scriptPages(shop.api, 3, test.first)
			got, err := shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 3)
			if err != nil {
				t.Fatalf("FetchAll: %v", err)
			}
			wantJSON(t, got, test.want)
			wantSent(t, shop.api, "GET "+adminAPI+"/pets?limit=3&skip=0")
		})
	}
}

func TestFetchAllPutsThePagesTogether(t *testing.T) {
	none := apitest.Response{Body: []any{}}
	tests := map[string]struct {
		pages []apitest.Response
		want  string
	}{
		// Eight pages are fetched ahead: those after the short one are
		// dropped, whatever they hold.
		"up to the first short page": {
			[]apitest.Response{pageOf("1", "2"), pageOf("3", "4"), pageOf("5"), none, pageOf("ignored"), pageOf("ignored", "too"), none, none, none, none, none},
			`[{"id":"1"},{"id":"2"},{"id":"3"},{"id":"4"},{"id":"5"}]`,
		},
		"up to the first empty page": {
			[]apitest.Response{pageOf("1", "2"), pageOf("3", "4"), none, pageOf("ignored", "too"), none, none, none, none, none, none, none},
			`[{"id":"1"},{"id":"2"},{"id":"3"},{"id":"4"}]`,
		},
		// What counts as nothing ends the listing as an empty list does.
		"up to a page that holds nothing": {
			[]apitest.Response{pageOf("1", "2"), {Raw: `{}`}, none, none, none, none, none, none, none},
			`[{"id":"1"},{"id":"2"}]`,
		},
		"up to a page without content": {
			[]apitest.Response{pageOf("1", "2"), {Status: 204}, none, none, none, none, none, none, none},
			`[{"id":"1"},{"id":"2"}]`,
		},
		// A page that is no list is no part of a listing.
		"a page that is no list is the result": {
			[]apitest.Response{pageOf("1", "2"), pageOf("3", "4"), {Raw: `{"complete":false}`}, none, none, none, none, none, none, none, none},
			`{"complete":false}`,
		},
		"many full pages": {
			[]apitest.Response{
				pageOf("1", "2"), pageOf("3", "4"), pageOf("5", "6"), pageOf("7", "8"), pageOf("9", "10"), pageOf("11", "12"),
				pageOf("13", "14"), pageOf("15", "16"), pageOf("17", "18"), pageOf("19", "20"), pageOf("21", "22"), pageOf("23"),
				none, none, none, none, none, none, none, none,
			},
			`[{"id":"1"},{"id":"2"},{"id":"3"},{"id":"4"},{"id":"5"},{"id":"6"},{"id":"7"},{"id":"8"},{"id":"9"},{"id":"10"},{"id":"11"},{"id":"12"},` +
				`{"id":"13"},{"id":"14"},{"id":"15"},{"id":"16"},{"id":"17"},{"id":"18"},{"id":"19"},{"id":"20"},{"id":"21"},{"id":"22"},{"id":"23"}]`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			scriptPages(shop.api, 2, test.pages...)
			got, err := shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 2)
			if err != nil {
				t.Fatalf("FetchAll: %v", err)
			}
			wantJSON(t, got, test.want)
			if first := sent(shop.api)[0]; first != "GET "+adminAPI+"/pets?limit=2&skip=0" {
				t.Errorf("the first request was %s", first)
			}
		})
	}
}

func TestFetchAllFails(t *testing.T) {
	none := apitest.Response{Body: []any{}}
	shop := open(t, nil)
	scriptPages(shop.api, 2, pageOf("1", "2"), apitest.Response{Status: 500, Raw: "boom"}, none, none, none, none, none, none, none)

	_, err := shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 2)
	wantError(t, err, apikit.KindFailed, "failed to fetch pets: 500 boom")

	t.Run("on the first page", func(t *testing.T) {
		shop := open(t, nil)
		scriptPages(shop.api, 2, apitest.Response{Status: 404})
		_, err := shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 2)
		wantError(t, err, apikit.KindNotFound, "failed to fetch pets: 404")
		wantSent(t, shop.api, "GET "+adminAPI+"/pets?limit=2&skip=0")
	})
}

func TestFetchAllAsksForItsPages(t *testing.T) {
	const visits = "/pets/" + rex + "/visits"
	tests := map[string]struct {
		change   func(doc *definition.Document, o *engine.Options)
		ref      engine.Ref
		query    []engine.Filter
		pageSize int
		handle   string
		want     string
	}{
		"the limit and the skip of the caller give way": {
			ref:      engine.Ref{Resource: "pets"},
			query:    []engine.Filter{{Key: "species", Value: "cat"}, {Key: "limit", Value: "50"}, {Key: "skip", Value: "7"}, {Key: "page", Value: "3"}},
			pageSize: 2, handle: "/v1/pets?species=cat&limit=2&skip=0&page=3",
			want: "GET " + adminAPI + "/pets?limit=2&page=3&skip=0&species=cat",
		},
		"the names of the parameters come from the definition": {
			change: func(doc *definition.Document, _ *engine.Options) {
				doc.Pagination.LimitParam, doc.Pagination.SkipParam = "per_page", "offset"
			},
			ref:      engine.Ref{Resource: "pets"},
			query:    []engine.Filter{{Key: "limit", Value: "50"}},
			pageSize: 2, handle: "/v1/pets?limit=50&per_page=2&offset=0",
			want: "GET " + adminAPI + "/pets?limit=50&offset=0&per_page=2",
		},
		"a sub-resource is listed for its item": {
			ref:      engine.Ref{Resource: "pets", ID: rex, Sub: "visits"},
			pageSize: 5, handle: "/v1" + visits + "?limit=5&skip=0",
			want: "GET " + adminAPI + visits + "?limit=5&skip=0",
		},
		// Without a page size, a page cannot be told from the last one.
		"no page size, no further pages": {
			ref:      engine.Ref{Resource: "pets"},
			pageSize: 0, handle: "/v1/pets?limit=0&skip=0",
			want: "GET " + adminAPI + "/pets?limit=0&skip=0",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, test.change)
			shop.api.Handle("GET", test.handle, pageOf("a"))
			got, err := shop.FetchAll(t.Context(), test.ref, test.query, test.pageSize)
			if err != nil {
				t.Fatalf("FetchAll: %v", err)
			}
			wantJSON(t, got, `[{"id":"a"}]`)
			wantSent(t, shop.api, test.want)
		})
	}
}

// heldPages scripts the listing of all pets, two at a time. Every page
// after the first is held back until its channel is closed.
func heldPages(api *apitest.Server, pages ...apitest.Response) []chan struct{} {
	gates := make([]chan struct{}, len(pages))
	for i := range pages {
		if i > 0 {
			gates[i] = make(chan struct{})
			pages[i].Until = gates[i]
		}
	}
	scriptPages(api, 2, pages...)
	return gates
}

// twoAhead limits the pages fetched ahead to two.
func twoAhead(doc *definition.Document, _ *engine.Options) {
	doc.HTTP.PaginationMaxWorkers = 2
}

func TestFetchAllFetchesAhead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reports []engine.Progress
		shop := open(t, func(doc *definition.Document, o *engine.Options) {
			twoAhead(doc, o)
			o.Progress = func(p engine.Progress) { reports = append(reports, p) }
		})
		gates := heldPages(shop.api, pageOf("1", "2"), pageOf("3", "4"), pageOf("5", "6"), pageOf("7"), pageOf())
		page := func(i int) string {
			return fmt.Sprintf("GET %s/pets?limit=2&skip=%d", adminAPI, 2*i)
		}

		var got any
		var err error
		var done sync.WaitGroup
		done.Go(func() { got, err = shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 2) })

		// asked checks which pages were asked for. Those that are asked
		// for at the same time arrive in any order.
		asked := func(want ...string) {
			t.Helper()
			got := sent(shop.api)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("pages asked for: %v, want %v", got, want)
			}
		}

		// The first page is full, so two more are asked for, and no third.
		synctest.Wait()
		asked(page(0), page(1), page(2))

		// The later one arrives first. It is full, so the next is due.
		close(gates[2])
		synctest.Wait()
		asked(page(0), page(1), page(2), page(3))

		// That one is short: no page after it is of use.
		close(gates[3])
		synctest.Wait()
		asked(page(0), page(1), page(2), page(3))

		close(gates[1])
		done.Wait()
		if err != nil {
			t.Fatalf("FetchAll: %v", err)
		}
		// The pages are put together in their order, not in that of their
		// arrival.
		if got := ids(t, got); !slices.Equal(got, []string{"1", "2", "3", "4", "5", "6", "7"}) {
			t.Errorf("FetchAll returned the pets %v", got)
		}
		asked(page(0), page(1), page(2), page(3))

		want := []engine.Progress{
			{Stage: engine.StageFetch, Done: 0},
			{Stage: engine.StageFetch, Done: 4},
			{Stage: engine.StageFetch, Done: 6},
			{Stage: engine.StageFetch, Done: 7},
		}
		if !slices.Equal(reports, want) {
			t.Errorf("progress: %+v, want %+v", reports, want)
		}
	})
}

func TestFetchAllDecidesInTheOrderOfThePages(t *testing.T) {
	t.Run("a failure beyond the end does not count", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			shop := open(t, twoAhead)
			gates := heldPages(shop.api, pageOf("1", "2"), pageOf("3"), apitest.Response{Status: 400, Raw: "skip is out of range"})
			var got any
			var err error
			var done sync.WaitGroup
			done.Go(func() { got, err = shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 2) })

			close(gates[2])
			synctest.Wait()
			close(gates[1])
			done.Wait()
			if err != nil {
				t.Fatalf("FetchAll: %v", err)
			}
			if got := ids(t, got); !slices.Equal(got, []string{"1", "2", "3"}) {
				t.Errorf("FetchAll returned the pets %v", got)
			}
		})
	})

	t.Run("a failure does not wait for the pages after it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			shop := open(t, twoAhead)
			gates := heldPages(shop.api, pageOf("1", "2"), apitest.Response{Status: 500, Raw: "boom"}, pageOf("5", "6"))
			var err error
			var done sync.WaitGroup
			done.Go(func() { _, err = shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 2) })

			// The page after the failing one never arrives.
			close(gates[1])
			done.Wait()
			wantError(t, err, apikit.KindFailed, "failed to fetch pets: 500 boom")
		})
	})

	t.Run("nor does the end of the listing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			shop := open(t, twoAhead)
			gates := heldPages(shop.api, pageOf("1", "2"), pageOf("3"), pageOf("ignored"))
			var got any
			var err error
			var done sync.WaitGroup
			done.Go(func() { got, err = shop.FetchAll(t.Context(), engine.Ref{Resource: "pets"}, nil, 2) })

			close(gates[1])
			done.Wait()
			if err != nil {
				t.Fatalf("FetchAll: %v", err)
			}
			if got := ids(t, got); !slices.Equal(got, []string{"1", "2", "3"}) {
				t.Errorf("FetchAll returned the pets %v", got)
			}
		})
	})
}

func TestFetchAllStopsWhenTheRunEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shop := open(t, twoAhead)
		heldPages(shop.api, pageOf("1", "2"), pageOf("3", "4"), pageOf("5", "6"))
		ctx, interrupt := context.WithCancel(t.Context())
		var err error
		var done sync.WaitGroup
		done.Go(func() { _, err = shop.FetchAll(ctx, engine.Ref{Resource: "pets"}, nil, 2) })

		synctest.Wait()
		interrupt()
		// The pages under way are given up, or this would wait forever.
		done.Wait()
		if !errors.Is(err, context.Canceled) || apikit.KindOf(err) != apikit.KindCanceled {
			t.Errorf("FetchAll failed with %v (kind %q), want context.Canceled", err, apikit.KindOf(err))
		}
		if got := len(shop.api.Requests()); got != 3 {
			t.Errorf("%d requests were sent, want 3", got)
		}
	})
}
