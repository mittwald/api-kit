package engine_test

import (
	"slices"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

func TestParseFilters(t *testing.T) {
	tests := map[string]struct {
		args []string
		want []engine.Filter
	}{
		"no arguments": {nil, nil},
		"pairs of one argument": {
			[]string{"species=cat,ownerId=4711"},
			[]engine.Filter{{Key: "species", Value: "cat"}, {Key: "ownerId", Value: "4711"}},
		},
		"several arguments": {
			[]string{"species=cat", "city=Bonn,vaccinated=true"},
			[]engine.Filter{{Key: "species", Value: "cat"}, {Key: "city", Value: "Bonn"}, {Key: "vaccinated", Value: "true"}},
		},
		// The value keeps everything after the first equals sign.
		"a value with equals signs": {
			[]string{"search=name=Rex=", "born>=2026-01-01"},
			[]engine.Filter{{Key: "search", Value: "name=Rex="}, {Key: "born>", Value: "2026-01-01"}},
		},
		"an empty value and no value at all": {
			[]string{"species=,vaccinated"},
			[]engine.Filter{{Key: "species", Value: ""}, {Key: "vaccinated", Value: ""}},
		},
		"white space around keys and values": {
			[]string{" species = long hair , city=Bonn "},
			[]engine.Filter{{Key: "species", Value: "long hair"}, {Key: "city", Value: "Bonn"}},
		},
		"the same key twice": {
			[]string{"species=cat,species=dog"},
			[]engine.Filter{{Key: "species", Value: "cat"}, {Key: "species", Value: "dog"}},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := engine.ParseFilters(test.args)
			if err != nil {
				t.Fatalf("ParseFilters(%q): %v", test.args, err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("ParseFilters(%q) = %v, want %v", test.args, got, test.want)
			}
		})
	}

	failures := map[string]struct {
		args    []string
		message string
	}{
		"an empty argument":     {[]string{""}, `filter "" has no key, expected key=value`},
		"a value without a key": {[]string{"species=cat, =dog"}, `filter "=dog" has no key`},
		"a comma at the end":    {[]string{"species=cat,"}, `filter "" has no key`},
	}
	for name, test := range failures {
		t.Run(name, func(t *testing.T) {
			got, err := engine.ParseFilters(test.args)
			wantError(t, err, apikit.KindUsage, test.message)
			if got != nil {
				t.Errorf("ParseFilters(%q) = %v besides the error", test.args, got)
			}
		})
	}
}

func TestEncodeQuery(t *testing.T) {
	tests := map[string]struct {
		query []engine.Filter
		want  string
	}{
		"no parameters, no question mark": {nil, ""},
		"sorted by key": {
			[]engine.Filter{{Key: "species", Value: "cat"}, {Key: "limit", Value: "100"}, {Key: "city", Value: "Bonn"}},
			"?city=Bonn&limit=100&species=cat",
		},
		"then by value": {
			[]engine.Filter{{Key: "species", Value: "dog"}, {Key: "a", Value: "2"}, {Key: "species", Value: "cat"}, {Key: "a", Value: "10"}},
			"?a=10&a=2&species=cat&species=dog",
		},
		"an empty value keeps its equals sign": {
			[]engine.Filter{{Key: "petId", Value: "pets:abc"}, {Key: "empty", Value: ""}, {Key: "ownerId", Value: "owners:def"}},
			"?empty=&ownerId=owners%3Adef&petId=pets%3Aabc",
		},
		// What an HTML form sends: a plus for a space, upper-case hex for
		// the bytes of everything but letters, digits and "-", "_", ".",
		// "~".
		"escaped as a form is": {
			[]engine.Filter{{Key: "search", Value: `"REX-7" & more/less?`}, {Key: "name", Value: "Zoë +1 ~ a_b.c*"}},
			"?name=Zo%C3%AB+%2B1+~+a_b.c%2A&search=%22REX-7%22+%26+more%2Fless%3F",
		},
		"keys are escaped too": {
			[]engine.Filter{{Key: "born>", Value: "2026-01-01T10:00:00Z"}, {Key: "tag[]", Value: "a,b=c"}},
			"?born%3E=2026-01-01T10%3A00%3A00Z&tag%5B%5D=a%2Cb%3Dc",
		},
		// Upper case sorts before lower case, bytes decide.
		"sorted by bytes": {
			[]engine.Filter{{Key: "b", Value: "1"}, {Key: "B", Value: "1"}, {Key: "a", Value: "é"}, {Key: "a", Value: "z"}},
			"?B=1&a=z&a=%C3%A9&b=1",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			before := slices.Clone(test.query)
			if got := engine.EncodeQuery(test.query); got != test.want {
				t.Errorf("EncodeQuery(%v) = %q, want %q", test.query, got, test.want)
			}
			if !slices.Equal(test.query, before) {
				t.Errorf("EncodeQuery reordered its argument: %v", test.query)
			}
			// The same parameters in another order give the same query.
			reversed := slices.Clone(test.query)
			slices.Reverse(reversed)
			if got := engine.EncodeQuery(reversed); got != test.want {
				t.Errorf("EncodeQuery(%v) = %q, want %q", reversed, got, test.want)
			}
		})
	}
}

