package engine_test

import (
	"slices"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// IDs of the pet shop.
const (
	owner  = "0b0b0b0b"
	beagle = "0c0c0c0c"
)

func TestGet(t *testing.T) {
	filter := func(key, value string) engine.Filter { return engine.Filter{Key: key, Value: value} }
	tests := map[string]struct {
		change  func(doc *definition.Document, o *engine.Options)
		request engine.GetRequest
		// want is the request that is sent, and resource the resource of
		// the result.
		want     string
		resource string
	}{
		"a listing asks for the default number of items": {
			request: engine.GetRequest{Resource: "pets"},
			want:    adminAPI + "/pets?limit=100", resource: "pets",
		},
		"an item has no limit": {
			request: engine.GetRequest{Resource: "pets", ID: rex},
			want:    mainAPI + "/pets/" + rex, resource: "pets",
		},
		"a sub-resource is a listing, described by its own resource": {
			request: engine.GetRequest{Resource: "pets", ID: rex, Sub: "visits"},
			want:    adminAPI + "/pets/" + rex + "/visits?limit=100", resource: "visits",
		},
		"a limit": {
			request: engine.GetRequest{Resource: "pets", Limit: new(5)},
			want:    adminAPI + "/pets?limit=5", resource: "pets",
		},
		"a page": {
			request: engine.GetRequest{Resource: "pets", Page: new(3)},
			want:    adminAPI + "/pets?limit=100&page=3", resource: "pets",
		},
		"items to skip": {
			request: engine.GetRequest{Resource: "pets", Skip: new(10), Limit: new(20)},
			want:    adminAPI + "/pets?limit=20&skip=10", resource: "pets",
		},
		"zero is a number, too": {
			request: engine.GetRequest{Resource: "pets", Skip: new(0), Limit: new(0)},
			want:    adminAPI + "/pets?limit=0&skip=0", resource: "pets",
		},
		"filters": {
			request: engine.GetRequest{Resource: "pets", Filters: []engine.Filter{filter("species", "long hair"), filter("city", "")}},
			want:    adminAPI + "/pets?city=&limit=100&species=long+hair", resource: "pets",
		},
		"the limit beats a filter of its name": {
			request: engine.GetRequest{Resource: "pets", Filters: []engine.Filter{filter("limit", "7"), filter("skip", "1")}, Skip: new(2)},
			want:    adminAPI + "/pets?limit=100&skip=2", resource: "pets",
		},
		"filters and a page for an item": {
			request: engine.GetRequest{Resource: "pets", ID: rex, Filters: []engine.Filter{filter("expand", "owner")}, Page: new(2), Limit: new(5)},
			want:    mainAPI + "/pets/" + rex + "?expand=owner&page=2", resource: "pets",
		},
		"all of one item is the item": {
			request: engine.GetRequest{Resource: "pets", ID: rex, All: true},
			want:    mainAPI + "/pets/" + rex, resource: "pets",
		},
		"the names and the page size of the definition": {
			change: func(doc *definition.Document, _ *engine.Options) {
				doc.Pagination = definition.Pagination{LimitParam: "per_page", SkipParam: "offset", PageParam: "p", PageSize: 25}
			},
			request: engine.GetRequest{Resource: "pets", Page: new(2)},
			want:    adminAPI + "/pets?p=2&per_page=25", resource: "pets",
		},
		"and the other one": {
			change: func(doc *definition.Document, _ *engine.Options) {
				doc.Pagination = definition.Pagination{LimitParam: "per_page", SkipParam: "offset", PageParam: "p", PageSize: 25}
			},
			request: engine.GetRequest{Resource: "pets", Skip: new(50)},
			want:    adminAPI + "/pets?offset=50&per_page=25", resource: "pets",
		},
		"a resource the definition does not describe": {
			request: engine.GetRequest{Resource: "collars"},
			want:    adminAPI + "/collars?limit=100", resource: "collars",
		},
		"a resource that cannot be listed, by ID": {
			request: engine.GetRequest{Resource: "receipts", ID: rex},
			want:    mainAPI + "/receipts/" + rex, resource: "receipts",
		},
		"a required filter that is given": {
			request: engine.GetRequest{Resource: "orders", Filters: []engine.Filter{filter("status", "open"), filter("ownerId", owner)}},
			want:    adminAPI + "/orders?limit=100&ownerId=" + owner + "&status=open", resource: "orders",
		},
		// The filters a resource requires are for its listing.
		"the sub-resource of a resource with required filters": {
			request: engine.GetRequest{Resource: "orders", ID: rex, Sub: "visits"},
			want:    adminAPI + "/orders/" + rex + "/visits?limit=100", resource: "visits",
		},
		"the required filters of a sub-resource": {
			request: engine.GetRequest{Resource: "owners", ID: owner, Sub: "invoices", Filters: []engine.Filter{filter("state", "paid"), filter("year", "2026")}},
			want:    mainAPI + "/owners/" + owner + "/invoices?limit=100&state=paid&year=2026", resource: "invoices",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var reports []engine.Progress
			shop := open(t, func(doc *definition.Document, o *engine.Options) {
				o.Progress = func(p engine.Progress) { reports = append(reports, p) }
				if test.change != nil {
					test.change(doc, o)
				}
			})
			const answer = `[{"zeta":1.50,"id":"0a1b2c3d"}]`
			shop.api.Handle("GET", test.want, apitest.Response{Raw: answer})

			result, err := shop.Get(t.Context(), test.request)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if result.Resource != test.resource {
				t.Errorf("Resource = %q, want %q", result.Resource, test.resource)
			}
			wantJSON(t, result.Value, answer)
			wantSent(t, shop.api, "GET "+test.want)
			if want := []engine.Progress{{Stage: engine.StageFetch}}; !slices.Equal(reports, want) {
				t.Errorf("progress: %+v, want %+v", reports, want)
			}
		})
	}
}

