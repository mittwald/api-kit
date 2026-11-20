package engine_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

// formOverlay adds to the pet shop calls that upload photos, post with
// photos, remind of a pet at a time and tag a pet.
//
//   - "upload" sends photos as a form, with a ticket in a header that the
//     step before it gets.
//   - "post" uploads each photo with a step and sends the IDs of the
//     uploads, and checks its text as markdown.
//   - "remind" takes a time, a preset or a time relative to now.
//   - "tag" adds tags to those the pet has and removes others.
const formOverlay = `
resources:
  pets:
    setters:
      - name: open-upload
        pathTemplate: "pets/${id}/uploads"
      - name: upload
        pathTemplate: "photos"
        encoding: multipart
        headers: {X-Ticket: "${ticket}", X-Caption: "${caption}"}
        payloadTemplate: {photo: "${photo}", caption: "${caption}"}
        parameters:
          - {name: photo, type: file, list: true}
          - {name: caption, required: false}
        steps:
          - {as: ticket, call: open-upload, resource: pets, id: "${id}", select: ticket}
      - name: post
        pathTemplate: "pets/${id}/posts"
        payloadTemplate: {text: "${text}", photoIds: "${photos}"}
        parameters:
          - {name: text, format: markdown}
          - {name: photo, type: file, list: true, required: false}
        steps:
          - {as: photos, call: upload, resource: pets, id: "${id}", each: photo, parameters: {photo: "${photo}"}, select: id}
      - name: remind
        pathTemplate: "pets/${id}/reminders"
        payloadTemplate: {at: "${at}"}
        parameters:
          - name: at
            type: time
            timezone: Europe/Berlin
            presets: {soon: "+1h", morning: "tomorrow 08:00"}
      - name: tag
        method: PUT
        pathTemplate: "pets/${id}/tags"
        payloadTemplate: {tagIds: "${tags}"}
        parameters:
          - {name: add, type: list, required: false}
          - {name: remove, type: list, required: false}
        steps:
          - {as: tags, get: pets, id: "${id}", select: tags.tagId, add: ["${add}"], remove: ["${remove}"]}
`

// photo writes a file of the test and returns its path.
func photo(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFormCarriesFilesAndFields(t *testing.T) {
	shop := callShop(t, nil, formOverlay)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/uploads", apitest.Response{Body: map[string]any{"ticket": "t1ck3t"}})
	shop.api.Handle("POST", mainAPI+"/photos", apitest.Response{Status: 201, Body: map[string]any{"id": "ph-1"}})
	first, second := photo(t, "rex.png", "\x89PNG\r\n\x1a\nfirst"), photo(t, "notes", "plain text")

	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "upload",
		Params: map[string]any{"photo": []any{first, second}, "caption": "Rex & me"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	requests := shop.api.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests: %d, want the step and the upload", len(requests))
	}
	upload := requests[1]
	if got := upload.Header.Get("X-Ticket"); got != "t1ck3t" {
		t.Errorf("X-Ticket = %q, want the ticket of the step", got)
	}
	if got := upload.Header.Get("X-Caption"); got != "Rex & me" {
		t.Errorf("X-Caption = %q", got)
	}
	if got := upload.Header.Get("Authorization"); got != "Bearer own" {
		t.Errorf("Authorization = %q, want the own credential besides the headers", got)
	}
	kind := upload.Header.Get("Content-Type")
	if !strings.HasPrefix(kind, "multipart/form-data; boundary=") {
		t.Fatalf("Content-Type = %q", kind)
	}
	body, _ := upload.Body.(string)
	for _, want := range []string{
		`Content-Disposition: form-data; name="photo"; filename="rex.png"`,
		"Content-Type: image/png",
		"first",
		`Content-Disposition: form-data; name="photo"; filename="notes"`,
		"Content-Type: text/plain; charset=utf-8",
		"plain text",
		`Content-Disposition: form-data; name="caption"`,
		"Rex & me",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %q:\n%s", want, body)
		}
	}
}

func TestFormLeavesOutWhatWasNotGiven(t *testing.T) {
	shop := callShop(t, nil, formOverlay)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/uploads", apitest.Response{Body: map[string]any{"ticket": "t1ck3t"}})
	shop.api.Handle("POST", mainAPI+"/photos", apitest.Response{Status: 201, Body: map[string]any{"id": "ph-1"}})

	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "upload",
		Params: map[string]any{"photo": photo(t, "rex.png", "png")}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	upload := shop.api.Requests()[1]
	if _, sent := upload.Header["X-Caption"]; sent {
		t.Errorf("a header without a value was sent: %q", upload.Header.Values("X-Caption"))
	}
	if body, _ := upload.Body.(string); strings.Contains(body, `name="caption"`) {
		t.Errorf("the form has a field without a value:\n%s", body)
	}
}

