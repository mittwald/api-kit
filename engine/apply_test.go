package engine_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

// IDs of the pet shop that the tests of calls use.
const (
	ada         = "0e0e0e0e" // a member of staff
	appointment = "0d0d0d0d"
)

// callOverlay adds to the pet shop the calls that the tests run.
//
//   - pets are renamed, weighed (a payload written as JSON text) and
//     archived (a setter with nothing but a name).
//   - "set" of pets is a call of three setters, each with an alias.
//   - "care" of pets is a call of three setters, two of which act for a
//     keeper of the pet's owner: their steps find one and get a token.
//   - appointments are booked for a pet or an owner, which takes no ID,
//     and get notes added; booking adds the first note itself.
//   - owners are suspended, which is the call with choices.
const callOverlay = `
filters: ["sort="]
orderFilters: [sort]
resources:
  pets:
    setters:
      - name: rename
        method: PATCH
        pathTemplate: "pets/${id}"
        payloadTemplate: {name: "${name}"}
        parameters:
          - {name: name, template: true, maxLength: 20}
        message: "Renamed pet ${id} to ${name}"
      - name: weigh
        method: PUT
        api: admin
        resource: weighings
        pathTemplate: "scales/${id}/weigh"
        payloadTemplate: '{"note":"${note}","grams":${grams},"fed":${fed},"by":"vet ${vet-name}"}'
        parameters:
          - {name: note}
          - {name: grams, type: int}
          - {name: fed, type: bool, required: false}
          - {name: vet-name, required: false}
      - name: archive
      - name: set
        aliases: [diet]
        method: PATCH
        api: admin
        pathTemplate: "pets/${id}"
        payloadTemplate: {care: [{name: diet, value: "${diet}"}]}
        parameters:
          - {name: diet}
        message: "Set diet of pet ${id} to ${diet}"
      - name: set
        aliases: [cage]
        method: PATCH
        api: admin
        pathTemplate: "pets/${id}"
        payloadTemplate: {care: [{name: cage, value: "${cage}"}]}
        parameters:
          - {name: cage}
        message: "Set cage of pet ${id} to ${cage}"
      - name: set
        aliases: [trait]
        method: PATCH
        api: admin
        pathTemplate: "pets/${id}"
        payloadTemplate: {care: [{name: "${trait-key}", value: "${trait-value}"}]}
        parameters:
          - {name: trait-key, aliases: [key]}
          - {name: trait-value, aliases: [value]}
        message: "Set trait ${trait-key} of pet ${id} to ${trait-value}"
      - name: care
        aliases: [care-food]
        method: PATCH
        pathTemplate: "pets/${id}"
        steps:
          - {as: owner, get: pets, id: "${id}", select: ownerId}
          - {as: keeper, get: owners, id: "${owner}", sub: keepers, filters: ["role=${as-role}", "name!=robot"], pick: first, select: staffId}
          - {as: pass, call: delegate, resource: staff, id: "${keeper}", select: token}
        stepParameters:
          - {name: as-role, default: guardian, help: Role of the keeper to act for}
        token: "${pass}"
        payloadTemplate: {food: "${food}", portions: "${portions}"}
        parameters:
          - {name: food, aliases: [food-id], required: false}
          - {name: portions, type: int, required: false}
        exclusiveParameters: [[food, found-food]]
        message: "Changed ${changes} for pet ${id} as ${as-role} ${keeper}"
      - name: care
        aliases: [care-food]
        method: PATCH
        api: admin
        pathTemplate: "pets/${id}"
        payloadTemplate: {foundFood: "${found-food}"}
        parameters:
          - {name: found-food, required: false}
        message: "Changed ${changes} for pet ${id}"
      - name: care
        aliases: [care-breed]
        method: PATCH
        pathTemplate: "pets/${id}"
        steps:
          - {as: owner, get: pets, id: "${id}", select: ownerId}
          - {as: keeper, get: owners, id: "${owner}", sub: keepers, filters: ["role=${as-role}", "name!=robot"], pick: first, select: staffId}
          - {as: pass, call: delegate, resource: staff, id: "${keeper}", select: token}
        stepParameters:
          - {name: as-role, default: guardian, help: Role of the keeper to act for}
        token: "${pass}"
        payloadTemplate:
          breeds:
            "${breed}": {share: "${share}", pure: "${pure}"}
        parameters:
          - {name: breed, aliases: [breed-id], resource: breeds}
          - {name: share, type: int, required: false}
          - {name: pure, type: bool, required: false}
        message: "Changed breed ${breed} of pet ${id} as ${as-role} ${keeper}"
  owners:
    setters:
      - name: suspend
        pathTemplate: "owners/${id}/actions/suspend"
        payloadTemplate: {reason: "${reason}"}
        parameters:
          - {name: reason, choices: [fraud, debt]}
        message: "Suspended owner ${id}"
  keepers:
    parent: owners
  carers:
    parent: owners
  staff:
    setters:
      - name: delegate
        pathTemplate: "staff/${id}/delegate"
        payloadTemplate: {staffId: "${id}"}
  weighings: {}
  notes: {}
  bookings:
    idField: appointmentId
  appointments:
    filters: ["about=", "status=", "search="]
    setters:
      - name: add
        api: admin
        resource: notes
        pathTemplate: "appointments/${id}/notes"
        payloadTemplate: {text: "${text}", internal: "${internal}", keepStatus: "${keep-status}"}
        parameters:
          - {name: text, template: true, maxLength: 40}
          - {name: internal, type: bool, required: false}
          - {name: keep-status, type: bool, required: false}
        message: "Added a note to appointment ${id}"
      - name: book
        requiresId: false
        api: main
        resource: bookings
        pathTemplate: appointments
        payloadTemplate:
          title: "${title}"
          roomId: "${room}"
          about: {kind: "${subject}", id: "${subject-id}"}
          sharedWith: {kind: "${subject}", id: "${subject-id}"}
          vetId: "${vet}"
          notify: ["${subject}_${role}"]
        parameters:
          - name: pet
            required: false
            resource: pets
            variables: {subject: pet, subject-resource: pets, subject-id: "${pet}"}
          - name: owner
            required: false
            resource: owners
            variables: {subject: owner, subject-resource: owners, subject-id: "${owner}"}
          - name: vet
            required: false
            lookup:
              get: "${subject-resource}"
              id: "${subject-id}"
              sub: carers
              filters: ["active=true", "role!=guest", "role!=none"]
              match: [staffId, email]
              select: staffId
              complete: email
          - name: role
            required: false
            choices: {pet: [keeper, sitter], owner: [keeper, payer]}
          - {name: title, template: true}
          - {name: text, template: true, maxLength: 40}
          - {name: room, aliases: [room-id], required: false}
          - {name: internal, type: bool, required: false}
        oneOfParameters: [[pet, owner], [vet, role]]
        then:
          - as: first-note
            call: add
            resource: appointments
            id: "${result}"
            parameters: {text: "${text}", internal: "${internal}"}
            select: noteId
`