func TestUpsert(t *testing.T) {
	query := []engine.Filter{{Key: "species", Value: "cat"}, {Key: "limit", Value: "5"}, {Key: "city", Value: "Bonn"}, {Key: "limit", Value: "7"}}
	before := slices.Clone(query)

	got := engine.Upsert(query, "limit", "100")
	want := []engine.Filter{{Key: "species", Value: "cat"}, {Key: "limit", Value: "100"}, {Key: "city", Value: "Bonn"}}
	if !slices.Equal(got, want) {
		t.Errorf("replacing: got %v, want %v", got, want)
	}
	got = engine.Upsert(query, "skip", "0")
	if want := append(slices.Clone(before), engine.Filter{Key: "skip", Value: "0"}); !slices.Equal(got, want) {
		t.Errorf("adding: got %v, want %v", got, want)
	}
	if got := engine.Upsert(nil, "limit", "100"); !slices.Equal(got, []engine.Filter{{Key: "limit", Value: "100"}}) {
		t.Errorf("adding to nothing: got %v", got)
	}
	if !slices.Equal(query, before) {
		t.Errorf("Upsert changed its argument: %v", query)
	}
}

func TestURL(t *testing.T) {
	const (
		stagingMain  = "https://api.staging.example.test/v1"
		stagingAdmin = "https://admin.staging.example.test/v1"
	)
	tests := map[string]struct {
		environment string
		force       string
		api         string
		ref         engine.Ref
		query       []engine.Filter
		want        string
	}{
		// Which API a request goes to.
		"a listing goes to the default API": {
			ref: engine.Ref{Resource: "pets"}, want: adminAPI + "/pets",
		},
		"an item goes to the default API for items": {
			ref: engine.Ref{Resource: "pets", ID: "0a1b2c3d"}, want: mainAPI + "/pets/0a1b2c3d",
		},
		"a resource names its API": {
			ref: engine.Ref{Resource: "breeds"}, want: mainAPI + "/breeds",
		},
		"a sub-resource names its API, with an ID": {
			ref: engine.Ref{Resource: "pets", ID: "0a1b2c3d", Sub: "visits"}, want: adminAPI + "/pets/0a1b2c3d/visits",
		},
		"the API of the sub-resource beats that of the resource": {
			ref: engine.Ref{Resource: "breeds", ID: "0a1b2c3d", Sub: "visits"}, want: adminAPI + "/breeds/0a1b2c3d/visits",
		},
		"a sub-resource without an API follows the resource": {
			ref: engine.Ref{Resource: "breeds", ID: "0a1b2c3d", Sub: "access"}, want: mainAPI + "/breeds/0a1b2c3d/access",
		},
		"and the default for items": {
			ref: engine.Ref{Resource: "orders", ID: "0a1b2c3d", Sub: "access"}, want: mainAPI + "/orders/0a1b2c3d/access",
		},
		"the API a request names beats the resource and the sub-resource": {
			api: "admin", ref: engine.Ref{Resource: "breeds", ID: "0a1b2c3d"}, want: adminAPI + "/breeds/0a1b2c3d",
		},
		"a forced API beats them all": {
			force: "main", api: "admin", ref: engine.Ref{Resource: "pets", ID: "0a1b2c3d", Sub: "visits"}, want: mainAPI + "/pets/0a1b2c3d/visits",
		},
		"a forced API for a listing": {
			force: "main", ref: engine.Ref{Resource: "pets"}, want: mainAPI + "/pets",
		},
		"another environment, a listing": {
			environment: "staging", ref: engine.Ref{Resource: "pets"}, want: stagingAdmin + "/pets",
		},
		"another environment, an item": {
			environment: "staging", ref: engine.Ref{Resource: "pets", ID: "0a1b2c3d"}, want: stagingMain + "/pets/0a1b2c3d",
		},
		"the first environment by name": {
			environment: "production", ref: engine.Ref{Resource: "breeds"}, want: mainAPI + "/breeds",
		},
		"a resource the definition does not describe": {
			ref: engine.Ref{Resource: "leashes", ID: "7"}, want: mainAPI + "/leashes/7",
		},

		// What the URL is made of.
		"the path of a resource": {
			ref: engine.Ref{Resource: "kennels", ID: "k1"}, want: mainAPI + "/boarding/kennels/k1",
		},
		"a query, sorted": {
			ref:   engine.Ref{Resource: "pets"},
			query: []engine.Filter{{Key: "species", Value: "long hair"}, {Key: "limit", Value: "100"}},
			want:  adminAPI + "/pets?limit=100&species=long+hair",
		},
		"a query for an item": {
			ref:   engine.Ref{Resource: "pets", ID: "0a1b2c3d", Sub: "visits"},
			query: []engine.Filter{{Key: "reason", Value: "checkup"}},
			want:  adminAPI + "/pets/0a1b2c3d/visits?reason=checkup",
		},
		// An ID is one segment of the path, whatever it holds.
		"an ID cannot leave its place in the path": {
			ref: engine.Ref{Resource: "pets", ID: "../owners/1?admin=true#x y"}, want: mainAPI + "/pets/..%2Fowners%2F1%3Fadmin=true%23x%20y",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, func(_ *definition.Document, o *engine.Options) {
				o.Environment, o.ForceAPI = test.environment, test.force
			})
			got, err := shop.URL(test.api, test.ref, test.query)
			if err != nil {
				t.Fatalf("URL: %v", err)
			}
			if got != test.want {
				t.Errorf("URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestURLOfAnAPITheDefinitionLacks(t *testing.T) {
	shop := open(t, func(doc *definition.Document, _ *engine.Options) {
		doc.Defaults.API = "search"
	})
	_, err := shop.URL("", engine.Ref{Resource: "pets"}, nil)
	wantError(t, err, apikit.KindDefinition, `the definition has no API "search" to reach pets`)

	_, err = shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
	wantError(t, err, apikit.KindDefinition, `no API "search"`)
	wantSent(t, shop.api)
}
