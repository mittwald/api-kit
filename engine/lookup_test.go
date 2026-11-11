package engine_test

import (
	"slices"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

// carersURL lists the carers of an owner that a vet is looked up among.
const carersURL = mainAPI + "/owners/" + owner + "/carers?active=true&limit=100&skip=0"

// carers are the carers of an owner. A guest and one without a role cannot
// be chosen as vet.
const carers = `[
	{"staffId":"` + ada + `","email":"ada@example.test","role":"keeper"},
	{"staffId":"0e0e0e02","email":"bob@example.test","role":"guest"},
	{"staffId":"0e0e0e03","email":"Cy@example.test","role":"payer"},
	{"staffId":"0e0e0e04","email":"dee@example.test","role":"none"}]`

// giftOverlay gives the toys a call with a lookup in a plain listing, which
// has a filter the API applies and no field for completion.
const giftOverlay = `
resources:
  toys:
    setters:
      - name: gift
        pathTemplate: "toys/${id}/gift"
        payloadTemplate: {breedId: "${breed}"}
        parameters:
          - {name: species}
          - name: breed
            lookup: {get: breeds, filters: ["species=${species}"], match: [name, code], select: id}
`

func TestLookup(t *testing.T) {
	about := `{"kind":"owner","id":"` + owner + `"}`
	booked := func(vet string) string {
		return "POST " + mainAPI + `/appointments {"title":"Checkup","about":` + about + `,"sharedWith":` + about + `,"vetId":"` + vet + `"}`
	}
	tests := map[string]struct {
		vet    string
		carers apitest.Response
		// want is the ID that is sent, or the error.
		want    string
		kind    apikit.Kind
		message string
	}{
		"by a field the lookup matches":     {vet: "ada@example.test", carers: apitest.Response{Raw: carers}, want: ada},
		"whatever its case":                 {vet: "CY@EXAMPLE.TEST", carers: apitest.Response{Raw: carers}, want: "0e0e0e03"},
		"by another field":                  {vet: "0e0e0e03", carers: apitest.Response{Raw: carers}, want: "0e0e0e03"},
		"one item that matches in two ways": {vet: "x", carers: apitest.Response{Raw: `[{"staffId":"x","email":"X"},{"staffId":"x","email":"x"}]`}, want: "x"},
		"a number equals its text":          {vet: "7", carers: apitest.Response{Raw: `[{"staffId":7,"email":"seven@example.test"}]`}, want: "7"},
		"an item that the filters drop": {
			vet: "bob@example.test", carers: apitest.Response{Raw: carers},
			kind: apikit.KindUsage, message: "--vet bob@example.test matches none of the carers that may be chosen",
		},
		"a value that only starts like a field": {
			vet: "ada@example", carers: apitest.Response{Raw: carers},
			kind: apikit.KindUsage, message: "--vet ada@example matches none of the carers that may be chosen",
		},
		"two items": {
			vet: "ada@example.test", carers: apitest.Response{Raw: `[{"staffId":"a","email":"ada@example.test"},{"staffId":"b","email":"ADA@example.test"}]`},
			kind: apikit.KindUsage, message: "--vet ada@example.test matches more than one of the carers that may be chosen",
		},
		// Nothing could be sent for it.
		"an item without the field to select": {
			vet: "ada@example.test", carers: apitest.Response{Raw: `[{"email":"ada@example.test"},{"staffId":null,"email":"ada@example.test"}]`},
			kind: apikit.KindUsage, message: "--vet ada@example.test matches none of the carers that may be chosen",
		},
		"null is nothing, not a text": {
			vet: "None", carers: apitest.Response{Raw: `[{"staffId":"a","email":null}]`},
			kind: apikit.KindUsage, message: "--vet None matches none of the carers that may be chosen",
		},
		"a listing that is empty": {
			vet: "ada@example.test", carers: apitest.Response{Raw: `[]`},
			kind: apikit.KindUsage, message: "--vet ada@example.test matches none of the carers that may be chosen",
		},
		"a listing the API refuses": {
			vet: "ada@example.test", carers: apitest.Response{Status: 500, Raw: "boom"},
			kind: apikit.KindFailed, message: "failed to fetch owner by ID " + owner + ": 500 boom",
		},
		"a listing that does not exist": {
			vet: "ada@example.test", carers: apitest.Response{Status: 404},
			kind: apikit.KindNotFound, message: "failed to fetch owner by ID " + owner + ": 404",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", carersURL, test.carers)
			shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Body: map[string]any{"appointmentId": appointment}})
			shop.api.Handle("POST", "/v1/appointments/"+appointment+"/notes", apitest.Response{Body: map[string]any{"noteId": "0a0a0a01"}})

			_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", Call: "book",
				Params: map[string]any{"owner": owner, "vet": test.vet, "title": "Checkup", "text": "See you."}})
			if test.message != "" {
				wantError(t, err, test.kind, test.message)
				// The listing is read, nothing is changed.
				wantRequests(t, shop.api, "GET "+carersURL)
				return
			}
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := requestLines(t, shop.api); len(got) != 3 || got[0] != "GET "+carersURL || got[1] != booked(test.want) {
				t.Errorf("requests sent: %q, want the listing, then %s", got, booked(test.want))
			}
		})
	}

	t.Run("a filter of the lookup goes to the API", func(t *testing.T) {
		const dogs = mainAPI + "/breeds?limit=100&skip=0&species=dog"
		shop := callShop(t, nil, giftOverlay)
		shop.api.Handle("GET", dogs, apitest.Response{Raw: `[{"id":"` + beagle + `","name":"Beagle","code":"BGL"},{"id":"0c0c0c02","name":"Boxer","code":"BXR"}]`})
		shop.api.Handle("POST", "/v1/toys/"+toy+"/gift", noContent)

		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "gift", Params: map[string]any{"species": "dog", "breed": "bgl"}}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api, "GET "+dogs, "POST "+mainAPI+"/toys/"+toy+`/gift {"breedId":"`+beagle+`"}`)

		// Without a sub-resource the message names the listing itself.
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "gift", Params: map[string]any{"species": "dog", "breed": "poodle"}})
		wantError(t, err, apikit.KindUsage, "--breed poodle matches none of the breeds that may be chosen")
	})
}