func TestGetOfAnItemNeedsNoFilters(t *testing.T) {
	// Orders are listed for one owner only; one order is asked for by ID.
	shop := open(t, nil)
	shop.api.Handle("GET", "/v1/orders/"+rex+"/access", apitest.Response{Body: map[string]any{"key": "k-7"}})
	shop.api.Handle("GET", "/v1/orders/"+rex, apitest.Response{Body: map[string]any{"id": rex}})
	result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "orders", ID: rex})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantJSON(t, result.Value, `{"id":"`+rex+`"}`)
	wantSent(t, shop.api, "GET "+mainAPI+"/orders/"+rex+"/access", "GET "+mainAPI+"/orders/"+rex)
}

func TestGetAll(t *testing.T) {
	none := apitest.Response{Body: []any{}}
	fewAhead := func(doc *definition.Document, _ *engine.Options) { doc.HTTP.PaginationMaxWorkers = 1 }

	t.Run("fetches every page, a limit at a time", func(t *testing.T) {
		var reports []engine.Progress
		shop := open(t, func(doc *definition.Document, o *engine.Options) {
			fewAhead(doc, o)
			o.Progress = func(p engine.Progress) { reports = append(reports, p) }
		})
		for skip, page := range map[string]apitest.Response{"0": pageOf("1", "2"), "2": pageOf("3", "4"), "4": pageOf("5")} {
			shop.api.Handle("GET", "/v1/pets?species=cat&limit=2&skip="+skip, page)
		}
		// Where to begin is not for the caller to say.
		result, err := shop.Get(t.Context(), engine.GetRequest{
			Resource: "pets", Filters: []engine.Filter{{Key: "species", Value: "cat"}},
			Limit: new(2), Skip: new(4), All: true,
		})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got := ids(t, result.Value); !slices.Equal(got, []string{"1", "2", "3", "4", "5"}) {
			t.Errorf("Get returned the pets %v", got)
		}
		wantSent(t, shop.api,
			"GET "+adminAPI+"/pets?limit=2&skip=0&species=cat",
			"GET "+adminAPI+"/pets?limit=2&skip=2&species=cat",
			"GET "+adminAPI+"/pets?limit=2&skip=4&species=cat",
		)
		want := []engine.Progress{{Stage: engine.StageFetch}, {Stage: engine.StageFetch, Done: 4}, {Stage: engine.StageFetch, Done: 5}}
		if !slices.Equal(reports, want) {
			t.Errorf("progress: %+v, want %+v", reports, want)
		}
	})

	t.Run("with the page size of the definition", func(t *testing.T) {
		shop := open(t, nil)
		shop.api.Handle("GET", "/v1/pets?limit=100&skip=0", pageOf("1", "2"))
		result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets", All: true})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got := ids(t, result.Value); !slices.Equal(got, []string{"1", "2"}) || result.Resource != "pets" {
			t.Errorf("Get returned the %s %v", result.Resource, got)
		}
	})

	t.Run("of a sub-resource", func(t *testing.T) {
		shop := open(t, fewAhead)
		const visits = "/v1/pets/" + rex + "/visits"
		shop.api.Handle("GET", visits+"?limit=1&skip=0", pageOf("1"))
		shop.api.Handle("GET", visits+"?limit=1&skip=1", pageOf("2"))
		shop.api.Handle("GET", visits+"?limit=1&skip=2", none)
		result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets", ID: rex, Sub: "visits", Limit: new(1), All: true})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got := ids(t, result.Value); !slices.Equal(got, []string{"1", "2"}) || result.Resource != "visits" {
			t.Errorf("Get returned the %s %v", result.Resource, got)
		}
	})

	t.Run("an empty listing is a list", func(t *testing.T) {
		shop := open(t, nil)
		shop.api.Handle("GET", "/v1/pets?limit=100&skip=0", apitest.Response{Status: 204})
		result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets", All: true})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		wantJSON(t, result.Value, `[]`)
	})
}