func TestFileThatDoesNotExistSendsNothing(t *testing.T) {
	shop := callShop(t, nil, formOverlay)
	missing := filepath.Join(t.TempDir(), "gone.png")

	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "upload",
		Params: map[string]any{"photo": []any{photo(t, "rex.png", "png"), missing}}})
	if apikit.KindOf(err) != apikit.KindUsage || !strings.Contains(err.Error(), "--photo "+missing+": no such file") {
		t.Errorf("Apply: %v, want a usage error that names the file", err)
	}
	_, err = shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "upload",
		Params: map[string]any{"photo": t.TempDir()}})
	if apikit.KindOf(err) != apikit.KindUsage || !strings.Contains(err.Error(), "is not a file") {
		t.Errorf("Apply of a directory: %v, want a usage error", err)
	}
	wantRequests(t, shop.api)
}

func TestHeadersMayGiveTheTypeOfJSON(t *testing.T) {
	shop := callShop(t, nil, `
resources:
  pets:
    setters:
      - name: vendor
        pathTemplate: "pets/${id}/vendor"
        headers: {Content-Type: application/vnd.shop+json}
        payloadTemplate: {name: "${name}"}
        parameters: [{name: name}]
`)
	shop.api.Handle("POST", petURL+"/vendor", noContent)
	if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "vendor", Params: map[string]any{"name": "Rex"}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := shop.api.Requests()[0].Header.Get("Content-Type"); got != "application/vnd.shop+json" {
		t.Errorf("Content-Type = %q, want that of the setter", got)
	}
}

func TestHeadersAreKeptOutOfErrors(t *testing.T) {
	shop := callShop(t, nil, formOverlay)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/uploads", apitest.Response{Body: map[string]any{"ticket": "t1ck3t"}})
	shop.api.Handle("POST", mainAPI+"/photos", apitest.Response{Status: 400, Raw: `{"message":"ticket t1ck3t has expired"}`})

	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "upload",
		Params: map[string]any{"photo": photo(t, "rex.png", "png")}})
	if err == nil || strings.Contains(err.Error(), "t1ck3t") || !strings.Contains(err.Error(), "has expired") {
		t.Errorf("Apply: %v, want the refusal without the ticket", err)
	}
}

func TestHeadersFromStepsAreKeptOutOfTheErrorsOfLaterSteps(t *testing.T) {
	shop := callShop(t, nil, formOverlay, `
resources:
  pets:
    setters:
      - name: checked-upload
        pathTemplate: "photos"
        encoding: multipart
        headers: {X-Ticket: "${ticket}"}
        payloadTemplate: {photo: "${photo}"}
        parameters: [{name: photo, type: file}]
        steps:
          - {as: ticket, call: open-upload, resource: pets, id: "${id}", select: ticket}
          - {as: second, call: open-upload, resource: pets, id: "${id}", select: ticket}
`)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/uploads", apitest.Response{Sequence: []apitest.Response{
		{Body: map[string]any{"ticket": "t1ck3t"}},
		{Status: 409, Raw: `{"message":"ticket t1ck3t is still open"}`},
	}})

	// The step after the one that got the ticket fails, before the
	// request that sends the ticket in a header.
	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "checked-upload",
		Params: map[string]any{"photo": photo(t, "a.png", "a")}})
	if err == nil || strings.Contains(err.Error(), "t1ck3t") || !strings.Contains(err.Error(), "is still open") {
		t.Errorf("Apply: %v, want the refusal without the ticket", err)
	}
}

func TestStepRunsForEachItemOfAList(t *testing.T) {
	shop := callShop(t, nil, formOverlay)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/uploads", apitest.Response{Body: map[string]any{"ticket": "t1ck3t"}})
	shop.api.Handle("POST", mainAPI+"/photos", apitest.Response{Sequence: []apitest.Response{
		{Status: 201, Body: map[string]any{"id": "ph-1"}},
		{Status: 201, Body: map[string]any{"id": "ph-2"}},
	}})
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/posts", noContent)

	first, second := photo(t, "a.png", "a"), photo(t, "b.png", "b")
	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "post",
		Params: map[string]any{"text": "Look", "photo": []any{first, second}}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	lines := requestLines(t, shop.api)
	// One upload for each photo, each with a ticket of its own, and the
	// post with the IDs of both.
	if len(lines) != 5 || lines[4] != "POST "+mainAPI+"/pets/"+rex+`/posts {"text":"Look","photoIds":["ph-1","ph-2"]}` {
		t.Errorf("requests:\n  %s", strings.Join(lines, "\n  "))
	}
	bodies := []string{}
	for _, request := range shop.api.Requests() {
		if body, ok := request.Body.(string); ok {
			bodies = append(bodies, body)
		}
	}
	if len(bodies) != 2 || !strings.Contains(bodies[0], `filename="a.png"`) || !strings.Contains(bodies[1], `filename="b.png"`) {
		t.Errorf("uploads: %q, want one for each photo in order", bodies)
	}
}

