package engine_test

import (
	"bytes"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

// The requests of the steps that find a keeper of Rex's owner and get a
// token to act for them, and the token.
const (
	petURL      = mainAPI + "/pets/" + rex
	keepersURL  = mainAPI + "/owners/" + owner + "/keepers?limit=100&role=guardian&skip=0"
	delegateURL = mainAPI + "/staff/" + ada + "/delegate"
	adasToken   = "t0ken-of-ada"
	kennel      = "0f0e0d0c"
)

// scriptKeeper scripts the answers to the steps of "care": Rex belongs to
// an owner whose first guardian that is no robot is Ada.
func scriptKeeper(api *apitest.Server) {
	api.Handle("GET", petURL, apitest.Response{Body: map[string]any{"id": rex, "ownerId": owner}})
	api.Handle("GET", keepersURL, apitest.Response{Raw: `[
		{"staffId":"0e0e0e01","role":"guardian","name":"robot"},
		{"staffId":"` + ada + `","role":"guardian","name":"Ada"},
		{"staffId":"0e0e0e02","role":"guardian","name":"Bob"}]`})
	api.Handle("POST", delegateURL, apitest.Response{Body: map[string]any{"token": adasToken}})
}

func TestStepsSupplyTheToken(t *testing.T) {
	shop := callShop(t, nil)
	scriptKeeper(shop.api)
	shop.api.Handle("PATCH", petURL, noContent)

	result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care", Params: map[string]any{"food": "hay"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// One item is fetched as it is, a listing with the filter of the step
	// and to its end; the call of a step is sent like any other.
	wantRequests(t, shop.api,
		"GET "+petURL,
		"GET "+keepersURL,
		"POST "+delegateURL+` {"staffId":"`+ada+`"}`,
		"PATCH "+petURL+` {"food":"hay"}`)
	// Only the change carries the token of the keeper.
	if got, want := authorizations(shop.api), []string{"Bearer own", "Bearer own", "Bearer own", "Bearer " + adasToken}; !slices.Equal(got, want) {
		t.Errorf("credentials sent: %q, want %q", got, want)
	}
	// The message sees the step parameter with its default and what the
	// steps kept.
	wantMessages(t, result, "Changed food=hay for pet "+rex+" as guardian "+ada)
}

func TestStepsOfASharedCall(t *testing.T) {
	t.Run("setters with the same steps run them once", func(t *testing.T) {
		shop := callShop(t, nil)
		scriptKeeper(shop.api)
		shop.api.Handle("PATCH", petURL, noContent)

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care",
			Params: map[string]any{"portions": int64(2), "breed": beagle, "pure": true}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api,
			"GET "+petURL, "GET "+keepersURL, "POST "+delegateURL+` {"staffId":"`+ada+`"}`,
			"PATCH "+petURL+` {"portions":2}`,
			"PATCH "+petURL+` {"breeds":{"`+beagle+`":{"pure":true}}}`)
		if got := authorizations(shop.api)[3:]; !slices.Equal(got, []string{"Bearer " + adasToken, "Bearer " + adasToken}) {
			t.Errorf("credentials of the changes: %q, want the token of the keeper twice", got)
		}
		wantMessages(t, result,
			"Changed portions=2 for pet "+rex+" as guardian "+ada,
			"Changed breed "+beagle+" of pet "+rex+" as guardian "+ada)

		// The next call asks again: a token is good for one call.
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care", Params: map[string]any{"food": "hay"}}); err != nil {
			t.Fatalf("Apply again: %v", err)
		}
		if got := requestLines(t, shop.api)[5:]; !slices.Equal(got, []string{"POST " + delegateURL + ` {"staffId":"` + ada + `"}`, "PATCH " + petURL + ` {"food":"hay"}`}) {
			t.Errorf("requests of the second call: %q, want the token asked for again", got)
		}
	})

	t.Run("a setter without steps uses the token of the engine", func(t *testing.T) {
		shop := callShop(t, nil)
		scriptKeeper(shop.api)
		shop.api.Handle("PATCH", "/v1/pets/"+rex, noContent)

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care",
			Params: map[string]any{"portions": int64(2), "found-food": "grass"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api,
			"GET "+petURL, "GET "+keepersURL, "POST "+delegateURL+` {"staffId":"`+ada+`"}`,
			"PATCH "+petURL+` {"portions":2}`,
			"PATCH "+adminAPI+"/pets/"+rex+` {"foundFood":"grass"}`)
		if got := authorizations(shop.api)[3:]; !slices.Equal(got, []string{"Bearer " + adasToken, "Bearer own"}) {
			t.Errorf("credentials of the changes: %q, want the token of the keeper, then that of the engine", got)
		}
		wantMessages(t, result, "Changed portions=2 for pet "+rex+" as guardian "+ada, "Changed foundFood=grass for pet "+rex)
	})

	t.Run("a step parameter that is given replaces its default", func(t *testing.T) {
		shop := callShop(t, nil)
		scriptKeeper(shop.api)
		sitters := strings.Replace(keepersURL, "guardian", "sitter", 1)
		shop.api.Handle("GET", sitters, apitest.Response{Raw: `[{"staffId":"` + ada + `","role":"sitter","name":"Ada"}]`})
		shop.api.Handle("PATCH", petURL, noContent)

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care", Params: map[string]any{"food": "hay", "as-role": "sitter"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if got := sent(shop.api)[1]; got != "GET "+sitters {
			t.Errorf("the keepers were asked for with %s, want %s", got, "GET "+sitters)
		}
		wantMessages(t, result, "Changed food=hay for pet "+rex+" as sitter "+ada)
	})

	// The body of an answer may end in a line break. What follows it in
	// the message stays on the same line.
	t.Run("a step that fails ends the call, in one line", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", petURL, apitest.Response{Body: map[string]any{"id": rex, "ownerId": owner}})
		shop.api.Handle("GET", keepersURL, apitest.Response{Status: 404, Raw: "{\"message\": \"no such owner\"}\n"})

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care",
			Params: map[string]any{"food": "hay", "breed": beagle, "share": int64(50)}})
		const want = "step keeper failed: failed to fetch owner by ID " + owner + `: 404 {"message": "no such owner"}; skipped --breed, --share after that`
		// The pet the caller named exists; what a step did not find is a
		// failure of the call.
		wantError(t, err, apikit.KindFailed, want)
		if err.Error() != want {
			t.Errorf("error %q, want %q", err, want)
		}
		if failure, ok := errors.AsType[*engine.HTTPError](err); !ok || failure.Status != 404 {
			t.Errorf("the error %v does not wrap the answer of the API", err)
		}
		wantMessages(t, result)
		wantRequests(t, shop.api, "GET "+petURL, "GET "+keepersURL)
	})

	t.Run("the payload is checked before the steps", func(t *testing.T) {
		shop := callShop(t, nil)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care-breed", Params: map[string]any{"breed": beagle}})
		wantError(t, err, apikit.KindUsage, "care-breed needs at least one of --pure, --share")
		wantRequests(t, shop.api)
	})
}