func TestCompleteLookup(t *testing.T) {
	const byNumber = adminAPI + "/owners?number=4711"
	tests := map[string]struct {
		params map[string]any
		prefix string
		carers string
		want   []string
		sent   []string
	}{
		// Sorted, and without the carers that cannot be chosen.
		"the field for completion of every item": {
			params: map[string]any{"owner": owner}, carers: carers,
			want: []string{"Cy@example.test", "ada@example.test"}, sent: []string{"GET " + carersURL},
		},
		"what starts with the prefix": {
			params: map[string]any{"owner": owner}, prefix: "ad", carers: carers,
			want: []string{"ada@example.test"}, sent: []string{"GET " + carersURL},
		},
		"a prefix nothing starts with": {
			params: map[string]any{"owner": owner}, prefix: "Ad", carers: carers,
			sent: []string{"GET " + carersURL},
		},
		"a name is resolved first": {
			params: map[string]any{"owner": "4711"}, carers: carers,
			want: []string{"Cy@example.test", "ada@example.test"}, sent: []string{"GET " + byNumber, "GET " + carersURL},
		},
		// Each value once, and none that counts as nothing.
		"values that are nothing are not offered": {
			params: map[string]any{"owner": owner},
			carers: `[{"email":""},{"email":null},{"staffId":"a"},{"email":0},{"email":false},{"email":"a@example.test"},{"email":"a@example.test"},{"email":7}]`,
			want:   []string{"7", "a@example.test"}, sent: []string{"GET " + carersURL},
		},
		// Nothing says yet whose carers to list.
		"a parameter the lookup depends on is not given yet": {
			params: map[string]any{"role": "keeper", "title": "Checkup"},
		},
		"no parameter at all": {},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", byNumber, apitest.Response{Raw: `[{"id":"` + owner + `","number":4711}]`})
			shop.api.Handle("GET", carersURL, apitest.Response{Raw: test.carers})

			got, err := shop.CompleteLookup(t.Context(), "appointments", "book", "vet", test.params, test.prefix)
			if err != nil {
				t.Fatalf("CompleteLookup: %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("offered %q, want %q", got, test.want)
			}
			wantSent(t, shop.api, test.sent...)
		})
	}

	t.Run("the first field to match, without one for completion", func(t *testing.T) {
		shop := callShop(t, nil, giftOverlay)
		shop.api.Handle("GET", mainAPI+"/breeds?limit=100&skip=0&species=dog", apitest.Response{Raw: `[{"id":"` + beagle + `","name":"Beagle","code":"BGL"},{"id":"0c0c0c02","name":"Boxer","code":"BXR"}]`})

		got, err := shop.CompleteLookup(t.Context(), "toys", "gift", "breed", map[string]any{"species": "dog"}, "Bo")
		if err != nil || !slices.Equal(got, []string{"Boxer"}) {
			t.Errorf("offered %q, %v, want Boxer", got, err)
		}
	})

	t.Run("a parameter without a lookup offers nothing", func(t *testing.T) {
		shop := callShop(t, nil)
		for _, parameter := range []string{"role", "nope"} {
			got, err := shop.CompleteLookup(t.Context(), "appointments", "book", parameter, map[string]any{"owner": owner}, "")
			if err != nil || got != nil {
				t.Errorf("CompleteLookup(%s) = %q, %v, want nothing", parameter, got, err)
			}
		}
		wantSent(t, shop.api)
	})

	// The caller offers nothing then; the error says why.
	failures := map[string]struct {
		call    string
		params  map[string]any
		carers  apitest.Response
		kind    apikit.Kind
		message string
	}{
		"a call the resource does not have": {
			call: "nope", kind: apikit.KindUsage, message: "nope is not available for appointments",
		},
		"a parameter the call does not have": {
			call: "book", params: map[string]any{"color": "red"}, kind: apikit.KindUsage, message: "book has no parameter color",
		},
		"a name that leads to no item": {
			call: "book", params: map[string]any{"pet": "REX-7"}, kind: apikit.KindNotFound, message: "unable to find pet REX-7",
		},
		"a listing the API refuses": {
			call: "book", params: map[string]any{"owner": owner}, carers: apitest.Response{Status: 403, Raw: "no"},
			kind: apikit.KindFailed, message: "failed to fetch owner by ID " + owner + ": 403 no",
		},
	}
	for name, test := range failures {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", adminAPI+"/pets?search=%22REX-7%22", apitest.Response{Raw: `[]`})
			shop.api.Handle("GET", carersURL, test.carers)

			got, err := shop.CompleteLookup(t.Context(), "appointments", test.call, "vet", test.params, "")
			wantError(t, err, test.kind, test.message)
			if got != nil {
				t.Errorf("offered %q with an error", got)
			}
		})
	}
}