func TestGetRefusesWhatCannotBeAsked(t *testing.T) {
	tests := map[string]struct {
		request engine.GetRequest
		kind    apikit.Kind
		message string
	}{
		"a page and items to skip": {
			engine.GetRequest{Resource: "pets", Page: new(2), Skip: new(0)},
			apikit.KindUsage, "page and skip cannot be used together",
		},
		"the listing of a resource that has none": {
			engine.GetRequest{Resource: "receipts"},
			apikit.KindFailed, "receipts cannot be listed directly",
		},
		"all of it neither": {
			engine.GetRequest{Resource: "receipts", All: true},
			apikit.KindFailed, "receipts cannot be listed directly",
		},
		"a sub-resource of no item": {
			engine.GetRequest{Resource: "pets", Sub: "visits"},
			apikit.KindUsage, "visits are listed for one pet, whose ID is missing",
		},
		"a listing without its required filter": {
			engine.GetRequest{Resource: "orders", Filters: []engine.Filter{{Key: "status", Value: "open"}}},
			apikit.KindUsage, "orders requires filter(s): ownerId",
		},
		"a listing of all pages without it": {
			engine.GetRequest{Resource: "orders", All: true},
			apikit.KindUsage, "orders requires filter(s): ownerId",
		},
		"a sub-resource without its required filters": {
			engine.GetRequest{Resource: "owners", ID: owner, Sub: "invoices"},
			apikit.KindUsage, "invoices requires filter(s): year, state",
		},
		"only the missing ones are named": {
			engine.GetRequest{Resource: "owners", ID: owner, Sub: "invoices", Filters: []engine.Filter{{Key: "year", Value: ""}}},
			apikit.KindUsage, "invoices requires filter(s): state",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			result, err := shop.Get(t.Context(), test.request)
			wantError(t, err, test.kind)
			if err.Error() != test.message {
				t.Errorf("message: %q, want %q", err, test.message)
			}
			if result != nil {
				t.Errorf("Get returned %+v besides the error", result)
			}
			wantSent(t, shop.api)
		})
	}
}

