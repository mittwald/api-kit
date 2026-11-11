package engine_test

import (
	"maps"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

// The requests of the tests of conditions: the appointment a note is added
// to, and the request that adds it.
const (
	appointmentURL = mainAPI + "/appointments/" + appointment
	notesURL       = adminAPI + "/appointments/" + appointment + "/notes"
	noted          = "POST " + notesURL + ` {"text":"Hello"}`
)

func TestConditionsOnTheItem(t *testing.T) {
	const item = `{"id":"` + appointment + `","status":"open","createdAt":"2026-02-03T09:30:00Z","rooms":2,"urgent":false,
		"vet":{"name":"Ada"},"visits":[{"reason":"checkup","date":"2026-01-10"},{"reason":"fleas","date":"2026-02-01"}]}`
	tests := map[string]struct {
		conditions []string
		// skipped is the message of a call that does not run.
		skipped string
	}{
		"conditions that hold":         {conditions: []string{"status!=closed", "createdAt>=2026-01-31", "rooms>1", "urgent=false", "closedAt="}},
		"a path into a list":           {conditions: []string{"visits[reason=fleas].date>2026-01-31", " vet.name =Ada"}},
		"one that does not hold":       {[]string{"status=closed"}, "Skipped add: status=closed does not hold, status is open"},
		"the first that does not hold": {[]string{"status=open", "rooms>=3", "status=closed"}, "Skipped add: rooms>=3 does not hold, rooms is 2"},
		"a field without a value":      {[]string{"closedAt>=2026-01-01"}, "Skipped add: closedAt>=2026-01-01 does not hold, closedAt is empty"},
		"a field that is no text":      {[]string{"vet=Ada"}, `Skipped add: vet=Ada does not hold, vet is {"name": "Ada"}`},
		"a flag":                       {[]string{"urgent=true"}, "Skipped add: urgent=true does not hold, urgent is false"},
		"a path as it was written":     {[]string{" visits[reason=checkup].date >=2026-01-31"}, "Skipped add:  visits[reason=checkup].date >=2026-01-31 does not hold, visits[reason=checkup].date is 2026-01-10"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", appointmentURL, apitest.Response{Raw: item})
			shop.api.Handle("POST", notesURL, noContent)

			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", ID: appointment, Call: "add",
				Params: map[string]any{"text": "Hello"}, If: test.conditions})
			if test.skipped == "" {
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				// The item is fetched once for all its conditions.
				wantRequests(t, shop.api, "GET "+appointmentURL, noted)
				return
			}
			// Nothing is done, on purpose; the message says why.
			wantError(t, err, apikit.KindSkipped, test.skipped)
			if err.Error() != test.skipped || result != nil {
				t.Errorf("error %q, result %+v, want %q and no result", err, result, test.skipped)
			}
			wantRequests(t, shop.api, "GET "+appointmentURL)
		})
	}

	for name, test := range map[string]struct {
		answer  apitest.Response
		kind    apikit.Kind
		message string
	}{
		"an item that does not exist":   {apitest.Response{Status: 404}, apikit.KindNotFound, "failed to fetch appointment by ID " + appointment + ": 404"},
		"an item the API does not show": {apitest.Response{Status: 403, Raw: "no"}, apikit.KindFailed, "failed to fetch appointment by ID " + appointment + ": 403 no"},
		"an answer that is no item":     {apitest.Response{Raw: `"open"`}, apikit.KindNotFound, "unable to find appointment " + appointment},
	} {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", appointmentURL, test.answer)
			_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", ID: appointment, Call: "add",
				Params: map[string]any{"text": "Hello"}, If: []string{"status=open"}})
			// A condition that cannot be tested is not one that holds.
			wantError(t, err, test.kind, test.message)
			wantRequests(t, shop.api, "GET "+appointmentURL)
		})
	}

	t.Run("a condition comes before a dry run", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", appointmentURL, apitest.Response{Raw: item})
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", ID: appointment, Call: "add", DryRun: true,
			Params: map[string]any{"text": "Hello"}, If: []string{"status=closed"}})
		wantError(t, err, apikit.KindSkipped, "Skipped add: status=closed does not hold")
	})
}

