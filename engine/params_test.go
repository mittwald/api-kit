package engine_test

import (
	"maps"
	"math"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

func TestParamsTakeTheTypeOfTheirParameter(t *testing.T) {
	stamped := func(rest string) string {
		return `{"label":"ball","count":1` + rest + `,"literal":"${count} {} []","braces":{"in":"a } and a { and a \\"}}`
	}
	accepted := map[string]struct {
		params map[string]any
		want   string
	}{
		"the types of a command line": {
			map[string]any{"label": "ball", "count": int64(1), "ratio": 2.5, "loud": true, "tags": []string{"a", "b"}},
			stamped(`,"ratio":2.5,"loud":true,"tags":["a","b"]`),
		},
		// A flag is given or not.
		"a flag that is false was not given": {
			map[string]any{"label": "ball", "count": int64(1), "loud": false},
			stamped(``),
		},
		"a value that is nil was not given": {
			map[string]any{"label": "ball", "count": int64(1), "ratio": nil},
			stamped(``),
		},
		"a number written as text": {
			map[string]any{"label": "ball", "count": " +1 ", "ratio": "1e3"},
			stamped(`,"ratio":1000`),
		},
		"a whole number for a number": {
			map[string]any{"label": "ball", "count": 1, "ratio": int64(2)},
			stamped(`,"ratio":2`),
		},
		"a number without a fraction for a whole number": {
			map[string]any{"label": "ball", "count": 1.0},
			stamped(``),
		},
		"one value for a list": {
			map[string]any{"label": "ball", "count": int64(1), "tags": "a"},
			stamped(`,"tags":["a"]`),
		},
		"a number for a text": {
			map[string]any{"label": 7, "count": int64(1)},
			strings.Replace(stamped(``), `"ball"`, `"7"`, 1),
		},
	}
	for name, test := range accepted {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, payloadOverlay)
			shop.api.Handle("PUT", "/v1/toys/"+toy+"/stamp", noContent)
			if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "stamp", Params: test.params}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, "PUT "+mainAPI+"/toys/"+toy+"/stamp "+test.want)
		})
	}

	refused := map[string]struct {
		params  map[string]any
		message string
	}{
		// JSON cannot hold them, so the API would get no number at all.
		"a NaN":                             {map[string]any{"ratio": math.NaN()}, "--ratio must be of type float"},
		"an infinity":                       {map[string]any{"ratio": math.Inf(-1)}, "--ratio must be of type float"},
		"a NaN written as text":             {map[string]any{"ratio": "nan"}, "--ratio must be of type float"},
		"a fraction for a whole number":     {map[string]any{"count": 2.5}, "--count must be of type int"},
		"a text that is no number":          {map[string]any{"count": "two"}, "--count must be of type int"},
		"a whole number that is too big":    {map[string]any{"count": "9223372036854775808"}, "--count must be of type int"},
		"a flag for a text":                 {map[string]any{"label": true}, "--label must be of type str"},
		"a list for a text":                 {map[string]any{"label": []string{"a"}}, "--label must be of type str"},
		"a text for a flag":                 {map[string]any{"loud": "true"}, "--loud must be true or false"},
		"a list without values":             {map[string]any{"tags": []string{}}, "--tags must be a value or a list of values"},
		"a list of what is no text":         {map[string]any{"tags": []any{true}}, "--tags must be a value or a list of values"},
		"a parameter and its alias at once": {map[string]any{"food": "hay", "food-id": "oats"}, "--food is given more than once"},
	}
	for name, test := range refused {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, payloadOverlay)
			in := engine.CallInput{Resource: "toys", ID: toy, Call: "stamp", Params: test.params}
			if _, isCare := test.params["food"]; isCare {
				in = engine.CallInput{Resource: "pets", ID: rex, Call: "care", Params: test.params}
			}
			_, err := shop.Apply(t.Context(), in)
			wantError(t, err, apikit.KindUsage, test.message)
			wantRequests(t, shop.api)
		})
	}
}