func TestGetFails(t *testing.T) {
	tests := map[string]struct {
		request  engine.GetRequest
		url      string
		response apitest.Response
		kind     apikit.Kind
		exit     int
		message  string
	}{
		"an item the API does not have": {
			engine.GetRequest{Resource: "pets", ID: rex}, "/v1/pets/" + rex,
			apitest.Response{Status: 404, Raw: "no such pet"},
			apikit.KindNotFound, apikit.ExitNotFound, "failed to fetch pet by ID " + rex + ": 404 no such pet",
		},
		"an API that fails": {
			engine.GetRequest{Resource: "pets", ID: rex}, "/v1/pets/" + rex,
			apitest.Response{Status: 500, Raw: "boom"},
			apikit.KindFailed, apikit.ExitFailed, "failed to fetch pet by ID " + rex + ": 500 boom",
		},
		"a listing the API does not have": {
			engine.GetRequest{Resource: "collars"}, "/v1/collars?limit=100",
			apitest.Response{Status: 404},
			apikit.KindNotFound, apikit.ExitNotFound, "failed to fetch collars: 404",
		},
		// A failed request for a sub-resource is told in terms of its item.
		"a sub-resource that fails": {
			engine.GetRequest{Resource: "pets", ID: rex, Sub: "visits"}, "/v1/pets/" + rex + "/visits?limit=100",
			apitest.Response{Status: 403, Raw: "not yours"},
			apikit.KindFailed, apikit.ExitFailed, "failed to fetch pet by ID " + rex + ": 403 not yours",
		},
		"an item that is nothing": {
			engine.GetRequest{Resource: "pets", ID: rex}, "/v1/pets/" + rex,
			apitest.Response{Status: 204},
			apikit.KindNotFound, apikit.ExitNotFound, "unable to find pet " + rex,
		},
		"an item that is null": {
			engine.GetRequest{Resource: "mice", ID: rex}, "/v1/mice/" + rex,
			apitest.Response{Raw: "null"},
			apikit.KindNotFound, apikit.ExitNotFound, "unable to find mouse " + rex,
		},
		"a sub-resource that is nothing": {
			engine.GetRequest{Resource: "pets", ID: rex, Sub: "visits"}, "/v1/pets/" + rex + "/visits?limit=100",
			apitest.Response{Status: 204},
			apikit.KindNotFound, apikit.ExitNotFound, "unable to find pet " + rex,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			shop.api.Handle("GET", test.url, test.response)
			result, err := shop.Get(t.Context(), test.request)
			wantError(t, err, test.kind)
			if err.Error() != test.message {
				t.Errorf("message: %q, want %q", err, test.message)
			}
			if code := apikit.ExitCode(err); code != test.exit {
				t.Errorf("ExitCode = %d, want %d", code, test.exit)
			}
			if result != nil {
				t.Errorf("Get returned %+v besides the error", result)
			}
		})
	}

	// Nothing is a fine answer for a listing.
	shop := open(t, nil)
	shop.api.Handle("GET", "/v1/pets?limit=100", apitest.Response{Status: 204})
	result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
	if err != nil || result.Value != nil || result.Items() != nil {
		t.Errorf("Get of a listing without content = %+v, %v; want nothing and no error", result, err)
	}
}