func TestStepsThatFail(t *testing.T) {
	robots := apitest.Response{Raw: `[{"staffId":"0e0e0e01","role":"guardian","name":"robot"}]`}
	tests := map[string]struct {
		pet, keepers, delegate apitest.Response
		kind                   apikit.Kind
		message                string
		sent                   int
	}{
		"a listing without an item that passes the filters": {
			pet: apitest.Response{Body: map[string]any{"ownerId": owner}}, keepers: robots,
			kind: apikit.KindFailed, message: "step keeper found nothing", sent: 2,
		},
		"an item without the field to keep": {
			pet:  apitest.Response{Body: map[string]any{"id": rex}},
			kind: apikit.KindFailed, message: "step owner found no ownerId", sent: 1,
		},
		"a field that is empty": {
			pet:  apitest.Response{Body: map[string]any{"ownerId": ""}},
			kind: apikit.KindFailed, message: "step owner found no ownerId", sent: 1,
		},
		// An API that has nothing to tell may answer with an empty object.
		"an item that is an empty object": {
			pet:  apitest.Response{Raw: `{}`},
			kind: apikit.KindFailed, message: "step owner found nothing", sent: 1,
		},
		"an answer that is no item": {
			pet:  apitest.Response{Raw: `["` + owner + `"]`},
			kind: apikit.KindFailed, message: "step owner found nothing", sent: 1,
		},
		"a request the API refuses": {
			pet:  apitest.Response{Status: 500, Raw: "boom"},
			kind: apikit.KindFailed, message: "step owner failed: failed to fetch pet by ID " + rex + ": 500 boom", sent: 1,
		},
		// A step that fails is a call that failed, whatever its request
		// came to: the item the caller named was found before.
		"an item that does not exist": {
			pet:  apitest.Response{Status: 404},
			kind: apikit.KindFailed, message: "step owner failed: failed to fetch pet by ID " + rex + ": 404", sent: 1,
		},
		"a listing that does not exist": {
			pet: apitest.Response{Body: map[string]any{"ownerId": owner}}, keepers: apitest.Response{Status: 404, Raw: "{\"message\": \"no such owner\"}\n"},
			kind: apikit.KindFailed, message: "step keeper failed: failed to fetch owner by ID " + owner + `: 404 {"message": "no such owner"}`, sent: 2,
		},
		"a call the API refuses": {
			pet: apitest.Response{Body: map[string]any{"ownerId": owner}}, keepers: apitest.Response{Raw: `[{"staffId":"` + ada + `"}]`},
			delegate: apitest.Response{Status: 403, Raw: "no"},
			kind:     apikit.KindFailed, message: "step pass failed: failed to apply staff by ID " + ada + ": 403 no", sent: 3,
		},
		"a call that answers without the field to keep": {
			pet: apitest.Response{Body: map[string]any{"ownerId": owner}}, keepers: apitest.Response{Raw: `[{"staffId":"` + ada + `"}]`},
			delegate: apitest.Response{Body: map[string]any{"expires": "soon"}},
			kind:     apikit.KindFailed, message: "step pass found no token", sent: 3,
		},
		"a call that answers nothing": {
			pet: apitest.Response{Body: map[string]any{"ownerId": owner}}, keepers: apitest.Response{Raw: `[{"staffId":"` + ada + `"}]`},
			delegate: noContent,
			kind:     apikit.KindFailed, message: "step pass found no token", sent: 3,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("GET", petURL, test.pet)
			shop.api.Handle("GET", keepersURL, test.keepers)
			shop.api.Handle("POST", delegateURL, test.delegate)

			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care", Params: map[string]any{"food": "hay"}})
			wantError(t, err, test.kind, test.message)
			if err != nil && err.Error() != test.message {
				t.Errorf("error %q, want %q", err, test.message)
			}
			// The change itself is never sent.
			if got := sent(shop.api); len(got) != test.sent || slices.ContainsFunc(got, func(line string) bool { return strings.HasPrefix(line, "PATCH") }) {
				t.Errorf("requests sent: %q, want %d and no change", got, test.sent)
			}
			wantMessages(t, result)
		})
	}
}