func TestConditionsOnAParameter(t *testing.T) {
	const byTag = adminAPI + "/pets?search=%22REX-7%22"
	book := func(shop *shop, params map[string]any, conditions ...string) (*engine.CallResult, error) {
		given := map[string]any{"role": "keeper", "title": "Checkup", "text": "See you."}
		maps.Copy(given, params)
		return shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", Call: "book", Params: given, If: conditions})
	}

	t.Run("a path that starts with a parameter tests the item it names", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
		shop.api.Handle("GET", petURL, apitest.Response{Body: map[string]any{"id": rex, "species": "dog", "chip": map[string]any{"valid": true}}})
		shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Body: map[string]any{"appointmentId": appointment}})
		shop.api.Handle("POST", notesURL, noContent)

		if _, err := book(shop, map[string]any{"pet": "REX-7"}, "pet.species=dog", "pet.chip.valid=true"); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// The pet is found by its tag, then fetched once by its ID.
		if got := sent(shop.api); len(got) != 4 || got[0] != "GET "+byTag || got[1] != "GET "+petURL {
			t.Errorf("requests sent: %q, want the search, the pet and two changes", got)
		}

		_, err := book(shop, map[string]any{"pet": "REX-7"}, "pet.species=cat")
		wantError(t, err, apikit.KindSkipped, "Skipped book: pet.species=cat does not hold, pet.species is dog")
		if got := len(sent(shop.api)); got != 4 {
			t.Errorf("%d requests sent, want no more than before", got)
		}
	})

	mistakes := map[string]struct {
		in      engine.CallInput
		message string
	}{
		"no condition": {
			engine.CallInput{Resource: "appointments", ID: appointment, Call: "add", Params: map[string]any{"text": "Hello"}, If: []string{"enabled"}},
			`--if: "enabled" is not a condition: expected a path, one of the operators = != < <= > >= and a value`,
		},
		"a path that is none": {
			engine.CallInput{Resource: "appointments", ID: appointment, Call: "add", Params: map[string]any{"text": "Hello"}, If: []string{"a..b=1"}},
			`--if a..b=1: path "a..b": empty segment`,
		},
		"a parameter that was not given": {
			engine.CallInput{Resource: "appointments", Call: "book", Params: map[string]any{"owner": owner, "role": "payer", "title": "Visit", "text": "Soon."}, If: []string{"pet.species=dog"}},
			"--if pet.species=dog needs --pet",
		},
		// There is no item to test, so the path must lead to one.
		"a field of the item, for a call that takes no ID": {
			engine.CallInput{Resource: "appointments", Call: "book", Params: map[string]any{"owner": owner, "role": "payer", "title": "Visit", "text": "Soon."}, If: []string{"status=open"}},
			"--if status=open: book takes no ID, so a condition tests a parameter: pet.<field>, owner.<field>",
		},
		"a parameter that names no item": {
			engine.CallInput{Resource: "appointments", Call: "book", Params: map[string]any{"owner": owner, "role": "payer", "title": "Visit", "text": "Soon."}, If: []string{"role.name=x"}},
			"--if role.name=x: book takes no ID, so a condition tests a parameter: pet.<field>, owner.<field>",
		},
	}
	for name, test := range mistakes {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			_, err := shop.Apply(t.Context(), test.in)
			wantError(t, err, apikit.KindUsage, test.message)
			wantRequests(t, shop.api)
		})
	}

	t.Run("a call without an ID and without such a parameter", func(t *testing.T) {
		shop := callShop(t, nil, stepOverlay)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", Call: "open", Params: map[string]any{"name": "north"}, If: []string{"status=open"}})
		wantError(t, err, apikit.KindUsage, "--if status=open: open takes no ID, so a condition tests nothing")
	})

	// The resource a parameter names may depend on another parameter,
	// which here says that it names none.
	t.Run("a parameter that names no item this time", func(t *testing.T) {
		shop := callShop(t, nil, `
resources:
  toys:
    setters:
      - name: lend
        parameters:
          - {name: kind}
          - {name: to, resource: {parameter: kind, resources: {pet: pets}}}
`)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "lend",
			Params: map[string]any{"kind": "shelter", "to": "north"}, If: []string{"to.name=North"}})
		wantError(t, err, apikit.KindUsage, "--to names no item of a resource here")
		wantRequests(t, shop.api)
	})

	// A path that starts like a parameter is one of the item when the
	// call has an ID.
	t.Run("a field of the item that is named like a parameter", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", appointmentURL, apitest.Response{Raw: `{"text":{"length":2}}`})
		shop.api.Handle("POST", notesURL, noContent)
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", ID: appointment, Call: "add",
			Params: map[string]any{"text": "Hello"}, If: []string{"text.length=2"}}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	})
}