func TestGetResolvesTheID(t *testing.T) {
	const search = adminAPI + "/pets?search=%22REX-7%22"
	found := apitest.Response{Body: map[string]any{"id": rex}}

	tests := map[string]struct {
		request engine.GetRequest
		// routes are the answers of the API by URL, sent the requests in
		// their order.
		routes map[string]apitest.Response
		sent   []string
		// message is that of the error, empty for a success.
		message string
	}{
		"an alias is searched for": {
			request: engine.GetRequest{Resource: "pets", ID: "REX-7"},
			routes: map[string]apitest.Response{
				search:                   {Body: []any{map[string]any{"id": rex, "tag": "REX-7"}}},
				mainAPI + "/pets/" + rex: found,
			},
			sent: []string{search, mainAPI + "/pets/" + rex},
		},
		// For reading, what the search finds is good enough.
		"the only hit counts, whatever its tag": {
			request: engine.GetRequest{Resource: "pets", ID: "REX-7"},
			routes: map[string]apitest.Response{
				search:                   {Body: []any{map[string]any{"id": rex, "tag": "REX-1", "notes": "brother of REX-7"}}},
				mainAPI + "/pets/" + rex: found,
			},
			sent: []string{search, mainAPI + "/pets/" + rex},
		},
		"the sub-resource of an alias": {
			request: engine.GetRequest{Resource: "pets", ID: "REX-7", Sub: "visits"},
			routes: map[string]apitest.Response{
				search: {Body: []any{map[string]any{"petId": rex}}},
				adminAPI + "/pets/" + rex + "/visits?limit=100": found,
			},
			sent: []string{search, adminAPI + "/pets/" + rex + "/visits?limit=100"},
		},
		"all pages of the sub-resource of an alias": {
			request: engine.GetRequest{Resource: "pets", ID: "REX-7", Sub: "visits", All: true},
			routes: map[string]apitest.Response{
				search: {Body: []any{map[string]any{"id": rex}}},
				adminAPI + "/pets/" + rex + "/visits?limit=100&skip=0": found,
			},
			sent: []string{search, adminAPI + "/pets/" + rex + "/visits?limit=100&skip=0"},
		},
		"an ID is not searched for": {
			request: engine.GetRequest{Resource: "pets", ID: rex},
			routes:  map[string]apitest.Response{mainAPI + "/pets/" + rex: found},
			sent:    []string{mainAPI + "/pets/" + rex},
		},
		// The alias of cages is for every value that starts with a "c".
		"nor is an ID that looks like an alias": {
			request: engine.GetRequest{Resource: "cages", ID: "c0c0c0c0"},
			routes:  map[string]apitest.Response{mainAPI + "/cages/c0c0c0c0": found},
			sent:    []string{mainAPI + "/cages/c0c0c0c0"},
		},
		"a value no alias is for is taken as the ID": {
			request: engine.GetRequest{Resource: "pets", ID: "rex 7"},
			routes:  map[string]apitest.Response{mainAPI + "/pets/rex%207": found},
			sent:    []string{mainAPI + "/pets/rex%207"},
		},
		"a name is looked for in the whole listing": {
			request: engine.GetRequest{Resource: "breeds", ID: "beagle"},
			routes: map[string]apitest.Response{
				mainAPI + "/breeds?limit=100&skip=0": {Body: []any{map[string]any{"id": owner, "name": "pug"}, map[string]any{"id": beagle, "name": "beagle"}}},
				mainAPI + "/breeds/" + beagle:        found,
			},
			sent: []string{mainAPI + "/breeds?limit=100&skip=0", mainAPI + "/breeds/" + beagle},
		},
		"an alias that names nothing": {
			request: engine.GetRequest{Resource: "pets", ID: "REX-7"},
			routes:  map[string]apitest.Response{search: {Body: []any{}}},
			sent:    []string{search},
			message: "unable to find pet REX-7",
		},
		"an alias that names several": {
			request: engine.GetRequest{Resource: "pets", ID: "REX-7"},
			routes:  map[string]apitest.Response{search: {Body: list(rex, owner)}},
			sent:    []string{search},
			message: "unable to find pet REX-7",
		},
		"a hit that is no item": {
			request: engine.GetRequest{Resource: "pets", ID: "REX-7"},
			routes:  map[string]apitest.Response{search: {Raw: `["REX-7"]`}},
			sent:    []string{search},
			message: "unable to find pet REX-7",
		},
		"a name the listing does not have": {
			request: engine.GetRequest{Resource: "breeds", ID: "beagle", All: true},
			routes:  map[string]apitest.Response{mainAPI + "/breeds?limit=100&skip=0": {Body: []any{map[string]any{"id": owner, "name": "pug"}}}},
			sent:    []string{mainAPI + "/breeds?limit=100&skip=0"},
			message: "unable to find breed beagle",
		},
		"an ID of dots is no ID": {
			request: engine.GetRequest{Resource: "pets", ID: ".."},
			message: "unable to find pet ..",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			for target, response := range test.routes {
				shop.api.Handle("GET", target, response)
			}
			result, err := shop.Get(t.Context(), test.request)
			if test.message != "" {
				wantError(t, err, apikit.KindNotFound)
				if err.Error() != test.message {
					t.Errorf("message: %q, want %q", err, test.message)
				}
			} else {
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				wantJSON(t, result.Value, `{"id":"`+rex+`"}`)
			}
			var want []string
			for _, target := range test.sent {
				want = append(want, "GET "+target)
			}
			wantSent(t, shop.api, want...)
		})
	}
}