// stepOverlay gives the kennels of the pet shop calls whose steps show
// what a step can do.
const stepOverlay = `
resources:
  kennels:
    setters:
      - name: inspect
        pathTemplate: "kennels/${id}/inspect"
        steps:
          - {as: guard, get: owners, id: "${owner}", sub: keepers, filters: ["name!=${skip}", "retired!=true", "note != "], select: staffId}
        stepParameters:
          - {name: skip, default: robot}
        payloadTemplate: {guard: "${guard}"}
        parameters:
          - {name: owner}
      - name: watch
        pathTemplate: "kennels/${id}/watch"
        steps:
          - {as: guard, get: owners, filters: [" loud = ${loud} ", "tags=${tags}", "level=${level}", "name=${name}", "city=$nobody"], pick: first, select: profile}
        payloadTemplate: {guard: "${guard}"}
        parameters:
          - {name: loud, type: bool, required: false}
          - {name: tags, type: list}
          - {name: level, type: int}
          - {name: name}
        message: "Watched by ${guard}, loud ${loud}, level ${level}"
      - name: log
        pathTemplate: "kennels/${id}/log"
        steps:
          - as: note
            call: add
            resource: appointments
            id: "${visit}"
            parameters: {text: "${text}", internal: "${quiet}", keep-status: "true"}
            select: noteId
        payloadTemplate: {note: "${note}"}
        parameters:
          - {name: visit}
          - {name: text}
          - {name: quiet, type: bool, required: false}
      - name: host
        pathTemplate: "kennels/${id}/host"
        steps:
          - as: booked
            call: book
            resource: appointments
            parameters: {pet: "${pet}", role: keeper, title: "Stay of ${pet}", text: "${text}", room-id: "${id}"}
            select: appointmentId
        payloadTemplate: {appointment: "${booked}"}
        parameters:
          - {name: pet}
          - {name: text}
      - name: open
        requiresId: false
        pathTemplate: kennels
        payloadTemplate: {name: "${name}"}
        parameters:
          - {name: name}
        then:
          - {as: first, call: add, resource: appointments, id: "${result}", parameters: {text: one}, select: noteId}
          - {as: second, call: add, resource: appointments, id: "${result}", parameters: {text: "after ${first}"}, select: noteId}
        message: "Opened kennel ${name}"
`

