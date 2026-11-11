package apitest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
)

// recorder stands in for the test a Server reports to, and keeps what the
// Server tells it.
type recorder struct {
	testing.TB
	mu     sync.Mutex
	errors []string
	fatal  []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// Fatalf ends the goroutine, as that of a test does.
func (r *recorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.fatal = append(r.fatal, fmt.Sprintf(format, args...))
	r.mu.Unlock()
	runtime.Goexit()
}

// send sends a request to the fake API and returns the status and the body
// of the answer.
func send(t *testing.T, api *apitest.Server, method, target, body string) (int, http.Header, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, target, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Shop-Token", "s3cret")
	resp, err := api.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer to %s %s: %v", method, target, err)
	}
	return resp.StatusCode, resp.Header, string(data)
}

func TestServerAnswersFromItsScript(t *testing.T) {
	tests := map[string]struct {
		response    apitest.Response
		status      int
		contentType string
		body        string
	}{
		"a body, as JSON": {
			apitest.Response{Body: map[string]any{"name": "Rex", "id": "0a1b2c3d", "tags": []any{"<calm>"}}},
			200, "application/json", `{"id":"0a1b2c3d","name":"Rex","tags":["<calm>"]}`,
		},
		"keys in the order of an object": {
			apitest.Response{Status: 201, Body: object(t, `{"name":"Rex","id":1.50}`)},
			201, "application/json", `{"name":"Rex","id":1.50}`,
		},
		"an empty list is a body":   {apitest.Response{Body: []any{}}, 200, "application/json", `[]`},
		"no body":                   {apitest.Response{Status: 204}, 204, "", ""},
		"a body that is not JSON":   {apitest.Response{Status: 502, Raw: "<h1>Bad Gateway</h1>"}, 502, "", "<h1>Bad Gateway</h1>"},
		"the body wins over Raw":    {apitest.Response{Body: "text", Raw: "raw"}, 200, "application/json", `"text"`},
		"headers of the script":     {apitest.Response{Body: 1, Header: http.Header{"Content-Type": {"text/plain"}}}, 200, "text/plain", `1`},
		"the first of a sequence":   {apitest.Response{Status: 500, Sequence: []apitest.Response{{Status: 503, Raw: "busy"}}}, 503, "", "busy"},
		"a status without its text": {apitest.Response{Status: 299}, 299, "", ""},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			api := apitest.New(t)
			api.Handle("GET", "/v1/pets/0a1b2c3d", test.response)
			status, header, body := send(t, api, "GET", api.URL()+"/v1/pets/0a1b2c3d", "")
			if status != test.status || body != test.body {
				t.Errorf("answer: %d %s, want %d %s", status, body, test.status, test.body)
			}
			if got := header.Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}
		})
	}
}

// object decodes a JSON object, which keeps the order of its keys.
func object(t *testing.T, text string) any {
	t.Helper()
	value, err := apikit.Unmarshal([]byte(text))
	if err != nil {
		t.Fatalf("Unmarshal(%s): %v", text, err)
	}
	return value
}

func TestServerRecordsRequests(t *testing.T) {
	api := apitest.New(t)
	if got := api.Requests(); len(got) != 0 {
		t.Errorf("a new Server has the requests %v", got)
	}
	api.Handle("post", "/v1/pets/a%2Fb/visits?reason=check+up&limit=5", apitest.Response{Status: 201})
	api.Handle("GET", "/v1/pets", apitest.Response{Body: []any{}})
	api.Handle("PUT", "/v1/pets", apitest.Response{})

	send(t, api, "POST", "https://admin.example.test:8443/v1/pets/a%2Fb/visits?reason=check+up&limit=5", `{"zeta":1.0,"alpha":[true]}`)
	send(t, api, "GET", "https://api.example.test/v1/pets", "")
	send(t, api, "PUT", "https://api.example.test/v1/pets", "not JSON")

	requests := api.Requests()
	if len(requests) != 3 {
		t.Fatalf("recorded %d requests, want 3", len(requests))
	}
	first := requests[0]
	if first.Method != "POST" || first.Host != "admin.example.test:8443" || first.Path != "/v1/pets/a%2Fb/visits" || first.Query != "reason=check+up&limit=5" {
		t.Errorf("the first request is %+v", first)
	}
	if got := first.Header.Get("X-Shop-Token"); got != "s3cret" {
		t.Errorf("the first request carries X-Shop-Token %q", got)
	}
	if encoded, _ := apikit.Marshal(first.Body); string(encoded) != `{"zeta":1.0,"alpha":[true]}` {
		t.Errorf("the body of the first request is %s", encoded)
	}
	if second := requests[1]; second.Method != "GET" || second.Host != "api.example.test" || second.Query != "" || second.Body != nil {
		t.Errorf("the second request is %+v", second)
	}
	if got := requests[2].Body; got != "not JSON" {
		t.Errorf("a body that is not JSON was recorded as %v", got)
	}

	// What Requests returns is the caller's.
	requests[0].Method = "changed"
	if got := api.Requests()[0].Method; got != "POST" {
		t.Errorf("changing the result of Requests changed the record: %q", got)
	}
}