func TestGetFailsWhenTheIDCannotBeResolved(t *testing.T) {
	for name, request := range map[string]engine.GetRequest{
		"an item":                       {Resource: "pets", ID: "REX-7"},
		"all pages of its sub-resource": {Resource: "pets", ID: "REX-7", Sub: "visits", All: true},
	} {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			shop.api.Handle("GET", "/v1/pets?search=%22REX-7%22", apitest.Response{Status: 500, Raw: "boom"})
			result, err := shop.Get(t.Context(), request)
			wantError(t, err, apikit.KindFailed, "failed to fetch pets: 500 boom")
			if result != nil {
				t.Errorf("Get returned %+v besides the error", result)
			}
			wantSent(t, shop.api, "GET "+adminAPI+"/pets?search=%22REX-7%22")
		})
	}
}

func TestGetResolvesTheValuesOfFilters(t *testing.T) {
	const (
		byNumber = adminAPI + "/owners?number=4711"
		byTag    = adminAPI + "/pets?search=%22REX-7%22"
		breeds   = mainAPI + "/breeds?limit=100&skip=0"
	)
	theOwner := apitest.Response{Raw: `[{"id":"` + owner + `","number":4711}]`}
	orders := func(filter string) string { return adminAPI + "/orders?" + filter }

	tests := map[string]struct {
		resource string
		filters  []string
		routes   map[string]apitest.Response
		// sent are the requests in their order; the last one is the
		// listing. message is that of the error, empty for a success.
		sent    []string
		message string
	}{
		"a membership number becomes the ID of the owner": {
			resource: "orders", filters: []string{"ownerId=4711"},
			routes: map[string]apitest.Response{byNumber: theOwner},
			sent:   []string{byNumber, orders("limit=100&ownerId=" + owner)},
		},
		"an ID stays as it is": {
			resource: "orders", filters: []string{"ownerId=" + owner},
			sent: []string{orders("limit=100&ownerId=" + owner)},
		},
		"so does the value of a filter without rules": {
			resource: "orders", filters: []string{"ownerId=" + owner, "status=4711"},
			sent: []string{orders("limit=100&ownerId=" + owner + "&status=4711")},
		},
		"a value no rule is for": {
			resource: "orders", filters: []string{"ownerId=someone"},
			sent: []string{orders("limit=100&ownerId=someone")},
		},
		"and no value at all": {
			resource: "orders", filters: []string{"ownerId="},
			sent: []string{orders("limit=100&ownerId=")},
		},
		"the first rule that is for the value decides: a pet": {
			resource: "orders", filters: []string{"ownerId=" + owner + ",about=REX-7"},
			routes: map[string]apitest.Response{byTag: {Body: []any{map[string]any{"id": rex, "tag": "REX-7"}}}},
			sent:   []string{byTag, orders("about=" + rex + "&limit=100&ownerId=" + owner)},
		},
		"the first rule that is for the value decides: an owner": {
			resource: "orders", filters: []string{"ownerId=" + owner + ",about=4711"},
			routes: map[string]apitest.Response{byNumber: theOwner},
			sent:   []string{byNumber, orders("about=" + owner + "&limit=100&ownerId=" + owner)},
		},
		"a rule without a pattern is for every value": {
			resource: "pets", filters: []string{"breedId=beagle"},
			routes: map[string]apitest.Response{breeds: {Body: []any{map[string]any{"id": beagle, "name": "beagle"}}}},
			sent:   []string{breeds, adminAPI + "/pets?breedId=" + beagle + "&limit=100"},
		},
		// A filter that finds nothing would look like an empty listing.
		"a value that names nothing stops the request": {
			resource: "orders", filters: []string{"ownerId=4711"},
			routes:  map[string]apitest.Response{byNumber: {Body: []any{}}},
			sent:    []string{byNumber},
			message: "unable to find owner 4711",
		},
		// The filter must name the owner, not one whose number holds it.
		"a hit with another number is not the one": {
			resource: "orders", filters: []string{"ownerId=4711"},
			routes:  map[string]apitest.Response{byNumber: {Raw: `[{"id":"` + owner + `","number":14711}]`}},
			sent:    []string{byNumber},
			message: "unable to find owner 4711",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			for target, response := range test.routes {
				shop.api.Handle("GET", target, response)
			}
			if test.message == "" {
				shop.api.Handle("GET", test.sent[len(test.sent)-1], pageOf("1"))
			}
			filters, err := engine.ParseFilters(test.filters)
			if err != nil {
				t.Fatalf("ParseFilters: %v", err)
			}
			given := slices.Clone(filters)

			result, err := shop.Get(t.Context(), engine.GetRequest{Resource: test.resource, Filters: filters})
			if test.message != "" {
				wantError(t, err, apikit.KindNotFound)
				if err.Error() != test.message {
					t.Errorf("message: %q, want %q", err, test.message)
				}
			} else {
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				wantJSON(t, result.Value, `[{"id":"1"}]`)
			}
			var want []string
			for _, target := range test.sent {
				want = append(want, "GET "+target)
			}
			wantSent(t, shop.api, want...)
			if !slices.Equal(filters, given) {
				t.Errorf("Get changed the filters of the request: %v", filters)
			}
		})
	}
}