// callShop returns an engine for the pet shop with the calls of
// [callOverlay] and further overlays, and the fake API it talks to. Its
// requests carry the token "own".
func callShop(t *testing.T, change func(o *engine.Options), overlays ...string) *shop {
	t.Helper()
	doc := load(t, append([]string{callOverlay}, overlays...)...)
	api := apitest.New(t)
	waits := &pauses{}
	options := engine.Options{Definition: doc, HTTP: api.Client(), Tokens: &tokens{current: "own"}, Sleep: waits.sleep}
	if change != nil {
		change(&options)
	}
	made, err := engine.New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &shop{Engine: made, doc: doc, api: api, pauses: waits}
}

// requestLines returns the requests the fake API received, one line each:
// the method, the URL and, of a request that has one, the body.
func requestLines(t *testing.T, api *apitest.Server) []string {
	t.Helper()
	lines := sent(api)
	for i, request := range api.Requests() {
		if request.Body != nil {
			lines[i] += " " + encode(t, request.Body)
		}
	}
	return lines
}

// wantRequests checks which requests the fake API received, in order, with
// their bodies.
func wantRequests(t *testing.T, api *apitest.Server, want ...string) {
	t.Helper()
	if got := requestLines(t, api); !slices.Equal(got, want) {
		t.Errorf("requests sent:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// wantMessages checks the lines a call reports for its requests.
func wantMessages(t *testing.T, result *engine.CallResult, want ...string) {
	t.Helper()
	if result == nil {
		t.Fatalf("no result, want the messages %q", want)
	}
	if !slices.Equal(result.Messages, want) {
		t.Errorf("messages:\n  %s\nwant:\n  %s", strings.Join(result.Messages, "\n  "), strings.Join(want, "\n  "))
	}
}

// noContent answers a request that changes something and tells nothing.
var noContent = apitest.Response{Status: 204}

func TestApplySendsTheRequestOfASetter(t *testing.T) {
	shop := callShop(t, nil)
	shop.api.Handle("PUT", adminAPI+"/scales/"+rex+"/weigh", apitest.Response{Body: map[string]any{"weighingId": "0f0f0f0f"}})

	result, err := shop.Apply(t.Context(), engine.CallInput{
		Resource: "pets", ID: rex, Call: "weigh",
		Params: map[string]any{"note": "after lunch", "grams": int64(4200), "fed": true, "vet-name": "Ada"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The method, the API and the path of the setter, with the payload.
	wantRequests(t, shop.api,
		"PUT "+adminAPI+"/scales/"+rex+`/weigh {"note":"after lunch","grams":4200,"fed":true,"by":"vet Ada"}`)
	if got := authorizations(shop.api); !slices.Equal(got, []string{"Bearer own"}) {
		t.Errorf("credentials sent: %q, want that of the engine", got)
	}
	// The answer is described by the resource the setter names for it.
	if result.Resource != "weighings" {
		t.Errorf("Resource = %q, want weighings", result.Resource)
	}
	wantJSON(t, result.Body, `{"weighingId":"0f0f0f0f"}`)
	if len(result.Messages) != 0 || result.DryRun != nil {
		t.Errorf("Messages = %q, DryRun = %v, want neither next to a body", result.Messages, result.DryRun)
	}
}

func TestApplyReportsWhatItDid(t *testing.T) {
	tests := map[string]struct {
		call     string
		params   map[string]any
		answer   apitest.Response
		request  string
		message  string
		resource string
		body     string
	}{
		"the message of the setter, when the API tells nothing": {
			call: "rename", params: map[string]any{"name": "Rex II"}, answer: noContent,
			request: "PATCH " + mainAPI + "/pets/" + rex + ` {"name":"Rex II"}`,
			message: "Renamed pet " + rex + " to Rex II", resource: "pets",
		},
		// A setter with nothing but a name posts to the item.
		"a line that names the item, without a message": {
			call: "archive", answer: noContent,
			request: "POST " + mainAPI + "/pets/" + rex,
			message: "Successfully applied pet " + rex, resource: "pets",
		},
		"the answer, when there is one": {
			call: "archive", answer: apitest.Response{Body: map[string]any{"archived": true}},
			request:  "POST " + mainAPI + "/pets/" + rex,
			resource: "pets", body: `{"archived":true}`,
		},
		// What counts as nothing is not shown in place of the message.
		"an empty object is no answer":   {call: "archive", answer: apitest.Response{Raw: `{}`}, request: "POST " + mainAPI + "/pets/" + rex, message: "Successfully applied pet " + rex, resource: "pets"},
		"nor is an empty list":           {call: "archive", answer: apitest.Response{Raw: `[]`}, request: "POST " + mainAPI + "/pets/" + rex, message: "Successfully applied pet " + rex, resource: "pets"},
		"nor null":                       {call: "archive", answer: apitest.Response{Raw: `null`}, request: "POST " + mainAPI + "/pets/" + rex, message: "Successfully applied pet " + rex, resource: "pets"},
		"nor false":                      {call: "archive", answer: apitest.Response{Raw: `false`}, request: "POST " + mainAPI + "/pets/" + rex, message: "Successfully applied pet " + rex, resource: "pets"},
		"a list of something is":         {call: "archive", answer: apitest.Response{Raw: `[1]`}, request: "POST " + mainAPI + "/pets/" + rex, resource: "pets", body: `[1]`},
		"the resource of the answer too": {call: "weigh", params: map[string]any{"note": "n", "grams": int64(1)}, answer: noContent, request: "PUT " + adminAPI + "/scales/" + rex + `/weigh {"note":"n","grams":1}`, message: `Successfully applied '{"note": "n", "grams": 1}' to pet ` + rex, resource: "weighings"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			method, target, _ := strings.Cut(strings.SplitN(test.request, " {", 2)[0], " ")
			shop.api.Handle(method, target, test.answer)

			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: test.call, Params: test.params})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, test.request)
			if result.Resource != test.resource {
				t.Errorf("Resource = %q, want %q", result.Resource, test.resource)
			}
			if test.body != "" {
				wantJSON(t, result.Body, test.body)
				wantMessages(t, result)
				return
			}
			if result.Body != nil {
				t.Errorf("Body = %s, want none", encode(t, result.Body))
			}
			wantMessages(t, result, test.message)
		})
	}
}

func TestApplyKeepsItsOrder(t *testing.T) {
	const (
		byTag    = adminAPI + "/pets?search=%22REX-7%22"
		breeds   = mainAPI + "/breeds?limit=100&skip=0"
		open     = adminAPI + "/appointments?about=" + rex + "&limit=100&skip=0&status=open"
		pet      = mainAPI + "/pets/" + rex
		keepers  = mainAPI + "/owners/" + owner + "/keepers?limit=100&role=guardian&skip=0"
		delegate = mainAPI + "/staff/" + ada + "/delegate"
	)
	script := func(shop *shop) {
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
		shop.api.Handle("GET", breeds, apitest.Response{Raw: `[{"id":"` + beagle + `","name":"Beagle"}]`})
		shop.api.Handle("GET", pet, apitest.Response{Body: map[string]any{"id": rex, "species": "dog", "ownerId": owner}})
		shop.api.Handle("GET", open, apitest.Response{Raw: `[]`})
		shop.api.Handle("GET", keepers, apitest.Response{Raw: `[{"staffId":"` + ada + `"}]`})
		shop.api.Handle("POST", delegate, apitest.Response{Body: map[string]any{"token": "t0ken-of-ada"}})
		shop.api.Handle("PATCH", pet, noContent)
	}
	in := engine.CallInput{
		Resource: "pets", ID: "REX-7", Call: "care-breed",
		Params: map[string]any{"breed": "Beagle", "share": int64(50)},
		If:     []string{"species=dog"},
		IfNone: [][]string{{"appointments", "about=@id", "status=open"}},
	}

	// The ID, the parameters that name an item, the conditions, the
	// listings, the steps, the change. The pet a step asks for is known
	// from the condition.
	shop := callShop(t, nil)
	script(shop)
	result, err := shop.Apply(t.Context(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantRequests(t, shop.api,
		"GET "+byTag, "GET "+breeds, "GET "+pet, "GET "+open, "GET "+keepers,
		"POST "+delegate+` {"staffId":"`+ada+`"}`,
		"PATCH "+pet+` {"breeds":{"`+beagle+`":{"share":50}}}`)
	wantMessages(t, result, "Changed breed "+beagle+" of pet "+rex+" as guardian "+ada)

	// A dry run ends before the first step.
	shop = callShop(t, nil)
	script(shop)
	in.DryRun = true
	if _, err := shop.Apply(t.Context(), in); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantRequests(t, shop.api, "GET "+byTag, "GET "+breeds, "GET "+pet, "GET "+open)

	// What is wrong with the call itself shows before the first request.
	shop = callShop(t, nil)
	in.Params = map[string]any{"breed": "Beagle", "share": int64(50), "food": "hay"}
	_, err = shop.Apply(t.Context(), in)
	wantError(t, err, apikit.KindUsage, "care-breed has no parameter food")
	in.Params, in.If = map[string]any{"share": int64(50)}, []string{"species"}
	_, err = shop.Apply(t.Context(), in)
	wantError(t, err, apikit.KindUsage, "care-breed needs --breed")
	wantRequests(t, shop.api)
}

func TestApplyOfASetterThatReads(t *testing.T) {
	shop := callShop(t, nil, `
resources:
  toys:
    setters:
      - name: ping
        method: GET
        pathTemplate: "toys/${id}/ping"
`)
	shop.api.Handle("GET", "/v1/toys/"+toy+"/ping", apitest.Response{Status: 404, Raw: "no such toy"})
	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "ping"})
	// A call is a call, whatever its method: the item was not asked for.
	wantError(t, err, apikit.KindFailed, "failed to apply toy by ID "+toy+": 404 no such toy")
	if failure, ok := errors.AsType[*engine.HTTPError](err); !ok || failure.Status != 404 {
		t.Errorf("the error %v does not wrap the answer of the API", err)
	}
}

func TestApplyRefusesWhatCannotRun(t *testing.T) {
	tests := map[string]struct {
		in      engine.CallInput
		message string
	}{
		// The ID is missed, not the item a condition would test.
		"a call that changes an item, with a condition and without an ID": {
			engine.CallInput{Resource: "pets", Call: "rename", Params: map[string]any{"name": "Rex II"}, If: []string{"species=dog"},
				IfAny: [][]string{{"appointments", "about=@id"}}},
			"rename needs the ID of a pet",
		},
		"a call the resource does not have": {
			engine.CallInput{Resource: "pets", ID: rex, Call: "missing"},
			"missing is not available for pets",
		},
		"a call of another resource": {
			engine.CallInput{Resource: "breeds", ID: beagle, Call: "rename", Params: map[string]any{"name": "x"}},
			"rename is not available for breeds",
		},
		"a call that changes an item, without an ID": {
			engine.CallInput{Resource: "pets", Call: "rename", Params: map[string]any{"name": "Rex II"}},
			"rename needs the ID of a pet",
		},
		"a call that takes no ID, with one": {
			engine.CallInput{Resource: "appointments", ID: appointment, Call: "book", Params: map[string]any{"pet": rex, "role": "keeper", "title": "Visit", "text": "Soon."}},
			"book takes no ID, got " + appointment,
		},
		"a parameter the call does not have": {
			engine.CallInput{Resource: "pets", ID: rex, Call: "rename", Params: map[string]any{"name": "x", "color": "red"}},
			"rename has no parameter color; it has name",
		},
		"a required parameter that is missing": {
			engine.CallInput{Resource: "pets", ID: rex, Call: "weigh", Params: map[string]any{"fed": true}},
			"weigh needs --note, --grams",
		},
		// A parameter that takes a template can be given in three ways.
		"a required parameter that takes a template": {
			engine.CallInput{Resource: "pets", ID: rex, Call: "rename"},
			"rename needs --name/--name-template/--name-template-file",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			result, err := shop.Apply(t.Context(), test.in)
			wantError(t, err, apikit.KindUsage, test.message)
			if err != nil && err.Error() != test.message {
				t.Errorf("error %q, want %q", err, test.message)
			}
			if result != nil {
				t.Errorf("result = %+v, want none: nothing was done", result)
			}
			wantRequests(t, shop.api)
		})
	}
}

func TestApplyResolvesTheID(t *testing.T) {
	const byTag = adminAPI + "/pets?search=%22REX-7%22"

	t.Run("a tag names the pet", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"0d0d0d0d","tag":"REX-1","notes":"brother of REX-7"},{"id":"` + rex + `","tag":"REX-7"}]`})
		shop.api.Handle("PATCH", "/v1/pets/"+rex, noContent)

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: "REX-7", Call: "rename", Params: map[string]any{"name": "Rex II"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// The change reaches the pet with that tag, not one that mentions
		// it, and the templates see its ID.
		wantRequests(t, shop.api, "GET "+byTag, "PATCH "+mainAPI+"/pets/"+rex+` {"name":"Rex II"}`)
		wantMessages(t, result, "Renamed pet "+rex+" to Rex II")
	})

	t.Run("a tag that names no pet sends nothing", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"0d0d0d0d","tag":"REX-1","notes":"brother of REX-7"}]`})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: "REX-7", Call: "rename", Params: map[string]any{"name": "Rex II"}})
		wantError(t, err, apikit.KindNotFound, "unable to find pet REX-7")
		wantRequests(t, shop.api, "GET "+byTag)
	})
}