func TestServerMatchesTheWholeURL(t *testing.T) {
	const scripted = "/v1/pets?species=long+hair&limit=100&tag=b&tag=a"
	matches := map[string]bool{
		"/v1/pets?species=long+hair&limit=100&tag=b&tag=a":   true,
		"/v1/pets?limit=100&species=long%20hair&tag=a&tag=b": true,
		"/v1/pets?tag=a&limit=100&tag=b&species=long+hair":   true,
		"/v1/pets?limit=100&species=long+hair&tag=a":         false,
		"/v1/pets?limit=100&species=longhair&tag=a&tag=b":    false,
		"/v1/pets?limit=100&species=long+hair&tag=a&tag=b&x": false,
		"/v1/pets":  false,
		"/v1/pets/": false,
		"/v1/Pets?species=long+hair&limit=100&tag=b&tag=a": false,
	}
	for target, match := range matches {
		t.Run(target, func(t *testing.T) {
			told := &recorder{TB: t}
			api := apitest.New(told)
			api.Handle("GET", scripted, apitest.Response{Body: []any{}})

			status, _, body := send(t, api, "GET", api.URL()+target, "")
			if match {
				if status != 200 || len(told.errors) != 0 {
					t.Errorf("answer: %d %s, and the test was told %v", status, body, told.errors)
				}
				return
			}
			// A request without an answer fails the test, and says which.
			want := "apitest: no response scripted for GET " + api.URL() + target
			if !slices.Equal(told.errors, []string{want}) {
				t.Errorf("the test was told %q, want %q", told.errors, want)
			}
			if status != http.StatusNotImplemented || body != want {
				t.Errorf("answer: %d %s, want 501 and the same message", status, body)
			}
			if got := len(api.Requests()); got != 1 {
				t.Errorf("recorded %d requests, want the one without an answer", got)
			}
		})
	}

	t.Run("a query that is not escaped as it should be", func(t *testing.T) {
		api := apitest.New(t)
		api.Handle("GET", "/v1/pets?discount=100%&name=%zz", apitest.Response{Raw: "found"})
		if _, _, body := send(t, api, "GET", api.URL()+"/v1/pets?name=%zz&discount=100%", ""); body != "found" {
			t.Errorf("answer: %q, want the scripted one", body)
		}
	})

	t.Run("the method counts", func(t *testing.T) {
		told := &recorder{TB: t}
		api := apitest.New(told)
		api.Handle("GET", "/v1/pets", apitest.Response{})
		send(t, api, "DELETE", api.URL()+"/v1/pets", "")
		if want := "apitest: no response scripted for DELETE " + api.URL() + "/v1/pets"; !slices.Equal(told.errors, []string{want}) {
			t.Errorf("the test was told %q, want %q", told.errors, want)
		}
	})
}

func TestServerAnswersForOneHostOrForAll(t *testing.T) {
	api := apitest.New(t)
	api.Handle("GET", "/v1/pets", apitest.Response{Raw: "any"})
	api.Handle("GET", "https://Admin.example.test/v1/pets", apitest.Response{Raw: "admin"})
	api.Handle("GET", "https://admin.example.test/v1/owners?limit=1", apitest.Response{Raw: "owners"})

	for target, want := range map[string]string{
		"https://admin.example.test/v1/pets":           "admin",
		"https://api.example.test/v1/pets":             "any",
		"https://api.staging.example.test/v1/pets":     "any",
		"http://admin.example.test/v1/pets":            "any",
		"https://admin.example.test:8443/v1/pets":      "any",
		"https://admin.example.test/v1/owners?limit=1": "owners",
	} {
		if _, _, body := send(t, api, "GET", target, ""); body != want {
			t.Errorf("GET %s was answered with %q, want %q", target, body, want)
		}
	}

	told := &recorder{TB: t}
	strict := apitest.New(told)
	strict.Handle("GET", "https://admin.example.test/v1/owners", apitest.Response{})
	send(t, strict, "GET", "https://api.example.test/v1/owners", "")
	if len(told.errors) != 1 {
		t.Errorf("a script for one host answered another: the test was told %q", told.errors)
	}
}