func TestItem(t *testing.T) {
	tests := map[string]struct {
		id       string
		response apitest.Response
		want     string
		message  string
	}{
		"an object":                 {rex, apitest.Response{Raw: `{"name":"Rex","id":"0a1b2c3d"}`}, `{"name":"Rex","id":"0a1b2c3d"}`, ""},
		"a list of one is its item": {rex, apitest.Response{Raw: `[{"name":"Rex"}]`}, `{"name":"Rex"}`, ""},
		"an empty object":           {rex, apitest.Response{Raw: `{}`}, `{}`, ""},
		"a list of two":             {rex, apitest.Response{Body: list("1", "2")}, "", "unable to find pet " + rex},
		"an empty list":             {rex, apitest.Response{Body: []any{}}, "", "unable to find pet " + rex},
		"a list of something else":  {rex, apitest.Response{Raw: `["Rex"]`}, "", "unable to find pet " + rex},
		"nothing":                   {rex, apitest.Response{Status: 204}, "", "unable to find pet " + rex},
		"a string":                  {rex, apitest.Response{Raw: `"Rex"`}, "", "unable to find pet " + rex},
		"an item the API lacks":     {rex, apitest.Response{Status: 404}, "", "failed to fetch pet by ID " + rex + ": 404"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			shop.api.Handle("GET", "/v1/pets/"+test.id, test.response)
			item, err := shop.Item(t.Context(), "pets", test.id)
			if test.message != "" {
				wantError(t, err, apikit.KindNotFound)
				if err.Error() != test.message || item != nil {
					t.Errorf("Item = %v, %q; want the error %q", item, err, test.message)
				}
				return
			}
			if err != nil {
				t.Fatalf("Item: %v", err)
			}
			wantJSON(t, item, test.want)
		})
	}

	t.Run("an alias", func(t *testing.T) {
		shop := open(t, nil)
		shop.api.Handle("GET", "/v1/pets?search=%22REX-7%22", apitest.Response{Body: list(rex)})
		shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Body: map[string]any{"id": rex}})
		item, err := shop.Item(t.Context(), "pets", "REX-7")
		if err != nil {
			t.Fatalf("Item: %v", err)
		}
		wantJSON(t, item, `{"id":"`+rex+`"}`)
	})

	t.Run("an alias that names nothing", func(t *testing.T) {
		shop := open(t, nil)
		shop.api.Handle("GET", "/v1/pets?search=%22REX-7%22", apitest.Response{Body: []any{}})
		_, err := shop.Item(t.Context(), "pets", "REX-7")
		wantError(t, err, apikit.KindNotFound, "unable to find pet REX-7")
	})

	t.Run("no ID", func(t *testing.T) {
		shop := open(t, nil)
		item, err := shop.Item(t.Context(), "pets", "")
		wantError(t, err, apikit.KindNotFound, "unable to find pet")
		if item != nil {
			t.Errorf("Item = %v besides the error", item)
		}
		wantSent(t, shop.api)
	})
}