func TestStepForEachItemDoesNotRunWithoutTheList(t *testing.T) {
	shop := callShop(t, nil, formOverlay)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/posts", noContent)

	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "post", Params: map[string]any{"text": "No photos"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The part of the payload that the step stands for goes with it.
	wantRequests(t, shop.api, "POST "+mainAPI+"/pets/"+rex+`/posts {"text":"No photos"}`)
}

func TestFormatsCheckValues(t *testing.T) {
	var checked []string
	shop := callShop(t, func(o *engine.Options) {
		o.Formats = map[string]func(string) []string{"markdown": func(value string) []string {
			checked = append(checked, value)
			if strings.Contains(value, "<b>") {
				return []string{"HTML is shown as text", "second mistake"}
			}
			return nil
		}}
	}, formOverlay)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/posts", noContent)

	result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "post", Params: map[string]any{"text": "<b>Rex</b>"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := []string{"--text: HTML is shown as text", "--text: second mistake"}; !slices.Equal(result.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", result.Warnings, want)
	}
	// The call is sent all the same.
	wantRequests(t, shop.api, "POST "+mainAPI+"/pets/"+rex+`/posts {"text":"<b>Rex</b>"}`)

	// A template is checked as it was rendered, and a dry run too.
	result, err = shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "post", DryRun: true,
		Params: map[string]any{"text": "fine"}})
	if err != nil || len(result.Warnings) != 0 || checked[len(checked)-1] != "fine" {
		t.Errorf("dry run: %v, warnings %q, checked %q", err, result.Warnings, checked)
	}
}

func TestTimesAreSentInTheirTimeZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	now := time.Date(2026, time.November, 20, 10, 15, 0, 0, time.UTC)
	tests := map[string]string{
		"soon":                      "2026-11-20T12:15:00+01:00",
		"morning":                   "2026-11-21T08:00:00+01:00",
		"+2d 09:30":                 "2026-11-22T09:30:00+01:00",
		"2026-12-24T18:00":          "2026-12-24T18:00:00+01:00",
		"2026-12-24T17:00:00Z":      "2026-12-24T18:00:00+01:00",
		"2026-06-01T08:00:00+02:00": "2026-06-01T08:00:00+02:00",
		"2026-12-24T17:00:00.750Z":  "2026-12-24T18:00:00.75+01:00",
	}
	for given, want := range tests {
		shop := callShop(t, func(o *engine.Options) { o.Now = func() time.Time { return now.In(berlin) } }, formOverlay)
		shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/reminders", noContent)
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "remind", Params: map[string]any{"at": given}}); err != nil {
			t.Errorf("--at %s: %v", given, err)
			continue
		}
		wantRequests(t, shop.api, "POST "+mainAPI+"/pets/"+rex+`/reminders {"at":"`+want+`"}`)
	}

	shop := callShop(t, func(o *engine.Options) { o.Now = func() time.Time { return now } }, formOverlay)
	result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "remind", DryRun: true, Params: map[string]any{"at": "soon"}})
	if err != nil || len(result.DryRun) != 2 || result.DryRun[1].Value != "2026-11-20T12:15:00+01:00" || result.DryRun[1].Given != "soon" {
		t.Errorf("dry run: %v, %+v, want the time with what was given", err, result)
	}
	_, err = shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "remind", Params: map[string]any{"at": "later"}})
	if apikit.KindOf(err) != apikit.KindUsage || !strings.Contains(err.Error(), `--at "later" is no time`) {
		t.Errorf("Apply with no time: %v, want a usage error", err)
	}
	wantRequests(t, shop.api)
}