func TestServerAnswersInSequence(t *testing.T) {
	api := apitest.New(t)
	api.Handle("GET", "/v1/pets", apitest.Response{Sequence: []apitest.Response{
		{Status: 503, Raw: "busy"},
		{Status: 401},
		{Body: []any{}, Sequence: []apitest.Response{{Status: 500}}},
	}})
	api.Handle("GET", "/v1/owners", apitest.Response{Raw: "always"})

	var got []string
	for range 5 {
		status, _, body := send(t, api, "GET", api.URL()+"/v1/pets", "")
		got = append(got, fmt.Sprint(status, body))
		send(t, api, "GET", api.URL()+"/v1/owners", "")
	}
	// The last answer stays; a sequence inside a sequence means nothing.
	if want := []string{"503busy", "401", "200[]", "200[]", "200[]"}; !slices.Equal(got, want) {
		t.Errorf("answers: %q, want %q", got, want)
	}

	// A new script starts over.
	api.Handle("GET", "/v1/pets", apitest.Response{Sequence: []apitest.Response{{Raw: "one"}, {Raw: "two"}}})
	if _, _, body := send(t, api, "GET", api.URL()+"/v1/pets", ""); body != "one" {
		t.Errorf("the first answer of a new script is %q", body)
	}
}

func TestServerFailsRequests(t *testing.T) {
	api := apitest.New(t)
	refused := errors.New("connection refused")
	api.Handle("GET", "/v1/pets", apitest.Response{Sequence: []apitest.Response{{Err: refused}, {Raw: "back"}}})

	req, err := http.NewRequestWithContext(t.Context(), "GET", api.URL()+"/v1/pets", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := api.Client().Do(req)
	if !errors.Is(err, refused) {
		t.Errorf("Do = %v, %v; want the scripted error", resp, err)
	}
	if _, _, body := send(t, api, "GET", api.URL()+"/v1/pets", ""); body != "back" {
		t.Errorf("the answer after the failure is %q", body)
	}
	if got := len(api.Requests()); got != 2 {
		t.Errorf("recorded %d requests, want the failed one as well", got)
	}
}

func TestServerHoldsAnswersBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := apitest.New(t)
		release := make(chan struct{})
		api.Handle("GET", "/v1/pets", apitest.Response{Raw: "held", Until: release})

		get := func(ctx context.Context) (string, error) {
			req, err := http.NewRequestWithContext(ctx, "GET", api.URL()+"/v1/pets", nil)
			if err != nil {
				return "", err
			}
			resp, err := api.Client().Do(req)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			return string(data), err
		}

		impatient, giveUp := context.WithCancel(t.Context())
		var patientBody string
		var patientErr, impatientErr error
		var patient, other sync.WaitGroup
		patient.Go(func() { patientBody, patientErr = get(t.Context()) })
		other.Go(func() { _, impatientErr = get(impatient) })

		// Both requests have arrived and wait.
		synctest.Wait()
		if got := len(api.Requests()); got != 2 {
			t.Errorf("recorded %d requests while they are held, want 2", got)
		}
		giveUp()
		other.Wait()
		if !errors.Is(impatientErr, context.Canceled) {
			t.Errorf("the canceled request failed with %v, want context.Canceled", impatientErr)
		}

		close(release)
		patient.Wait()
		if patientErr != nil || patientBody != "held" {
			t.Errorf("the request that waited got %q, %v", patientBody, patientErr)
		}
	})
}

func TestServerDoesNotAnswerACanceledRequest(t *testing.T) {
	api := apitest.New(t)
	api.Handle("GET", "/v1/pets", apitest.Response{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", api.URL()+"/v1/pets", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := api.Client().Do(req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Do = %v, %v; want context.Canceled", resp, err)
	}
}

func TestServerRefusesAScriptItCannotFollow(t *testing.T) {
	tests := map[string]struct {
		target   string
		response apitest.Response
		want     string
	}{
		"a URL that is none":            {"/v1/%zz", apitest.Response{}, "apitest: cannot script GET /v1/%zz: "},
		"a body that cannot be encoded": {"/v1/pets", apitest.Response{Body: func() {}}, "apitest: cannot encode the body scripted for GET /v1/pets: "},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			told := &recorder{TB: t}
			api := apitest.New(told)
			// Handle ends the test it reports to, here a goroutine.
			var done sync.WaitGroup
			done.Go(func() { api.Handle("GET", test.target, test.response) })
			done.Wait()
			if len(told.fatal) != 1 || !strings.HasPrefix(told.fatal[0], test.want) {
				t.Errorf("the test was ended with %q, want %q", told.fatal, test.want)
			}
		})
	}
}