func TestApplyFailsWhenTheAPIRefuses(t *testing.T) {
	for name, status := range map[string]int{
		"a failure":     500,
		"a refusal":     403,
		"a 404 as well": 404,
	} {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			shop.api.Handle("PATCH", "/v1/pets/"+rex, apitest.Response{Status: status, Raw: `{"message":"no"}`})

			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "rename", Params: map[string]any{"name": "Rex II"}})
			// What a call changes was found before, so a 404 is a failure
			// of the call and not a missing item.
			wantError(t, err, apikit.KindFailed, "failed to apply pet by ID "+rex+": ", `{"message":"no"}`)
			failure, ok := errors.AsType[*engine.HTTPError](err)
			if !ok || failure.Status != status || failure.Method != "PATCH" {
				t.Errorf("the error wraps %+v, want the answer with status %d", failure, status)
			}
			if result == nil || len(result.Messages) != 0 || result.Body != nil {
				t.Errorf("result = %+v, want one that tells nothing was done", result)
			}
			// A request that changes something is sent once.
			wantRequests(t, shop.api, "PATCH "+mainAPI+"/pets/"+rex+` {"name":"Rex II"}`)
		})
	}
}

func TestApplySharedCall(t *testing.T) {
	petURL := adminAPI + "/pets/" + rex
	diet := `PATCH ` + petURL + ` {"care":[{"name":"diet","value":"dry food"}]}`
	cage := `PATCH ` + petURL + ` {"care":[{"name":"cage","value":"B \"big\" 2"}]}`

	t.Run("one request for each setter of which a parameter was given", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("PATCH", petURL, noContent)

		// A quote in a value is a quote in the payload.
		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "set",
			Params: map[string]any{"cage": `B "big" 2`, "diet": "dry food"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// In the order of the definition, not of the parameters.
		wantRequests(t, shop.api, diet, cage)
		wantMessages(t, result, "Set diet of pet "+rex+" to dry food", "Set cage of pet "+rex+` to B "big" 2`)
		if result.Body != nil || result.Resource != "pets" {
			t.Errorf("Body = %v, Resource = %q, want no body of pets", result.Body, result.Resource)
		}
	})

	t.Run("one setter of several answers like a call of one setter", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("PATCH", petURL, apitest.Response{Body: map[string]any{"id": rex}})

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "set", Params: map[string]any{"diet": "dry food"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// One setter ran, so its answer is the result.
		wantJSON(t, result.Body, `{"id":"`+rex+`"}`)
		wantMessages(t, result)
	})

	t.Run("an alias runs its setter alone", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("PATCH", petURL, noContent)

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "trait",
			Params: map[string]any{"key": "color", "trait-value": "black"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api, `PATCH `+petURL+` {"care":[{"name":"color","value":"black"}]}`)
		wantMessages(t, result, "Set trait color of pet "+rex+" to black")

		_, err = shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "trait", Params: map[string]any{"diet": "dry food"}})
		wantError(t, err, apikit.KindUsage, "trait has no parameter diet; it has trait-key, trait-value")
	})

	t.Run("the first request that fails ends the call", func(t *testing.T) {
		shop := callShop(t, nil)
		// The body of the answer ends in a line break, the message does
		// not break there.
		shop.api.Handle("PATCH", petURL, apitest.Response{Sequence: []apitest.Response{noContent, {Status: 500, Raw: "{\"message\": \"boom\"}\n"}}})

		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "set",
			Params: map[string]any{"diet": "dry food", "cage": `B "big" 2`, "trait-key": "color", "trait-value": "black"}})
		const want = "failed to apply pet by ID " + rex + `: 500 {"message": "boom"}; skipped --trait-key, --trait-value after that`
		wantError(t, err, apikit.KindFailed, want)
		if err.Error() != want {
			t.Errorf("error %q, want %q", err, want)
		}
		if _, ok := errors.AsType[*engine.HTTPError](err); !ok {
			t.Errorf("the error %v does not wrap the answer of the API", err)
		}
		// What was done before comes back with the error.
		wantRequests(t, shop.api, diet, cage)
		wantMessages(t, result, "Set diet of pet "+rex+" to dry food")
	})

	t.Run("the last request that fails skips nothing", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("PATCH", petURL, apitest.Response{Sequence: []apitest.Response{noContent, {Status: 500, Raw: "boom"}}})

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "set",
			Params: map[string]any{"diet": "dry food", "cage": `B "big" 2`}})
		wantError(t, err, apikit.KindFailed, "500 boom")
		if strings.Contains(err.Error(), "skipped") {
			t.Errorf("error %q, want nothing skipped", err)
		}
	})

	mistakes := map[string]struct {
		call    string
		params  map[string]any
		message string
	}{
		"no parameter at all": {
			"set", nil,
			"set needs at least one of --cage, --diet, --trait-key, --trait-value",
		},
		// --diet alone would do, but nothing is sent while --trait-key
		// lacks its value.
		"a setter that lacks a required parameter": {
			"set", map[string]any{"trait-key": "color", "diet": "dry food"},
			"--trait-key also needs --trait-value",
		},
		// A parameter whose name starts like another one is not that one.
		"parameters that share a prefix are told apart": {
			"care", map[string]any{"share": int64(50)},
			"--share also needs --breed",
		},
		"parameters of different setters that exclude each other": {
			"care", map[string]any{"food": "hay", "found-food": "grass"},
			"care takes only one of --food, --found-food",
		},
		// The found food is sent with the engine's own token, so the role
		// to act in would do nothing.
		"a step parameter without a setter that uses it": {
			"care-food", map[string]any{"found-food": "grass", "as-role": "sitter"},
			"--as-role needs one of --food, --portions",
		},
		// The breed part has neither share nor purity, so the food part
		// is not sent either.
		"a part without a payload stops the whole call": {
			"care", map[string]any{"portions": int64(2), "breed": beagle},
			"care needs at least one of --pure, --share",
		},
		"an alias that selects no setter": {
			"care-food", nil,
			"care-food needs at least one of --food, --found-food, --portions",
		},
	}
	for name, test := range mistakes {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: test.call, Params: test.params})
			wantError(t, err, apikit.KindUsage, test.message)
			if result != nil {
				t.Errorf("result = %+v, want none", result)
			}
			wantRequests(t, shop.api)
		})
	}
}

