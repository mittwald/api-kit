package engine_test

import (
	"errors"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

func TestResolveID(t *testing.T) {
	const (
		byTag    = adminAPI + "/pets?search=%22REX-7%22"
		byNumber = adminAPI + "/owners?number=4711"
		byLabel  = adminAPI + "/toys?kind=toy&label=t%3Aball"
		probe    = mainAPI + "/owners/o-ada"
		breeds   = mainAPI + "/breeds?limit=2&skip="
		shelters = adminAPI + "/shelters?limit=2&skip="
		other    = "0d0d0d0d"
	)
	pets := func(json string) apitest.Response { return apitest.Response{Raw: json} }
	// fewAhead keeps a listing to the pages it needs, two items each.
	fewAhead := func(doc *definition.Document, _ *engine.Options) {
		doc.HTTP.PaginationMaxWorkers, doc.Pagination.PageSize = 1, 2
	}

	tests := map[string]struct {
		change   func(doc *definition.Document, o *engine.Options)
		resource string
		value    string
		routes   map[string]apitest.Response
		// want is the ID, empty for a value that names nothing. sent are
		// the requests in their order.
		want string
		sent []string
	}{
		// What looks like an ID is one.
		"an ID is returned as it is":             {resource: "pets", value: rex, want: rex},
		"whatever the rules of the resource say": {resource: "breeds", value: beagle, want: beagle},
		"no value names nothing":                 {resource: "pets", value: ""},
		// The alias of cages is for every value that starts with a "c".
		"an ID is no alias, whatever it looks like": {resource: "cages", value: "c0c0c0c0", want: "c0c0c0c0"},

		// An alias that names its field.
		"the item whose field is the alias, not one that mentions it": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[
				{"id":"` + other + `","tag":"REX-1","notes":"brother of REX-7"},
				{"id":"` + rex + `","tag":"REX-7","name":"Rex"}]`)},
			want: rex, sent: []string{byTag},
		},
		"an item that only mentions the alias is not meant": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[{"id":"` + other + `","tag":"REX-1","notes":"brother of REX-7"}]`)},
			sent:   []string{byTag},
		},
		"nor is one that has the alias in another field": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[
				{"id":"` + other + `","tag":"REX-1","name":"REX-7"},
				{"id":"` + rex + `","tag":"REX-2","notes":"brother of REX-7"}]`)},
			sent: []string{byTag},
		},
		"two items with the alias in their field": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[{"id":"` + other + `","tag":"REX-7"},{"id":"` + rex + `","tag":"REX-7"}]`)},
			sent:   []string{byTag},
		},
		"a search without hits": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[]`)},
			sent:   []string{byTag},
		},
		"a search without an answer": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: {Status: 204}},
			sent:   []string{byTag},
		},
		"a search that answers with one item, not a list": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`{"id":"` + rex + `","tag":"REX-7"}`)},
			sent:   []string{byTag},
		},
		"hits that are no items": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`["REX-7"]`)},
			sent:   []string{byTag},
		},
		"a hit without an ID": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[{"tag":"REX-7"}]`)},
			sent:   []string{byTag},
		},
		"a hit whose ID counts as nothing": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[{"id":0,"tag":"REX-7"}]`)},
			sent:   []string{byTag},
		},
		"the ID in the field named after the resource": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: pets(`[{"petId":"` + rex + `","tag":"REX-7"}]`)},
			want:   rex, sent: []string{byTag},
		},
		"a field that is a number equals its text": {
			resource: "owners", value: "4711",
			routes: map[string]apitest.Response{byNumber: pets(`[{"id":"` + owner + `","number":4711},{"id":"` + other + `","number":"14711"}]`)},
			want:   owner, sent: []string{byNumber},
		},

		"a field that is no path equals nothing": {
			resource: "cages", value: "c1",
			routes: map[string]apitest.Response{adminAPI + "/cages?q=c1": pets(`[{"id":"` + other + `","tag":{"code":"c1"},"tag..code":"c1"}]`)},
			sent:   []string{adminAPI + "/cages?q=c1"},
		},

		// An alias without a field takes what the search finds.
		"the only hit of an alias without a field": {
			resource: "toys", value: "t:ball",
			routes: map[string]apitest.Response{byLabel: pets(`[{"id":"` + other + `","label":"a ball"}]`)},
			want:   other, sent: []string{byLabel},
		},
		"several hits of an alias without a field": {
			resource: "toys", value: "t:ball",
			routes: map[string]apitest.Response{byLabel: pets(`[{"id":"` + other + `"},{"id":"` + rex + `"}]`)},
			sent:   []string{byLabel},
		},
		// The first alias that is for the value decides, found or not.
		"later aliases are not tried": {
			resource: "toys", value: "t:ball",
			routes: map[string]apitest.Response{byLabel: pets(`[]`)},
			sent:   []string{byLabel},
		},
		// The value is one value of the query, whatever it holds.
		"a value cannot add a filter to the search": {
			resource: "toys", value: "t:a,b=c&d= e",
			routes: map[string]apitest.Response{adminAPI + "/toys?kind=toy&label=t%3Aa%2Cb%3Dc%26d%3D+e": pets(`[{"id":"` + other + `"}]`)},
			want:   other, sent: []string{adminAPI + "/toys?kind=toy&label=t%3Aa%2Cb%3Dc%26d%3D+e"},
		},
		"a search that fails": {
			resource: "pets", value: "REX-7",
			routes: map[string]apitest.Response{byTag: {Status: 500, Raw: "boom"}},
			want:   "failed to fetch pets: 500 boom", sent: []string{byTag},
		},

		// What the alias found may be known to the API under another name.
		"an alias that leads to something that is no ID yet": {
			resource: "toys", value: "t:ball",
			routes: map[string]apitest.Response{
				byLabel:                 pets(`[{"id":"toy-7"}]`),
				mainAPI + "/toys/toy-7": pets(`{"id":"` + other + `"}`),
			},
			want: other, sent: []string{byLabel, mainAPI + "/toys/toy-7"},
		},

		// No rule is for the value: the API is asked for the item.
		"the API knows the item by the value and tells its ID": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: pets(`{"name":"Ada","id":"` + owner + `"}`)},
			want:   owner, sent: []string{probe},
		},
		"the API does not know the item": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: {Status: 404}},
			sent:   []string{probe},
		},
		// The value may still be good where it is used.
		"the API does not tell": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: {Status: 403, Raw: "not yours"}},
			want:   "o-ada", sent: []string{probe},
		},
		"the API fails": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: {Status: 500}},
			want:   "o-ada", sent: []string{probe},
		},
		"the API answers with nothing": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: {Status: 204}},
			want:   "o-ada", sent: []string{probe},
		},
		"the item has no ID": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: pets(`{"name":"Ada"}`)},
			want:   "o-ada", sent: []string{probe},
		},
		"the item is a list": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: pets(`[{"id":"` + owner + `"}]`)},
			want:   "o-ada", sent: []string{probe},
		},
		// An ID that counts as nothing is none.
		"the ID is empty": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: pets(`{"id":""}`)},
			want:   "o-ada", sent: []string{probe},
		},
		"the ID is null": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: pets(`{"id":null}`)},
			want:   "o-ada", sent: []string{probe},
		},
		"the ID is zero": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: pets(`{"id":0}`)},
			want:   "o-ada", sent: []string{probe},
		},
		"the ID is a number": {
			resource: "owners", value: "o-ada",
			routes: map[string]apitest.Response{probe: pets(`{"id":42}`)},
			want:   "42", sent: []string{probe},
		},
		"the ID is in the field of the resource": {
			resource: "kennels", value: "k-1",
			routes: map[string]apitest.Response{mainAPI + "/boarding/kennels/k-1": pets(`{"id":"` + other + `","code":"` + rex + `"}`)},
			want:   rex, sent: []string{mainAPI + "/boarding/kennels/k-1"},
		},
		"a resource the definition does not describe": {
			resource: "collars", value: "c-1",
			routes: map[string]apitest.Response{mainAPI + "/collars/c-1": pets(`{"collarId":"` + rex + `"}`)},
			want:   rex, sent: []string{mainAPI + "/collars/c-1"},
		},
		"an alias without a pattern is for no value": {
			resource: "toys", value: "ball",
			routes: map[string]apitest.Response{mainAPI + "/toys/ball": pets(`{"id":"` + other + `"}`)},
			want:   other, sent: []string{mainAPI + "/toys/ball"},
		},

		// An ID search reads the whole listing.
		"a name in the listing": {
			change: fewAhead, resource: "breeds", value: "beagle",
			routes: map[string]apitest.Response{
				breeds + "0": pets(`[{"id":"` + other + `","name":"pug"},{"id":"` + rex + `","name":"beagle mix"}]`),
				breeds + "2": pets(`[{"id":"` + beagle + `","name":"beagle"}]`),
			},
			want: beagle, sent: []string{breeds + "0", breeds + "2"},
		},
		"a name the listing does not have": {
			change: fewAhead, resource: "breeds", value: "beagle",
			routes: map[string]apitest.Response{breeds + "0": pets(`[{"id":"` + other + `","name":"pug"}]`)},
			sent:   []string{breeds + "0"},
		},
		"an empty listing": {
			change: fewAhead, resource: "breeds", value: "beagle",
			routes: map[string]apitest.Response{breeds + "0": pets(`[]`)},
			sent:   []string{breeds + "0"},
		},
		"a listing that is none": {
			change: fewAhead, resource: "breeds", value: "beagle",
			routes: map[string]apitest.Response{breeds + "0": pets(`{"id":"` + beagle + `","name":"beagle"}`)},
			sent:   []string{breeds + "0"},
		},
		// Only text equals the value.
		"a name that is a number": {
			change: fewAhead, resource: "breeds", value: "7",
			routes: map[string]apitest.Response{breeds + "0": pets(`[{"id":"` + other + `","name":7}]`)},
			sent:   []string{breeds + "0"},
		},
		"an item of that name whose ID counts as nothing": {
			change: fewAhead, resource: "breeds", value: "beagle",
			routes: map[string]apitest.Response{breeds + "0": pets(`[{"id":"","name":"beagle"}]`)},
			sent:   []string{breeds + "0"},
		},
		// What it finds is taken further like any other value.
		"the first item of that name that has an ID": {
			change: fewAhead, resource: "breeds", value: "beagle",
			routes: map[string]apitest.Response{
				breeds + "0":          pets(`["beagle",{"id":null,"name":"beagle"}]`),
				breeds + "2":          pets(`[{"id":7,"name":"beagle"}]`),
				mainAPI + "/breeds/7": {Status: 403},
			},
			want: "7", sent: []string{breeds + "0", breeds + "2", mainAPI + "/breeds/7"},
		},
		"paths to the name and the ID": {
			change: fewAhead, resource: "shelters", value: "Bonn",
			routes: map[string]apitest.Response{
				shelters + "0": pets(`[
					{"id":"` + other + `","name":"Bonn","address":{"city":"Kiel"}},
					{"id":"` + rex + `","address":{"city":"Bonn"},"ref":{"id":"` + beagle + `"}}]`),
				shelters + "2": pets(`[]`),
			},
			want: beagle, sent: []string{shelters + "0", shelters + "2"},
		},
		// The rule is for values that do not start with a digit.
		"a search that is not for the value": {
			change: fewAhead, resource: "shelters", value: "7 Oaks",
			routes: map[string]apitest.Response{mainAPI + "/shelters/7%20Oaks": pets(`{"id":"` + other + `"}`)},
			want:   other, sent: []string{mainAPI + "/shelters/7%20Oaks"},
		},
		"a listing that fails": {
			change: fewAhead, resource: "breeds", value: "beagle",
			routes: map[string]apitest.Response{breeds + "0": {Status: 404}},
			want:   "failed to fetch breeds: 404", sent: []string{breeds + "0"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, test.change)
			for target, response := range test.routes {
				shop.api.Handle("GET", target, response)
			}

			got, err := shop.ResolveID(t.Context(), test.resource, test.value)
			switch failure, failed := errors.AsType[*engine.HTTPError](err); {
			case failed:
				if err.Error() != test.want {
					t.Errorf("ResolveID failed with %q, want %q", err, test.want)
				}
				if failure.Resource != test.resource {
					t.Errorf("the failure is about %q, want %q", failure.Resource, test.resource)
				}
			case test.want == "":
				wantError(t, err, apikit.KindNotFound)
				want := "unable to find " + shop.doc.Resource(test.resource).Singular + " " + test.value
				if err.Error() != want || got != "" {
					t.Errorf("ResolveID = %q, %q; want the error %q", got, err, want)
				}
				if code := apikit.ExitCode(err); code != apikit.ExitNotFound {
					t.Errorf("ExitCode = %d, want %d", code, apikit.ExitNotFound)
				}
			case err != nil:
				t.Fatalf("ResolveID: %v", err)
			case got != test.want:
				t.Errorf("ResolveID = %q, want %q", got, test.want)
			}
			var want []string
			for _, target := range test.sent {
				want = append(want, "GET "+target)
			}
			wantSent(t, shop.api, want...)
		})
	}
}

func TestResolveIDCannotReachTheAPI(t *testing.T) {
	shop := open(t, func(doc *definition.Document, _ *engine.Options) { doc.HTTP.Retries = 0 })
	shop.api.Handle("GET", "/v1/owners/o-ada", apitest.Response{Err: errors.New("connection refused")})
	got, err := shop.ResolveID(t.Context(), "owners", "o-ada")
	wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch owners", "connection refused")
	if got != "" {
		t.Errorf("ResolveID = %q besides the error", got)
	}
}

func TestResolveIDWithoutIDsOfItsOwn(t *testing.T) {
	// A definition that does not say what an ID looks like takes no value
	// for one: the API is asked every time.
	shop := open(t, func(doc *definition.Document, _ *engine.Options) { doc.Identifiers.Native = nil })
	shop.api.Handle("GET", "/v1/owners/"+owner, apitest.Response{Body: map[string]any{"id": "o-1"}})
	got, err := shop.ResolveID(t.Context(), "owners", owner)
	if err != nil || got != "o-1" {
		t.Errorf("ResolveID = %q, %v; want o-1", got, err)
	}
	wantSent(t, shop.api, "GET "+mainAPI+"/owners/"+owner)
}

func TestIDOf(t *testing.T) {
	tests := map[string]struct {
		change   func(doc *definition.Document, o *engine.Options)
		resource string
		item     string
		want     string
		ok       bool
	}{
		"the field id":                          {nil, "pets", `{"name":"Rex","id":"a"}`, "a", true},
		"the field named after the resource":    {nil, "pets", `{"name":"Rex","petId":"b"}`, "b", true},
		"id comes first":                        {nil, "pets", `{"petId":"b","id":"a"}`, "a", true},
		"the name of one item of the resource":  {nil, "mice", `{"miceId":"x","mouseId":"m"}`, "m", true},
		"the ID field of the resource is first": {nil, "kennels", `{"id":"a","kennelId":"b","code":"c"}`, "c", true},
		"without it, the others":                {nil, "kennels", `{"kennelId":"b"}`, "b", true},
		"a resource that is not described":      {nil, "collars", `{"collarId":"c"}`, "c", true},
		"a number":                              {nil, "pets", `{"id":42.0}`, "42.0", true},
		"null is a field, too":                  {nil, "pets", `{"id":null,"petId":"b"}`, "", true},
		"no field for the ID":                   {nil, "pets", `{"name":"Rex","ownerId":"o"}`, "", false},
		"no fields at all":                      {nil, "pets", `{}`, "", false},
		"the fields of the definition": {
			func(doc *definition.Document, _ *engine.Options) {
				doc.Identifiers.Fields = []string{"uid", "${singular}_ref"}
			},
			"pets", `{"id":"a","pet_ref":"r"}`, "r", true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, test.change)
			item, _ := decode(t, test.item).(*apikit.Object)
			got, ok := shop.IDOf(test.resource, item)
			if got != test.want || ok != test.ok {
				t.Errorf("IDOf(%s, %s) = %q, %v; want %q, %v", test.resource, test.item, got, ok, test.want, test.ok)
			}
		})
	}

	if got, ok := open(t, nil).IDOf("pets", nil); got != "" || ok {
		t.Errorf("IDOf of no item = %q, %v", got, ok)
	}
}