func TestParamsAreCheckedBeforeAnythingIsSent(t *testing.T) {
	// Forty characters, some of which take more than one byte.
	fits := strings.Repeat("ä", 38) + "ok"
	book := func(params map[string]any) engine.CallInput {
		given := map[string]any{"title": "Checkup", "text": "See you."}
		maps.Copy(given, params)
		return engine.CallInput{Resource: "appointments", Call: "book", Params: given}
	}
	tests := map[string]struct {
		in      engine.CallInput
		message string
	}{
		"a value that is no choice": {
			engine.CallInput{Resource: "owners", ID: owner, Call: "suspend", Params: map[string]any{"reason": "theft"}},
			"--reason theft is not possible here, choose from fraud, debt",
		},
		// The roles depend on what the appointment is about.
		"a choice of the other parameter": {
			book(map[string]any{"owner": owner, "role": "sitter"}),
			"--role sitter is not possible here, choose from keeper, payer",
		},
		"a value that is too long": {
			engine.CallInput{Resource: "appointments", ID: appointment, Call: "add", Params: map[string]any{"text": fits + "!"}},
			"--text has 41 characters, at most 40 are possible",
		},
		"none of a group of which one is needed": {
			book(map[string]any{"role": "keeper"}),
			"book needs exactly one of --owner, --pet",
		},
		"two of a group of which one is needed": {
			book(map[string]any{"pet": rex, "owner": owner, "role": "keeper"}),
			"book needs exactly one of --owner, --pet",
		},
		"none of the second group": {
			book(map[string]any{"pet": rex}),
			"book needs exactly one of --role, --vet",
		},
		"two parameters that exclude each other": {
			engine.CallInput{Resource: "pets", ID: rex, Call: "care-food", Params: map[string]any{"food": "hay", "found-food": "grass"}},
			"care-food takes only one of --food, --found-food",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			_, err := shop.Apply(t.Context(), test.in)
			wantError(t, err, apikit.KindUsage, test.message)
			wantRequests(t, shop.api)
		})
	}

	// While none of the parameters is given that the choices depend on,
	// every value of theirs is possible.
	t.Run("choices that depend on parameters that were not given", func(t *testing.T) {
		shop := callShop(t, nil, `
resources:
  toys:
    setters:
      - name: sort
        payloadTemplate: {bin: "${bin}"}
        parameters:
          - {name: small, type: bool, required: false}
          - {name: large, type: bool, required: false}
          - {name: bin, choices: {small: [box, bag], large: [crate, box]}}
`)
		shop.api.Handle("POST", "/v1/toys/"+toy, noContent)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "sort", Params: map[string]any{"bin": "shelf"}})
		wantError(t, err, apikit.KindUsage, "--bin shelf is not possible here, choose from box, bag, crate")
		_, err = shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "sort", Params: map[string]any{"bin": "crate", "small": true}})
		wantError(t, err, apikit.KindUsage, "--bin crate is not possible here, choose from box, bag")
		// The first of them that is given decides.
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "sort", Params: map[string]any{"bin": "bag", "small": true, "large": true}}); err != nil {
			t.Errorf("Apply: %v", err)
		}
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "sort", Params: map[string]any{"bin": "crate"}}); err != nil {
			t.Errorf("Apply: %v", err)
		}
	})

	t.Run("a value of the longest length passes", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("POST", "/v1/appointments/"+appointment+"/notes", noContent)
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", ID: appointment, Call: "add", Params: map[string]any{"text": fits}}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	})

	t.Run("a choice passes", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("POST", "/v1/owners/"+owner+"/actions/suspend", noContent)
		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "owners", ID: owner, Call: "suspend", Params: map[string]any{"reason": "debt"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api, "POST "+mainAPI+"/owners/"+owner+`/actions/suspend {"reason":"debt"}`)
		wantMessages(t, result, "Suspended owner "+owner)
	})
}

func TestParamsThatNameAnItem(t *testing.T) {
	const byNumber = adminAPI + "/owners?number=4711"
	notes := adminAPI + "/appointments/" + appointment + "/notes"

	t.Run("a name becomes the ID, and the variables follow", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byNumber, apitest.Response{Raw: `[{"id":"` + owner + `","number":4711}]`})
		shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Body: map[string]any{"appointmentId": appointment}})
		shop.api.Handle("POST", notes, apitest.Response{Body: map[string]any{"noteId": "0a0a0a01"}})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", Call: "book",
			Params: map[string]any{"owner": "4711", "role": "payer", "title": "Invoice", "text": "Due on Monday.", "room-id": "0a0a0a03"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// The parameter that was given sets what the appointment is
		// about; the payload gets the ID, not the number.
		about := `{"kind":"owner","id":"` + owner + `"}`
		wantRequests(t, shop.api,
			"GET "+byNumber,
			"POST "+mainAPI+`/appointments {"title":"Invoice","roomId":"0a0a0a03","about":`+about+`,"sharedWith":`+about+`,"notify":["owner_payer"]}`,
			"POST "+notes+` {"text":"Due on Monday."}`)
	})

	t.Run("a name that leads to no item sends nothing", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byNumber, apitest.Response{Raw: `[]`})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", Call: "book",
			Params: map[string]any{"owner": "4711", "role": "payer", "title": "Invoice", "text": "Due on Monday."}})
		wantError(t, err, apikit.KindNotFound, "unable to find owner 4711")
		wantRequests(t, shop.api, "GET "+byNumber)
	})

	// The resource a value names may depend on another parameter.
	lending := `
resources:
  toys:
    setters:
      - name: lend
        pathTemplate: "toys/${id}/lend"
        payloadTemplate: {to: "${to}", kind: "${kind}"}
        parameters:
          - {name: kind, choices: [pet, owner, shelter]}
          - {name: to, resource: {parameter: kind, resources: {pet: pets, owner: owners}}}
`
	tests := map[string]struct {
		kind, to string
		lookup   string
		want     string
	}{
		"an owner":                      {"owner", "4711", byNumber, owner},
		"a pet":                         {"pet", "REX-7", adminAPI + "/pets?search=%22REX-7%22", rex},
		"a kind that names no resource": {"shelter", "4711", "", "4711"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, lending)
			lent := "POST " + mainAPI + "/toys/" + toy + `/lend {"to":"` + test.want + `","kind":"` + test.kind + `"}`
			want := []string{lent}
			if test.lookup != "" {
				shop.api.Handle("GET", test.lookup, apitest.Response{Raw: `[{"id":"` + test.want + `","number":4711,"tag":"REX-7"}]`})
				want = []string{"GET " + test.lookup, lent}
			}
			shop.api.Handle("POST", "/v1/toys/"+toy+"/lend", noContent)

			if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "lend", Params: map[string]any{"kind": test.kind, "to": test.to}}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, want...)
		})
	}
}