func TestGetStep(t *testing.T) {
	const keepers = mainAPI + "/owners/" + owner + "/keepers?limit=2&skip="
	inspect := func(t *testing.T, shop *shop) (*engine.CallResult, error) {
		t.Helper()
		shop.api.Handle("POST", "/v1/kennels/"+kennel+"/inspect", noContent)
		return shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "inspect", Params: map[string]any{"owner": owner}})
	}
	twoAtATime := func(o *engine.Options) {
		o.Definition.Pagination.PageSize, o.Definition.HTTP.PaginationMaxWorkers = 2, 1
	}

	t.Run("a listing is read to its end", func(t *testing.T) {
		shop := callShop(t, twoAtATime, stepOverlay)
		// The only keeper the filters leave is on the second page. A
		// value is compared as it is shown: true, and nothing for null.
		shop.api.Handle("GET", keepers+"0", apitest.Response{Raw: `[{"staffId":"0e0e0e01","name":"robot"},{"staffId":"0e0e0e02","name":"Bob","retired":true}]`})
		shop.api.Handle("GET", keepers+"2", apitest.Response{Raw: `[{"staffId":"0e0e0e03","name":"Cy","retired":false,"note":null},{"staffId":"` + ada + `","name":"Ada","retired":"no","note":"x"}]`})
		shop.api.Handle("GET", keepers+"4", apitest.Response{Raw: `[]`})

		if _, err := inspect(t, shop); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		requests := requestLines(t, shop.api)
		if got := requests[len(requests)-1]; got != "POST "+mainAPI+"/kennels/"+kennel+`/inspect {"guard":"`+ada+`"}` {
			t.Errorf("sent %s, want the keeper of the second page", got)
		}
	})

	t.Run("several items where one is expected", func(t *testing.T) {
		shop := callShop(t, twoAtATime, stepOverlay)
		shop.api.Handle("GET", keepers+"0", apitest.Response{Raw: `[{"staffId":"` + ada + `","note":"x"},{"staffId":"0e0e0e02","note":"y"}]`})
		shop.api.Handle("GET", keepers+"2", apitest.Response{Raw: `[{"staffId":"0e0e0e03","note":"z"}]`})

		_, err := inspect(t, shop)
		wantError(t, err, apikit.KindFailed, "step guard found 3 results, expected one")
	})

	t.Run("an owner that does not exist", func(t *testing.T) {
		shop := callShop(t, nil, stepOverlay)
		shop.api.Handle("GET", adminAPI+"/owners?number=4711", apitest.Response{Raw: `[]`})
		shop.api.Handle("POST", "/v1/kennels/"+kennel+"/inspect", noContent)

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "inspect", Params: map[string]any{"owner": "4711"}})
		wantError(t, err, apikit.KindFailed, "step guard failed: unable to find owner 4711")
		wantRequests(t, shop.api, "GET "+adminAPI+"/owners?number=4711")
	})

	// A step passes values on, so it sees flags and lists as well, shown
	// as text. A filter is taken apart before its values are filled in.
	t.Run("the templates of a step see every value", func(t *testing.T) {
		shop := callShop(t, nil, stepOverlay)
		listing := adminAPI + "/owners?city=%24nobody&level=3&limit=100&loud=true&name=a%3Db%21%3Dc%2Cd&skip=0&tags=%5B%22dogs%22%2C+%22cats%22%5D"
		shop.api.Handle("GET", listing, apitest.Response{Raw: `[{"profile":{"name":"Ada","shifts":[1,2]}},{"profile":"second"}]`})
		shop.api.Handle("POST", "/v1/kennels/"+kennel+"/watch", noContent)

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "watch",
			Params: map[string]any{"loud": true, "tags": []string{"dogs", "cats"}, "level": int64(3), "name": "a=b!=c,d"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// What a step keeps may be any value, and the payload passes it
		// on. A message shows what the payload asked for; of the rest it
		// knows texts and numbers only, so the flag stays a placeholder.
		wantRequests(t, shop.api, "GET "+listing,
			"POST "+mainAPI+"/kennels/"+kennel+`/watch {"guard":{"name":"Ada","shifts":[1,2]}}`)
		wantMessages(t, result, `Watched by {"name": "Ada", "shifts": [1, 2]}, loud ${loud}, level 3`)
	})
}