func TestStepAddsToAndRemovesFromAList(t *testing.T) {
	tests := map[string]struct {
		tags   string
		params map[string]any
		want   string
	}{
		"adds what is missing": {
			tags: `[{"tagId":"t-1"},{"tagId":"t-2"}]`, params: map[string]any{"add": []any{"t-3", "t-1"}},
			want: `{"tagIds":["t-1","t-2","t-3"]}`,
		},
		"removes what is there": {
			tags: `[{"tagId":"t-1"},{"tagId":"t-2"}]`, params: map[string]any{"remove": []any{"t-1", "t-9"}},
			want: `{"tagIds":["t-2"]}`,
		},
		"removes the last one": {
			tags: `[{"tagId":"t-1"}]`, params: map[string]any{"remove": []any{"t-1"}},
			want: `{"tagIds":[]}`,
		},
		"adds to a list the item lacks": {
			tags: `null`, params: map[string]any{"add": []any{"t-1"}},
			want: `{"tagIds":["t-1"]}`,
		},
		"does both": {
			tags: `[{"tagId":"t-1"}]`, params: map[string]any{"add": []any{"t-2"}, "remove": []any{"t-1"}},
			want: `{"tagIds":["t-2"]}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, formOverlay)
			shop.api.Handle("GET", petURL, apitest.Response{Raw: `{"id":"` + rex + `","tags":` + tt.tags + `}`})
			shop.api.Handle("PUT", petURL+"/tags", noContent)
			if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "tag", Params: tt.params}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, "GET "+petURL, "PUT "+petURL+"/tags "+tt.want)
		})
	}
}

func TestHeadersFromParametersAreKeptOutOfTheErrorsOfSteps(t *testing.T) {
	shop := callShop(t, nil, formOverlay, `
resources:
  pets:
    setters:
      - name: open-with
        pathTemplate: "pets/${id}/uploads"
        payloadTemplate: {code: "${code}"}
        parameters: [{name: code}]
      - name: coded-upload
        pathTemplate: "photos"
        headers: {X-Code: "${code}"}
        parameters: [{name: code}]
        steps:
          - {as: ticket, call: open-with, resource: pets, id: "${id}", parameters: {code: "${code}"}, select: ticket}
`)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/uploads", apitest.Response{Status: 409, Raw: `{"message":"code s3cr3t is used up"}`})

	_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "coded-upload", Params: map[string]any{"code": "s3cr3t"}})
	if err == nil || strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), "is used up") {
		t.Errorf("Apply: %v, want the refusal without the code", err)
	}
}

func TestGetOfASetterWithHeadersIsSentEachTime(t *testing.T) {
	shop := callShop(t, nil, `
resources:
  pets:
    setters:
      - name: peek
        method: GET
        pathTemplate: "pets/${id}"
        headers: {X-As: "${as}"}
        parameters: [{name: as}]
`)
	shop.api.Handle("GET", petURL, apitest.Response{Body: map[string]any{"id": rex}})
	for _, as := range []string{"ada", "bob"} {
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "peek", Params: map[string]any{"as": as}}); err != nil {
			t.Fatalf("Apply as %s: %v", as, err)
		}
	}
	requests := shop.api.Requests()
	if len(requests) != 2 || requests[0].Header.Get("X-As") != "ada" || requests[1].Header.Get("X-As") != "bob" {
		t.Errorf("requests: %d, want one for each header", len(requests))
	}
}

func TestFormatsCheckTheValuesOfCallSteps(t *testing.T) {
	shop := callShop(t, func(o *engine.Options) {
		o.Formats = map[string]func(string) []string{"markdown": func(value string) []string {
			if strings.Contains(value, "<") {
				return []string{"HTML in " + value}
			}
			return nil
		}}
	}, formOverlay, `
resources:
  pets:
    setters:
      - name: announce
        pathTemplate: "pets/${id}/announcements"
        payloadTemplate: {title: "${title}"}
        parameters:
          - {name: title, format: markdown}
          - {name: text}
        then:
          - {as: post, call: post, resource: pets, id: "${id}", parameters: {text: "${text}"}, select: id}
      - name: repeat
        pathTemplate: "pets/${id}/repeats"
        payloadTemplate: {text: "${text}"}
        parameters: [{name: text, format: markdown}]
        steps:
          - {as: post, call: post, resource: pets, id: "${id}", parameters: {text: "${text}"}, select: id}
`)
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/announcements", apitest.Response{Status: 201, Body: map[string]any{"id": rex}})
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/posts", apitest.Response{Status: 201, Body: map[string]any{"id": "post-1"}})
	shop.api.Handle("POST", mainAPI+"/pets/"+rex+"/repeats", noContent)

	// The text is checked by the call of the step after the request.
	result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "announce",
		Params: map[string]any{"title": "<h1>", "text": "<b>"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := []string{"--title: HTML in <h1>", "--text: HTML in <b>"}; !slices.Equal(result.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", result.Warnings, want)
	}

	// A value that a step passes on as it was given is reported once.
	result, err = shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "repeat", Params: map[string]any{"text": "<b>"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := []string{"--text: HTML in <b>"}; !slices.Equal(result.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", result.Warnings, want)
	}
}