func TestServerIsSafeForConcurrentUse(t *testing.T) {
	api := apitest.New(t)
	api.Handle("GET", "/v1/pets", apitest.Response{Body: []any{}})
	var done sync.WaitGroup
	for i := range 16 {
		done.Go(func() {
			api.Handle("GET", fmt.Sprintf("/v1/pets/%d", i), apitest.Response{Status: 204})
			send(t, api, "GET", api.URL()+"/v1/pets", "")
			send(t, api, "GET", fmt.Sprintf("%s/v1/pets/%d", api.URL(), i), "")
			api.Requests()
		})
	}
	done.Wait()
	if got := len(api.Requests()); got != 32 {
		t.Errorf("recorded %d requests, want 32", got)
	}
}

func TestPetShop(t *testing.T) {
	doc, warnings, err := definition.Load(definition.Source{Name: "petshop.yaml", Data: []byte(apitest.PetShop)}, definition.LoadOptions{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Load: %v, warnings %v", err, warnings)
	}

	// Two environments with two APIs each, on hosts of their own.
	var urls []string
	for _, environment := range []string{"production", "staging"} {
		for _, api := range []string{"main", "admin"} {
			base, ok := doc.API(environment, api)
			if !ok {
				t.Errorf("environment %s has no API %s", environment, api)
			}
			urls = append(urls, base)
		}
	}
	want := []string{
		"https://api.example.test/v1", "https://admin.example.test/v1",
		"https://api.staging.example.test/v1", "https://admin.staging.example.test/v1",
	}
	if !slices.Equal(urls, want) {
		t.Errorf("base URLs: %v, want %v", urls, want)
	}
	if doc.Defaults.API != "admin" || doc.Defaults.ItemAPI != "main" {
		t.Errorf("Defaults = %+v, want listings on admin and items on main", doc.Defaults)
	}
	if native := doc.Identifiers.Native; native == nil || !native.MatchString("0a1b2c3d") || native.MatchString("REX-7") {
		t.Errorf("Identifiers.Native = %v, want eight hexadecimal digits", native)
	}

	// What the comment of PetShop promises.
	owners, pets, visits := doc.Resource("owners"), doc.Resource("pets"), doc.Resource("visits")
	breeds, orders, receipts := doc.Resource("breeds"), doc.Resource("orders"), doc.Resource("receipts")
	for _, resource := range []*definition.Resource{owners, pets, visits, breeds, orders, receipts} {
		if !resource.Defined() {
			t.Errorf("resource %s is not described", resource.Name)
		}
	}
	if len(owners.IDAliases) != 1 || !owners.IDAliases[0].Match.MatchString("4711") || owners.IDAliases[0].Field != "number" {
		t.Errorf("owners.IDAliases = %+v", owners.IDAliases)
	}
	if len(pets.IDAliases) != 1 || !pets.IDAliases[0].Match.MatchString("REX-7") || pets.IDAliases[0].Field != "tag" {
		t.Errorf("pets.IDAliases = %+v", pets.IDAliases)
	}
	if visits.Parent != "pets" || visits.API != "admin" {
		t.Errorf("visits: parent %q on API %q", visits.Parent, visits.API)
	}
	if breeds.API != "main" || len(breeds.IDSearch) != 1 || !breeds.IDSearch[0].Not || breeds.IDSearch[0].Column != "name" {
		t.Errorf("breeds: API %q, IDSearch %+v", breeds.API, breeds.IDSearch)
	}
	if !slices.Equal(orders.RequiredFilters, []string{"ownerId"}) || !orders.SupportsListing {
		t.Errorf("orders: RequiredFilters %v, SupportsListing %v", orders.RequiredFilters, orders.SupportsListing)
	}
	if receipts.SupportsListing {
		t.Error("receipts can be listed")
	}
	var filters []string
	for _, rules := range doc.FilterIDs {
		filters = append(filters, fmt.Sprintf("%s:%d", rules.Filter, len(rules.Rules)))
	}
	if !slices.Equal(filters, []string{"ownerId:1", "about:2"}) {
		t.Errorf("FilterIDs: %v", filters)
	}
}