func TestCallStep(t *testing.T) {
	notes := adminAPI + "/appointments/" + appointment + "/notes"
	logged := "POST " + mainAPI + "/kennels/" + kennel + `/log {"note":"0a0a0a01"}`
	tests := map[string]struct {
		params map[string]any
		want   string
	}{
		// A value takes the type of its parameter: the text "true" is a
		// flag. A placeholder alone passes a flag on as it is.
		"values take the type of their parameter": {
			map[string]any{"visit": appointment, "text": "Hello", "quiet": true},
			`{"text":"Hello","internal":true,"keepStatus":true}`,
		},
		"a placeholder alone leaves out a parameter that was not given": {
			map[string]any{"visit": appointment, "text": "Hello"},
			`{"text":"Hello","keepStatus":true}`,
		},
		// What a placeholder stood for is not read as a template again.
		"a value keeps its placeholders as text": {
			map[string]any{"visit": appointment, "text": "Leave ${result}, ${visit} and $$"},
			`{"text":"Leave ${result}, ${visit} and $$","keepStatus":true}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, stepOverlay)
			shop.api.Handle("POST", notes, apitest.Response{Body: map[string]any{"noteId": "0a0a0a01"}})
			shop.api.Handle("POST", "/v1/kennels/"+kennel+"/log", noContent)

			if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "log", Params: test.params}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, "POST "+notes+" "+test.want, logged)
		})
	}

	t.Run("an ID that names nothing", func(t *testing.T) {
		shop := callShop(t, nil, stepOverlay)
		shop.api.Handle("GET", mainAPI+"/appointments/next-week", apitest.Response{Status: 404})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "log", Params: map[string]any{"visit": "next-week", "text": "Hello"}})
		wantError(t, err, apikit.KindFailed, "step note failed: unable to find appointment next-week")
		wantRequests(t, shop.api, "GET "+mainAPI+"/appointments/next-week")
	})

	t.Run("a parameter that names nothing", func(t *testing.T) {
		const byTag = adminAPI + "/pets?search=%22REX-9%22"
		shop := callShop(t, nil, stepOverlay)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[]`})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "host", Params: map[string]any{"pet": "REX-9", "text": "Two nights"}})
		wantError(t, err, apikit.KindFailed, "step booked failed: unable to find pet REX-9")
		wantRequests(t, shop.api, "GET "+byTag)
	})

	// The call of a step is prepared like one from the command line: its
	// parameters take aliases and names, set their variables, and its own
	// steps run.
	t.Run("the call of a step is a call like any other", func(t *testing.T) {
		const byTag = adminAPI + "/pets?search=%22REX-7%22"
		shop := callShop(t, nil, stepOverlay)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
		shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Body: map[string]any{"appointmentId": appointment}})
		shop.api.Handle("POST", notes, apitest.Response{Body: map[string]any{"noteId": "0a0a0a01"}})
		shop.api.Handle("POST", "/v1/kennels/"+kennel+"/host", noContent)

		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "host", Params: map[string]any{"pet": "REX-7", "text": "Two nights"}}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		about := `{"kind":"pet","id":"` + rex + `"}`
		wantRequests(t, shop.api,
			"GET "+byTag,
			"POST "+mainAPI+`/appointments {"title":"Stay of REX-7","roomId":"`+kennel+`","about":`+about+`,"sharedWith":`+about+`,"notify":["pet_keeper"]}`,
			"POST "+notes+` {"text":"Two nights"}`,
			"POST "+mainAPI+"/kennels/"+kennel+`/host {"appointment":"`+appointment+`"}`)
	})
}

// stepShop returns an engine whose kennels have the call "try" with one
// step, written as a YAML mapping, that runs a call of the cages.
func stepShop(t *testing.T, step string) *shop {
	t.Helper()
	return callShop(t, nil, `
resources:
  kennels:
    setters:
      - name: try
        steps:
          - `+step+`
        payloadTemplate: {got: "${got}"}
        parameters:
          - {name: text, required: false}
          - {name: quiet, type: bool, required: false}
  cages:
    setters:
      - name: fit
        payloadTemplate: {a: "${a}", b: "${b}", n: "${n}", f: "${f}", tags: "${tags}", "on": "${on}"}
        parameters:
          - {name: a, required: false}
          - {name: b, required: false}
          - {name: n, type: int, required: false}
          - {name: f, type: float, required: false}
          - {name: tags, type: list, required: false}
          - {name: "on", type: bool, required: false}
        exclusiveParameters: [[a, b]]
`)
}