func TestListChecks(t *testing.T) {
	const (
		listing = adminAPI + "/appointments?about=" + rex + "&limit=100&search=fleas&skip=0"
		early   = `{"id":"0d0d0d01","tag":"A-1","createdAt":"2026-01-01T10:00:00Z"}`
		late    = `{"id":"0d0d0d02","tag":"A-2","createdAt":"2026-02-06T10:00:00Z"}`
		later   = `{"id":"0d0d0d03","tag":"","number":0,"createdAt":"2026-02-07T10:00:00Z"}`
	)
	words := []string{"appointment", "about=@pet", "search=fleas", "createdAt>=2026-01-31"}
	const text = "appointment about=@pet search=fleas createdAt>=2026-01-31"
	book := func(shop *shop, in engine.CallInput) (*engine.CallResult, error) {
		shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Body: map[string]any{"appointmentId": appointment}})
		shop.api.Handle("POST", notesURL, noContent)
		in.Resource, in.Call = "appointments", "book"
		in.Params = map[string]any{"pet": rex, "role": "keeper", "title": "Checkup", "text": "See you."}
		return shop.Apply(t.Context(), in)
	}

	tests := map[string]struct {
		items   string
		in      engine.CallInput
		skipped string
	}{
		"no item that meets all conditions, as required":   {items: `[` + early + `]`, in: engine.CallInput{IfNone: [][]string{words}}},
		"no item at all, as required":                      {items: `[]`, in: engine.CallInput{IfNone: [][]string{words}}},
		"an item that meets all conditions, as required":   {items: `[` + early + `,` + late + `]`, in: engine.CallInput{IfAny: [][]string{words}}},
		"an item where none may be":                        {`[` + early + `,` + late + `]`, engine.CallInput{IfNone: [][]string{words}}, "Skipped book: --if-none " + text + " found A-2"},
		"items where none may be, each by what names it":   {`[` + late + `,` + later + `]`, engine.CallInput{IfNone: [][]string{words}}, "Skipped book: --if-none " + text + " found A-2, 0d0d0d03"},
		"no item where one must be":                        {`[` + early + `]`, engine.CallInput{IfAny: [][]string{words}}, "Skipped book: --if-any " + text + " found nothing"},
		"an empty listing where an item must be":           {`[]`, engine.CallInput{IfAny: [][]string{words}}, "Skipped book: --if-any " + text + " found nothing"},
		"an empty object is no item":                       {`{}`, engine.CallInput{IfAny: [][]string{{"appointments", "about=@pet", "search=fleas"}}}, "Skipped book: --if-any appointments about=@pet search=fleas found nothing"},
		"one object is one item":                           {late, engine.CallInput{IfNone: [][]string{words}}, "Skipped book: --if-none " + text + " found A-2"},
		"the listings that must be empty are read first":   {`[` + late + `]`, engine.CallInput{IfAny: [][]string{words}, IfNone: [][]string{words}}, "Skipped book: --if-none " + text + " found A-2"},
		"a condition comes before a listing":               {`[` + late + `]`, engine.CallInput{IfNone: [][]string{words}, If: []string{"pet.species=cat"}}, "Skipped book: pet.species=cat does not hold, pet.species is dog"},
		"a condition that the API does not take as filter": {`[` + late + `]`, engine.CallInput{IfNone: [][]string{{"appointments", "about=@pet", "search=fleas", "tag!=A-2"}}}, ""},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", listing, apitest.Response{Raw: test.items})
			shop.api.Handle("GET", petURL, apitest.Response{Body: map[string]any{"id": rex, "species": "dog"}})

			result, err := book(shop, test.in)
			if test.skipped == "" {
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				// A filter of the resource goes to the API, with the ID
				// the parameter was resolved to.
				if got := sent(shop.api); len(got) != 3 || got[0] != "GET "+listing {
					t.Errorf("requests sent: %q, want the listing and two changes", got)
				}
				return
			}
			wantError(t, err, apikit.KindSkipped, test.skipped)
			if err.Error() != test.skipped || result != nil {
				t.Errorf("error %q, result %+v, want %q and no result", err, result, test.skipped)
			}
			if got := sent(shop.api); strings.Contains(strings.Join(got, " "), "POST") {
				t.Errorf("requests sent: %q, want nothing changed", got)
			}
		})
	}

	// What a value refers to in the call, and what is sent for it.
	const byTag = adminAPI + "/pets?search=%22REX-7%22"
	references := map[string]struct {
		word, query string
	}{
		"a parameter as it was resolved":      {"about=@pet", "about=" + rex + "&limit=100&skip=0"},
		"a field of the item it names":        {"search=@pet.tag", "limit=100&search=REX-7&skip=0"},
		"a field in depth":                    {"search=@pet.chip.code", "limit=100&search=c-1&skip=0"},
		"an @ that is one":                    {"search=@@night", "limit=100&search=%40night&skip=0"},
		"a text":                              {"status=open", "limit=100&skip=0&status=open"},
		"a value that a filter rule resolves": {"about=REX-7", "about=" + rex + "&limit=100&skip=0"},
	}
	for name, test := range references {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
			shop.api.Handle("GET", petURL, apitest.Response{Raw: `{"id":"` + rex + `","tag":"REX-7","chip":{"code":"c-1"}}`})
			checked := adminAPI + "/appointments?" + test.query
			shop.api.Handle("GET", checked, apitest.Response{Raw: `[]`})

			if _, err := book(shop, engine.CallInput{IfNone: [][]string{{"appointments", test.word}}}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := sent(shop.api); !strings.Contains(strings.Join(got, " "), "GET "+checked+" ") {
				t.Errorf("requests sent: %q, want the listing %s", got, checked)
			}
		})
	}

	t.Run("the ID of the call", func(t *testing.T) {
		const checked = adminAPI + "/appointments?about=" + appointment + "&limit=100&skip=0"
		shop := callShop(t, nil)
		shop.api.Handle("GET", checked, apitest.Response{Raw: `[{"id":"0d0d0d02"}]`})
		shop.api.Handle("POST", notesURL, noContent)

		in := engine.CallInput{Resource: "appointments", ID: appointment, Call: "add", Params: map[string]any{"text": "Hello"}}
		in.IfAny = [][]string{{"appointments", "about=@id"}}
		if _, err := shop.Apply(t.Context(), in); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api, "GET "+checked, noted)
	})

	failures := map[string]struct {
		check   []string
		pet     string
		listing apitest.Response
		kind    apikit.Kind
		message string
	}{
		// Never taken for an empty listing.
		"a listing the API refuses": {
			check: words, listing: apitest.Response{Status: 500, Raw: "boom"},
			kind: apikit.KindFailed, message: "failed to fetch appointments: 500 boom",
		},
		"a listing that does not exist": {
			check: words, listing: apitest.Response{Status: 404},
			kind: apikit.KindNotFound, message: "failed to fetch appointments: 404",
		},
		"a reference to a field the item lacks": {
			check: []string{"appointments", "search=@pet.nickname"}, pet: `{"id":"` + rex + `"}`,
			kind: apikit.KindFailed, message: "@pet.nickname has no value",
		},
		"a reference to a field that is empty": {
			check: []string{"appointments", "search=@pet.nickname"}, pet: `{"id":"` + rex + `","nickname":""}`,
			kind: apikit.KindFailed, message: "@pet.nickname has no value",
		},
		"a reference to a field that is no path": {
			check: []string{"appointments", "search=@pet.a..b"}, pet: `{"id":"` + rex + `"}`,
			kind: apikit.KindFailed, message: "@pet.a..b has no value",
		},
		"a reference to an item that is gone": {
			check: []string{"appointments", "search=@pet.tag"}, pet: `"gone"`,
			kind: apikit.KindNotFound, message: "unable to find pet " + rex,
		},
		// A filter that matches nothing would look like an empty listing.
		"a filter value that names no item": {
			check: []string{"appointments", "about=REX-9"},
			kind:  apikit.KindNotFound, message: "unable to find pet REX-9",
		},
	}
	for name, test := range failures {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", listing, test.listing)
			shop.api.Handle("GET", adminAPI+"/pets?search=%22REX-9%22", apitest.Response{Raw: `[]`})
			shop.api.Handle("GET", petURL, apitest.Response{Raw: test.pet})

			_, err := book(shop, engine.CallInput{IfAny: [][]string{test.check}})
			wantError(t, err, test.kind, test.message)
			if got := sent(shop.api); strings.Contains(strings.Join(got, " "), "POST") {
				t.Errorf("requests sent: %q, want nothing changed", got)
			}
		})
	}
}