func TestApplyDryRun(t *testing.T) {
	const (
		byTag  = adminAPI + "/pets?search=%22REX-7%22"
		carers = mainAPI + "/pets/" + rex + "/carers?active=true&limit=100&skip=0"
	)
	shop := callShop(t, nil)
	shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
	shop.api.Handle("GET", carers, apitest.Response{Raw: `[{"staffId":"` + ada + `","email":"ada@example.test","role":"keeper"}]`})

	result, err := shop.Apply(t.Context(), engine.CallInput{
		Resource: "appointments", Call: "book", DryRun: true,
		Params: map[string]any{"pet": "REX-7", "vet": "ADA@example.test", "title": "Checkup", "text": "Line 1\nLine 2", "internal": true},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Names are resolved, which only reads; nothing is changed.
	wantRequests(t, shop.api, "GET "+byTag, "GET "+carers)
	want := []engine.ResolvedParam{
		{Name: "pet", Value: rex, Given: "REX-7"},
		{Name: "vet", Value: ada, Given: "ADA@example.test"},
		{Name: "title", Value: "Checkup", Given: "Checkup"},
		{Name: "text", Value: "Line 1\nLine 2", Given: "Line 1\nLine 2"},
		{Name: "internal", Value: true, Given: true},
	}
	if !slices.Equal(result.DryRun, want) {
		t.Errorf("DryRun = %+v\nwant %+v", result.DryRun, want)
	}
	if result.Body != nil || len(result.Messages) != 0 || result.Resource != "appointments" {
		t.Errorf("result = %+v, want nothing but the parameters", result)
	}

	t.Run("the ID comes first, as it was resolved", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
		result, err := shop.Apply(t.Context(), engine.CallInput{
			Resource: "pets", ID: "REX-7", Call: "care", DryRun: true,
			Params: map[string]any{"portions": int64(2), "as-role": "sitter"},
		})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// The steps do not run, so a step parameter shows only when given.
		wantJSON(t, []any{result.DryRun[0].Name, result.DryRun[0].Value, result.DryRun[0].Given}, `["id","`+rex+`","REX-7"]`)
		wantJSON(t, []any{result.DryRun[1].Name, result.DryRun[1].Value, result.DryRun[1].Given}, `["portions",2,2]`)
		wantJSON(t, []any{result.DryRun[2].Name, result.DryRun[2].Value, result.DryRun[2].Given}, `["as-role","sitter","sitter"]`)
		if len(result.DryRun) != 3 {
			t.Errorf("DryRun = %+v, want the ID and two parameters", result.DryRun)
		}
		wantRequests(t, shop.api, "GET "+byTag)
	})

	t.Run("a call without parameters has none, which is not nothing", func(t *testing.T) {
		shop := callShop(t, nil)
		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "archive", DryRun: true})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantJSON(t, []any{len(result.DryRun), result.DryRun[0].Name}, `[1,"id"]`)
		wantRequests(t, shop.api)
	})

	t.Run("a payload that cannot be built stops a dry run", func(t *testing.T) {
		shop := callShop(t, nil)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "care-breed", DryRun: true, Params: map[string]any{"breed": beagle}})
		wantError(t, err, apikit.KindUsage, "care-breed needs at least one of --pure, --share")
	})
}