func TestCallStepConvertsItsParameters(t *testing.T) {
	tests := map[string]struct {
		parameters string
		params     map[string]any
		want       string
	}{
		"texts become numbers and flags": {
			parameters: `{n: "2", f: "2.50", "on": "TRUE"}`, want: `{"n":2,"f":2.5,"on":true}`,
		},
		"numbers and flags stay what they are": {
			parameters: `{n: 2, f: 3, "on": false}`, want: `{"n":2,"f":3,"on":false}`,
		},
		"a number becomes a text": {
			parameters: `{a: 5}`, want: `{"a":"5"}`,
		},
		"one value becomes a list": {
			parameters: `{tags: "${text}"}`, params: map[string]any{"text": "hello"}, want: `{"tags":["hello"]}`,
		},
		"the texts of a list are templates": {
			parameters: `{tags: ["${text}!", b, 3]}`, params: map[string]any{"text": "hello"}, want: `{"tags":["hello!","b",3]}`,
		},
		"a text is a template": {
			parameters: `{a: "${text} of ${id}, not $nobody"}`, params: map[string]any{"text": "cage"}, want: `{"a":"cage of ` + kennel + `, not $nobody"}`,
		},
		"a placeholder alone passes a flag on": {
			parameters: `{"on": "${quiet}", n: 1}`, params: map[string]any{"quiet": true}, want: `{"n":1,"on":true}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := stepShop(t, `{as: got, call: fit, resource: cages, id: "${id}", parameters: `+test.parameters+`, select: id}`)
			shop.api.Handle("POST", "/v1/cages/"+kennel, apitest.Response{Body: map[string]any{"id": "0a0a0a02"}})
			shop.api.Handle("POST", "/v1/kennels/"+kennel, noContent)

			if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "try", Params: test.params}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api,
				"POST "+mainAPI+"/cages/"+kennel+" "+test.want,
				"POST "+mainAPI+"/kennels/"+kennel+` {"got":"0a0a0a02"}`)
		})
	}
}

func TestCallStepThatCannotRun(t *testing.T) {
	fit := func(parameters string) string {
		return `{as: got, call: fit, resource: cages, id: "${id}", parameters: ` + parameters + `, select: id}`
	}
	tests := map[string]struct {
		step    string
		message string
	}{
		"a call of several setters": {
			`{as: got, call: set, resource: pets, id: "${id}", parameters: {diet: hay}, select: id}`,
			"step got needs exactly one set setter of pets",
		},
		"a call that needs an ID, without one": {
			`{as: got, call: add, resource: appointments, parameters: {text: hi}, select: noteId}`,
			"step got needs the ID of a appointment",
		},
		"a call that takes no ID, with one": {
			`{as: got, call: book, resource: appointments, id: "${id}", parameters: {pet: "` + rex + `", role: keeper, title: Visit, text: Soon}, select: appointmentId}`,
			"step got must not give an ID, book takes none",
		},
		"a parameter the call does not have": {
			fit(`{a: x, nope: y}`), "step got gives unknown parameter nope",
		},
		"a required parameter that is missing": {
			`{as: got, call: add, resource: appointments, id: "${id}", parameters: {internal: true}, select: noteId}`,
			"step got misses --text",
		},
		// Left out, since --text was not given.
		"a required parameter whose placeholder has no value": {
			`{as: got, call: add, resource: appointments, id: "${id}", parameters: {text: "${text}"}, select: noteId}`,
			"step got misses --text",
		},
		"a flag that is neither true nor false": {
			fit(`{"on": "yes"}`), "step got gives bool parameter on the value yes",
		},
		"a number that is none":                 {fit(`{n: two}`), "step got gives int parameter n the value two"},
		"a whole number with a fraction":        {fit(`{n: 2.5}`), "step got gives int parameter n the value 2.5"},
		"a flag where a number belongs":         {fit(`{n: true}`), "step got gives int parameter n the value true"},
		"a number JSON cannot hold":             {fit(`{f: "NaN"}`), "step got gives float parameter f the value NaN"},
		"a list where one value belongs":        {fit(`{a: [x, y]}`), "step got gives parameter a a list"},
		"a mapping where one value belongs":     {fit(`{a: {x: y}}`), "step got gives parameter a a mapping"},
		"parameters that exclude each other":    {fit(`{a: "1", b: "2"}`), "step got failed: fit takes only one of --a, --b"},
		"a payload of which nothing is left":    {fit(`{}`), "step got failed: fit needs at least one of --a, --b, --f, --n, --on, --tags"},
		"a value that is too much for a number": {fit(`{n: "99999999999999999999"}`), "step got gives int parameter n the value 99999999999999999999"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := stepShop(t, test.step)
			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "try"})
			// A mistake of the definition is none of the command line.
			wantError(t, err, apikit.KindFailed, test.message)
			if err != nil && err.Error() != test.message {
				t.Errorf("error %q, want %q", err, test.message)
			}
			wantMessages(t, result)
			wantRequests(t, shop.api)
		})
	}
}

func TestStepTokenStaysSecret(t *testing.T) {
	var log bytes.Buffer
	var lines sync.Mutex
	logging := func(o *engine.Options) {
		o.Log = slog.New(slog.NewTextHandler(lockedWriter{&lines, &log}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	t.Run("an answer that repeats the token", func(t *testing.T) {
		log.Reset()
		shop := callShop(t, logging)
		scriptKeeper(shop.api)
		shop.api.Handle("PATCH", petURL, apitest.Response{Status: 403, Raw: `{"message":"` + adasToken + ` may not feed pets"}`})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care", Params: map[string]any{"food": "hay"}})
		wantError(t, err, apikit.KindFailed, "failed to apply pet by ID "+rex+`: 403 {"message":"*** may not feed pets"}`)
		// Nothing of the error leads to the token, wrapped errors included.
		if _, ok := errors.AsType[*engine.HTTPError](err); ok || strings.Contains(err.Error(), adasToken) {
			t.Errorf("the error tells the token of the step: %v", err)
		}
		if logged := log.String(); strings.Contains(logged, adasToken) || strings.Count(logged, "\n") != 4 {
			t.Errorf("the log holds the token, or not one line for each of 4 requests:\n%s", logged)
		}
	})

	t.Run("a token the API rejects", func(t *testing.T) {
		source := &tokens{current: "own", next: "other"}
		shop := callShop(t, func(o *engine.Options) { o.Tokens = source })
		scriptKeeper(shop.api)
		shop.api.Handle("PATCH", petURL, apitest.Response{Status: 401, Raw: adasToken + " has expired"})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care", Params: map[string]any{"food": "hay"}})
		wantError(t, err, apikit.KindUnauthenticated, "the token of the steps was rejected by the server")
		// The engine's own credential is not to blame.
		if strings.Contains(err.Error(), adasToken) || len(source.rejected) != 0 {
			t.Errorf("error %q, rejected %q: want neither the token nor a new login", err, source.rejected)
		}
		if got := len(sent(shop.api)); got != 4 {
			t.Errorf("%d requests sent, want 4: the change is not sent again", got)
		}
	})

	t.Run("a later step that fails on the token", func(t *testing.T) {
		shop := callShop(t, nil, `
resources:
  kennels:
    setters:
      - name: enter
        steps:
          - {as: pass, call: delegate, resource: staff, id: "${keeper}", select: token}
          - {as: door, call: fit, resource: cages, id: "${id}", parameters: {n: "${pass}"}, select: id}
        token: "${pass}"
        payloadTemplate: {door: "${door}"}
        parameters:
          - {name: keeper}
  cages:
    setters:
      - name: fit
        payloadTemplate: {n: "${n}"}
        parameters:
          - {name: n, type: int}
`)
		shop.api.Handle("POST", delegateURL, apitest.Response{Body: map[string]any{"token": adasToken}})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", ID: kennel, Call: "enter", Params: map[string]any{"keeper": ada}})
		wantError(t, err, apikit.KindFailed, "step door gives int parameter n the value ***")
		if strings.Contains(err.Error(), adasToken) {
			t.Errorf("the error tells the token of the step: %v", err)
		}
	})
}

func TestThenSteps(t *testing.T) {
	notes := adminAPI + "/appointments/" + appointment + "/notes"
	about := `{"kind":"pet","id":"` + rex + `"}`
	booking := "POST " + mainAPI + `/appointments {"title":"Checkup","about":` + about + `,"sharedWith":` + about + `,"notify":["pet_keeper"]}`
	book := func(shop *shop, params map[string]any) (*engine.CallResult, error) {
		given := map[string]any{"pet": rex, "role": "keeper", "title": "Checkup", "text": "See you on Monday."}
		maps.Copy(given, params)
		return shop.Apply(t.Context(), engine.CallInput{Resource: "appointments", Call: "book", Params: given})
	}

	t.Run("the steps after the request see the ID of its answer", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Status: 201, Body: map[string]any{"appointmentId": appointment}})
		shop.api.Handle("POST", notes, apitest.Response{Status: 201, Body: map[string]any{"noteId": "0a0a0a01"}})

		result, err := book(shop, nil)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// The first note is not internal, as the flag was not given.
		wantRequests(t, shop.api, booking, "POST "+notes+` {"text":"See you on Monday."}`)
		// The answer of the call is that of its own request.
		wantJSON(t, result.Body, `{"appointmentId":"`+appointment+`"}`)
		if result.Resource != "bookings" {
			t.Errorf("Resource = %q, want bookings", result.Resource)
		}
	})

	t.Run("a flag reaches the step", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Body: map[string]any{"appointmentId": appointment}})
		shop.api.Handle("POST", notes, apitest.Response{Body: map[string]any{"noteId": "0a0a0a01"}})

		if _, err := book(shop, map[string]any{"internal": true, "room": "0a0a0a03"}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if got := requestLines(t, shop.api)[1]; got != "POST "+notes+` {"text":"See you on Monday.","internal":true}` {
			t.Errorf("the first note was sent as %s, want it internal", got)
		}
	})

	t.Run("a step that fails says what was done", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("POST", mainAPI+"/appointments", apitest.Response{Body: map[string]any{"appointmentId": appointment}})
		shop.api.Handle("POST", notes, apitest.Response{Status: 400, Raw: "too long"})

		result, err := book(shop, nil)
		wantError(t, err, apikit.KindFailed,
			"book succeeded with "+appointment+", but not the steps after it: step first-note failed: failed to apply appointment by ID "+appointment+": 400 too long")
		wantMessages(t, result)
		if result.Body != nil {
			t.Errorf("Body = %v, want none: the call did not succeed", result.Body)
		}
	})

	for name, answer := range map[string]apitest.Response{
		"an answer without an ID":     {Body: map[string]any{"title": "Checkup"}},
		"an answer whose ID is empty": {Body: map[string]any{"appointmentId": ""}},
		"no answer":                   noContent,
	} {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("POST", mainAPI+"/appointments", answer)

			_, err := book(shop, nil)
			wantError(t, err, apikit.KindFailed, "book succeeded, but its response has no ID for the steps after it")
			wantRequests(t, shop.api, booking)
		})
	}

	// A request without an ID goes to the API for listings.
	opened := "POST " + adminAPI + `/kennels {"name":"north"}`
	open := func(shop *shop) (*engine.CallResult, error) {
		shop.api.Handle("POST", adminAPI+"/kennels", apitest.Response{Body: map[string]any{"id": appointment}})
		return shop.Apply(t.Context(), engine.CallInput{Resource: "kennels", Call: "open", Params: map[string]any{"name": "north"}})
	}

	t.Run("a step sees what the steps before it kept", func(t *testing.T) {
		shop := callShop(t, nil, stepOverlay)
		shop.api.Handle("POST", notes, apitest.Response{Sequence: []apitest.Response{
			{Body: map[string]any{"noteId": "0a0a0a01"}}, {Body: map[string]any{"noteId": "0a0a0a02"}},
		}})
		if _, err := open(shop); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api, opened, "POST "+notes+` {"text":"one"}`, "POST "+notes+` {"text":"after 0a0a0a01"}`)
	})

	t.Run("a step that needs what an earlier one did not keep does not run", func(t *testing.T) {
		shop := callShop(t, nil, stepOverlay)
		shop.api.Handle("POST", notes, apitest.Response{Raw: `{"noteId":""}`})

		_, err := open(shop)
		wantError(t, err, apikit.KindFailed,
			"open succeeded with "+appointment+", but not the steps after it: step second needs first, which that step's response lacked")
		wantRequests(t, shop.api, opened, "POST "+notes+` {"text":"one"}`)
	})
}