func TestListCheckMistakes(t *testing.T) {
	tests := map[string]struct {
		words   []string
		message string
	}{
		// Without a filter the whole listing would be read.
		"no filter of the resource":      {[]string{"appointments", "createdAt>=2026-01-31"}, "--if-none appointments createdAt>=2026-01-31: needs one filter of appointments, e.g. about=, search=, status="},
		"a filter that only orders":      {[]string{"appointments", "sort=createdAt"}, "--if-none appointments sort=createdAt: needs one filter of appointments, e.g. about=, search=, status="},
		"a filter that is not an =":      {[]string{"appointments", "status!=open"}, "needs one filter of appointments"},
		"a filter without a value":       {[]string{"appointments", "about="}, "--if-none appointments about=: filter about has no value"},
		"a parameter that was not given": {[]string{"appointments", "about=@owner"}, "--if-none appointments about=@owner: @owner needs --owner"},
		"a name the call does not know":  {[]string{"appointments", "about=@nope"}, "--if-none appointments about=@nope: @nope names no parameter of the call, nor a field of an item one names"},
		"a field of what is no item":     {[]string{"appointments", "about=@title.x"}, "@title.x names no parameter of the call"},
		"a field of the ID":              {[]string{"appointments", "about=@id.x"}, "@id.x names no parameter of the call"},
		"the ID of a call without one":   {[]string{"appointments", "about=@id"}, "--if-none appointments about=@id: @id needs a call with an ID"},
		"no condition":                   {[]string{"appointments", "about"}, "--if-none appointments about: expected conditions like status=open, ownerId=@owner or 'createdAt>=2026-01-31', got about"},
		"a path that is none":            {[]string{"appointments", "about=@pet", "a..b=1"}, `--if-none appointments about=@pet a..b=1: path "a..b": empty segment`},
		"a resource nobody described":    {[]string{"toys", "a=1"}, "--if-none toys a=1: toys is no resource that can be listed"},
		"a resource that is not listed":  {[]string{"receipt", "a=1"}, "--if-none receipt a=1: receipt is no resource that can be listed"},
		"nothing":                        {nil, "--if-none : expected a resource and conditions"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", Call: "book", IfNone: [][]string{test.words},
				Params: map[string]any{"pet": rex, "role": "keeper", "title": "Checkup", "text": "See you."}})
			// Found before anything is sent.
			wantError(t, err, apikit.KindUsage, test.message)
			wantRequests(t, shop.api)
		})
	}

	t.Run("the option is named", func(t *testing.T) {
		shop := callShop(t, nil)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", Call: "book", IfAny: [][]string{{"appointments", "about="}},
			Params: map[string]any{"pet": rex, "role": "keeper", "title": "Checkup", "text": "See you."}})
		wantError(t, err, apikit.KindUsage, "--if-any appointments about=: filter about has no value")
	})
}
