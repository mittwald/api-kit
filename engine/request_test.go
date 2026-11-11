package engine_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// The pet most tests ask for, and its URL.
const (
	rex    = "0a1b2c3d"
	rexURL = mainAPI + "/pets/" + rex
)

// getRex is the request for one pet as a fetch sends it.
func getRex() engine.Call {
	return engine.Call{Method: http.MethodGet, URL: rexURL, Resource: "pets", ID: rex}
}

// doerFunc makes a function a [engine.Doer].
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestRequestDecodesTheAnswer(t *testing.T) {
	tests := map[string]struct {
		response apitest.Response
		want     string
	}{
		// A call that creates something is answered with 201.
		"created, with a body":    {apitest.Response{Status: 201, Body: map[string]any{"visitId": "7"}}, `{"visitId":"7"}`},
		"no content":              {apitest.Response{Status: 204}, `null`},
		"no content, yet a body":  {apitest.Response{Status: 204, Raw: "done"}, `null`},
		"a success without body":  {apitest.Response{Status: 200}, `null`},
		"nothing but white space": {apitest.Response{Status: 202, Raw: " \r\n"}, `null`},
		"null":                    {apitest.Response{Raw: "null"}, `null`},
		"a list":                  {apitest.Response{Body: []any{}}, `[]`},
		// Keys keep their order, numbers their spelling.
		"order and numbers": {
			apitest.Response{Raw: `{"zeta":1.0,"alpha":12345678901234567890,"note":"<b> & co","weight":4.50}`},
			`{"zeta":1.0,"alpha":12345678901234567890,"note":"<b> & co","weight":4.50}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			shop.api.Handle("POST", "/v1/pets/"+rex+"/visits", test.response)
			got, err := shop.Request(t.Context(), engine.Call{Method: http.MethodPost, URL: rexURL + "/visits", Body: map[string]any{}})
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			wantJSON(t, got, test.want)
		})
	}

	t.Run("a success that is not JSON", func(t *testing.T) {
		shop := open(t, nil)
		shop.api.Handle("POST", "/v1/pets", apitest.Response{Raw: "<html>welcome</html>"})
		_, err := shop.Request(t.Context(), engine.Call{Method: http.MethodPost, URL: mainAPI + "/pets", Verb: "apply", Resource: "pets"})
		wantError(t, err, apikit.KindFailed, "API request failed while trying to apply pets: POST "+mainAPI+"/pets: the response is not JSON")
	})
}

func TestRequestSendsItsBodyAndHeaders(t *testing.T) {
	var body string
	var header http.Header
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		header = req.Header.Clone()
		body = ""
		if req.Body != nil {
			data, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			body = string(data)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})
	shop := open(t, func(_ *definition.Document, o *engine.Options) { o.HTTP = doer })

	// The body goes out as apikit.Marshal writes it: keys in their order,
	// numbers as they were spelled, no escapes for HTML.
	const payload = `{"note":"<b> & co","weight":4.50,"alpha":null,"tags":[]}`
	_, err := shop.Request(t.Context(), engine.Call{
		Method: http.MethodPatch, URL: rexURL, Body: decode(t, payload),
		Header: http.Header{"X-Order-Key": {"k-1"}},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if body != payload {
		t.Errorf("body sent: %s, want %s", body, payload)
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := header.Get("X-Order-Key"); got != "k-1" {
		t.Errorf("X-Order-Key = %q, want the header of the request", got)
	}

	if _, err := shop.Request(t.Context(), engine.Call{Method: http.MethodDelete, URL: rexURL}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if body != "" || header.Get("Content-Type") != "" {
		t.Errorf("a request without a body sent %q as %q", body, header.Get("Content-Type"))
	}
}

// broken is the body of a response that breaks off.
type broken struct{}

func (broken) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (broken) Close() error             { return nil }

func TestAnswerThatBreaksOffIsAskedForAgain(t *testing.T) {
	attempts := 0
	doer := doerFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusOK, Body: broken{}}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"0a1b2c3d"}`))}, nil
	})
	shop := open(t, func(_ *definition.Document, o *engine.Options) { o.HTTP = doer })

	got, err := shop.Request(t.Context(), getRex())
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	wantJSON(t, got, `{"id":"0a1b2c3d"}`)
	if attempts != 2 || !slices.Equal(shop.pauses.all(), []time.Duration{time.Second}) {
		t.Errorf("%d attempts with the pauses %v, want 2 with one pause", attempts, shop.pauses.all())
	}
}

func TestRequestCannotEncodeItsBody(t *testing.T) {
	shop := open(t, nil)
	_, err := shop.Request(t.Context(), engine.Call{Method: http.MethodPost, URL: rexURL, Body: func() {}, Verb: "apply", Resource: "pets"})
	wantError(t, err, apikit.KindFailed, "API request failed while trying to apply pets: the request body cannot be encoded")
	wantSent(t, shop.api)
}

func TestRequestToAURLThatIsNone(t *testing.T) {
	shop := open(t, nil)
	_, err := shop.Request(t.Context(), engine.Call{Method: http.MethodGet, URL: "https://api.example.test/%zz", Resource: "pets"})
	wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch pets")
	if waits := shop.pauses.all(); len(waits) != 0 {
		t.Errorf("a request that cannot be built was tried again after %v", waits)
	}
}

func TestAnswersToGetsAreRemembered(t *testing.T) {
	tests := map[int]bool{
		200: true, 204: true, 404: true,
		// A GET that is answered with another success is not done yet.
		201: false, 202: false,
		// Failures that may pass.
		400: false, 401: false, 403: false, 408: false, 429: false, 500: false, 503: false,
	}
	for status, remembered := range tests {
		t.Run(http.StatusText(status), func(t *testing.T) {
			shop := open(t, func(doc *definition.Document, _ *engine.Options) { doc.HTTP.Retries = 0 })
			shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Status: status, Body: map[string]any{"id": rex}})

			first, firstErr := shop.Request(t.Context(), getRex())
			second, secondErr := shop.Request(t.Context(), getRex())

			want := 2
			if remembered {
				want = 1
			}
			if got := len(shop.api.Requests()); got != want {
				t.Errorf("%d requests were sent, want %d", got, want)
			}
			if (firstErr == nil) != (secondErr == nil) || encode(t, first) != encode(t, second) {
				t.Errorf("the answers differ: %v, %v and %v, %v", first, firstErr, second, secondErr)
			}
			if firstErr != nil && firstErr.Error() != secondErr.Error() {
				t.Errorf("the errors differ: %v and %v", firstErr, secondErr)
			}
		})
	}
}

func TestRememberedNotFoundIsStillReported(t *testing.T) {
	shop := open(t, nil)
	shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Status: 404, Raw: "no such pet"})

	for range 2 {
		_, err := shop.Request(t.Context(), getRex())
		wantError(t, err, apikit.KindNotFound, "failed to fetch pet by ID "+rex+": 404 no such pet")
		failure, ok := errors.AsType[*engine.HTTPError](err)
		if !ok || failure.Status != 404 || failure.Body != "no such pet" {
			t.Errorf("the error wraps %+v, want the answer of the API", failure)
		}
	}
	wantSent(t, shop.api, "GET "+rexURL)
}

func TestOnlyGetsAreRemembered(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			shop := open(t, nil)
			shop.api.Handle(method, "/v1/pets/"+rex, apitest.Response{Status: 204})
			shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Body: map[string]any{"id": rex}})

			for range 2 {
				if _, err := shop.Request(t.Context(), engine.Call{Method: method, URL: rexURL}); err != nil {
					t.Fatalf("Request: %v", err)
				}
			}
			// What a request that changes something was answered says
			// nothing about the item.
			got, err := shop.Request(t.Context(), getRex())
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			wantJSON(t, got, `{"id":"`+rex+`"}`)
			wantSent(t, shop.api, method+" "+rexURL, method+" "+rexURL, "GET "+rexURL)
		})
	}
}

func TestRememberedValuesAreCopies(t *testing.T) {
	shop := open(t, nil)
	shop.api.Handle("GET", "/v1/pets?limit=100", apitest.Response{Raw: `[{"id":"0a1b2c3d","tags":["calm"]}]`})
	get := func() []any {
		result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return result.Items()
	}

	// Whoever gets the value first may do with it as it likes.
	first := get()
	item, _ := first[0].(*apikit.Object)
	item.Set("extra", "joined")
	tags, _ := item.Get("tags")
	tags.([]any)[0] = "wild"
	first[0] = nil

	wantJSON(t, get(), `[{"id":"0a1b2c3d","tags":["calm"]}]`)
	wantSent(t, shop.api, "GET "+adminAPI+"/pets?limit=100")
}

func TestGetsUnderWayShareOneRequest(t *testing.T) {
	tests := map[string]struct {
		response apitest.Response
		after    int
	}{
		"an answer that is remembered": {apitest.Response{Body: list(rex)}, 1},
		"an answer that is not":        {apitest.Response{Status: 500, Raw: "boom"}, 2},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				shop := open(t, nil)
				release := make(chan struct{})
				test.response.Until = release
				shop.api.Handle("GET", "/v1/pets?limit=100", test.response)
				get := func() (string, error) {
					result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
					if err != nil {
						return "", err
					}
					// Everyone gets a list of its own.
					result.Items()[0] = "mine"
					return encode(t, result.Value), nil
				}

				const callers = 8
				values, errs := make([]string, callers), make([]error, callers)
				var done sync.WaitGroup
				for i := range callers {
					done.Go(func() { values[i], errs[i] = get() })
				}
				// All callers are waiting now: one for the API, the others
				// for that one.
				synctest.Wait()
				if got := len(shop.api.Requests()); got != 1 {
					t.Errorf("%d requests are under way, want 1", got)
				}
				close(release)
				done.Wait()

				for i := range callers {
					if values[i] != values[0] || (errs[i] == nil) != (errs[0] == nil) {
						t.Errorf("caller %d got %q, %v; caller 0 got %q, %v", i, values[i], errs[i], values[0], errs[0])
					}
				}
				if got := len(shop.api.Requests()); got != 1 {
					t.Errorf("%d requests were sent, want 1", got)
				}
				// A caller that comes later is answered from memory, if
				// the answer is one to remember.
				_, _ = get()
				if got := len(shop.api.Requests()); got != test.after {
					t.Errorf("%d requests were sent after a later call, want %d", got, test.after)
				}
			})
		})
	}
}

func TestWaitingCallerAsksItselfWhenTheOtherGaveUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shop := open(t, nil)
		never := make(chan struct{})
		shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Sequence: []apitest.Response{
			{Until: never},
			{Body: map[string]any{"id": rex}},
		}})

		impatient, giveUp := context.WithCancel(t.Context())
		var first, second error
		var value any
		var done sync.WaitGroup
		done.Go(func() { _, first = shop.Request(impatient, getRex()) })
		synctest.Wait()
		done.Go(func() { value, second = shop.Request(t.Context(), getRex()) })
		synctest.Wait()
		wantSent(t, shop.api, "GET "+rexURL)

		giveUp()
		done.Wait()
		if !errors.Is(first, context.Canceled) {
			t.Errorf("the caller that gave up got %v, want context.Canceled", first)
		}
		if second != nil {
			t.Fatalf("the caller that waited got %v", second)
		}
		wantJSON(t, value, `{"id":"`+rex+`"}`)
		wantSent(t, shop.api, "GET "+rexURL, "GET "+rexURL)
	})
}

func TestWaitingCallerGivesUpOnItsOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shop := open(t, nil)
		release := make(chan struct{})
		shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Body: map[string]any{"id": rex}, Until: release})

		impatient, giveUp := context.WithCancel(t.Context())
		var first, second error
		var sender, waiter sync.WaitGroup
		sender.Go(func() { _, first = shop.Request(t.Context(), getRex()) })
		synctest.Wait()
		waiter.Go(func() { _, second = shop.Request(impatient, getRex()) })
		synctest.Wait()

		giveUp()
		waiter.Wait()
		if !errors.Is(second, context.Canceled) || apikit.KindOf(second) != apikit.KindCanceled {
			t.Errorf("the caller that gave up got %v, want context.Canceled", second)
		}
		close(release)
		sender.Wait()
		if first != nil {
			t.Errorf("the caller that sent the request got %v", first)
		}
		wantSent(t, shop.api, "GET "+rexURL)
	})
}

func TestRetries(t *testing.T) {
	lost := apitest.Response{Err: errors.New("connection reset by peer")}
	found := apitest.Response{Body: map[string]any{"id": rex}}
	status := func(code int) apitest.Response { return apitest.Response{Status: code, Raw: "try later"} }
	const second = time.Second

	tests := map[string]struct {
		method  string
		retries int
		delay   float64
		answers []apitest.Response
		// requests is how many requests are sent; status is that of the
		// error, 0 for a success and -1 for a request without an answer.
		requests int
		pauses   []time.Duration
		status   int
	}{
		"a GET is sent again after a status worth it and a lost connection": {
			http.MethodGet, 2, 1, []apitest.Response{status(503), lost, found}, 3, []time.Duration{second, 2 * second}, 0,
		},
		"the pause doubles every time": {
			http.MethodGet, 4, 0.5, []apitest.Response{status(502), status(503), status(504), lost, found}, 5,
			[]time.Duration{second / 2, second, 2 * second, 4 * second}, 0,
		},
		"a GET gives up after the retries": {
			http.MethodGet, 2, 1, []apitest.Response{status(502), status(502), status(502), found}, 3, []time.Duration{second, 2 * second}, 502,
		},
		"so it does without an answer": {
			http.MethodGet, 1, 1, []apitest.Response{lost, lost, found}, 2, []time.Duration{second}, -1,
		},
		"no retries, one attempt": {
			http.MethodGet, 0, 1, []apitest.Response{lost, found}, 1, nil, -1,
		},
		"a failure that will not pass is not tried again": {
			http.MethodGet, 2, 1, []apitest.Response{status(500), found}, 1, nil, 500,
		},
		"nor is a missing item": {
			http.MethodGet, 2, 1, []apitest.Response{status(404), found}, 1, nil, 404,
		},
		// It may have reached the API.
		"a POST is sent once, whatever the status": {
			http.MethodPost, 2, 1, []apitest.Response{status(503), found}, 1, nil, 503,
		},
		"and when the connection is lost": {
			http.MethodDelete, 2, 1, []apitest.Response{lost, found}, 1, nil, -1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, func(doc *definition.Document, _ *engine.Options) {
				doc.HTTP.Retries, doc.HTTP.RetryDelaySeconds = test.retries, test.delay
			})
			shop.api.Handle(test.method, "/v1/pets/"+rex, apitest.Response{Sequence: test.answers})

			got, err := shop.Request(t.Context(), engine.Call{Method: test.method, URL: rexURL, Resource: "pets", ID: rex})
			switch test.status {
			case 0:
				if err != nil {
					t.Fatalf("Request: %v", err)
				}
				wantJSON(t, got, `{"id":"`+rex+`"}`)
			case -1:
				wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch pets: "+test.method+" "+rexURL+": connection reset by peer")
				if _, ok := errors.AsType[*engine.HTTPError](err); ok {
					t.Errorf("a request without an answer failed with an HTTPError: %v", err)
				}
			default:
				failure, ok := errors.AsType[*engine.HTTPError](err)
				if !ok || failure.Status != test.status {
					t.Errorf("Request failed with %v, want status %d", err, test.status)
				}
			}
			if got := len(shop.api.Requests()); got != test.requests {
				t.Errorf("%d requests were sent, want %d", got, test.requests)
			}
			if got := shop.pauses.all(); !slices.Equal(got, test.pauses) {
				t.Errorf("paused for %v, want %v", got, test.pauses)
			}
		})
	}
}

func TestRetriesWaitUnlessTheRunEnds(t *testing.T) {
	// Without a Sleep of the caller's the engine waits itself.
	ownClock := func(_ *definition.Document, o *engine.Options) { o.Sleep = nil }

	t.Run("the pauses are waited for", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			shop := open(t, ownClock)
			shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Sequence: []apitest.Response{
				{Status: 503}, {Status: 503}, {Body: map[string]any{"id": rex}},
			}})
			start := time.Now()
			if _, err := shop.Request(t.Context(), getRex()); err != nil {
				t.Fatalf("Request: %v", err)
			}
			if waited := time.Since(start); waited != 3*time.Second {
				t.Errorf("waited %v between three attempts, want 1s and 2s", waited)
			}
		})
	})

	t.Run("a pause ends with the run", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			shop := open(t, ownClock)
			shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Status: 503})
			ctx, interrupt := context.WithCancel(t.Context())
			var err error
			var done sync.WaitGroup
			start := time.Now()
			done.Go(func() { _, err = shop.Request(ctx, getRex()) })
			// The request has failed once and waits for its second attempt.
			synctest.Wait()
			interrupt()
			done.Wait()

			if !errors.Is(err, context.Canceled) || apikit.KindOf(err) != apikit.KindCanceled {
				t.Errorf("Request failed with %v, want context.Canceled", err)
			}
			if waited := time.Since(start); waited != 0 {
				t.Errorf("the interrupted request still waited %v", waited)
			}
			wantSent(t, shop.api, "GET "+rexURL)
		})
	})
}

func TestCanceledRequest(t *testing.T) {
	shop := open(t, nil)
	shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Body: map[string]any{"id": rex}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := shop.Request(ctx, getRex())
	if !errors.Is(err, context.Canceled) || apikit.KindOf(err) != apikit.KindCanceled {
		t.Errorf("Request failed with %v (kind %q), want context.Canceled", err, apikit.KindOf(err))
	}
	if waits := shop.pauses.all(); len(waits) != 0 {
		t.Errorf("a canceled request was tried again after %v", waits)
	}

	// The failure of an interrupted run is not remembered.
	if _, err := shop.Request(t.Context(), getRex()); err != nil {
		t.Errorf("Request after the canceled one: %v", err)
	}
}

// authorizations returns the Authorization header of every request the
// fake API received.
func authorizations(api *apitest.Server) []string {
	var headers []string
	for _, request := range api.Requests() {
		headers = append(headers, request.Header.Get("Authorization"))
	}
	return headers
}

// errNotLoggedIn is a failure of a token source that is no matter of the
// credential alone.
var errNotLoggedIn = apikit.Errorf(apikit.KindUsage, "not logged in")

func TestRejectedTokenIsReplacedOnce(t *testing.T) {
	rejected := apitest.Response{Status: 401, Raw: "token expired"}
	found := apitest.Response{Body: map[string]any{"id": rex}}
	busy := apitest.Response{Status: 503}

	tests := map[string]struct {
		source  *tokens
		retries int
		answers []apitest.Response
		// sent are the tokens of the requests; message is that of the
		// error, empty for a success, and failure the error itself where
		// it is that of the token source.
		sent      []string
		message   string
		failure   error
		rejected  []string
		confirmed []string
	}{
		"a token that works is confirmed": {
			source: &tokens{current: "old", next: "new"}, answers: []apitest.Response{found},
			sent: []string{"Bearer old"}, confirmed: []string{"old"},
		},
		"a rejected token is replaced and the request sent again": {
			source: &tokens{current: "old", next: "new"}, answers: []apitest.Response{rejected, found},
			sent: []string{"Bearer old", "Bearer new"}, rejected: []string{"old"}, confirmed: []string{"new"},
		},
		"the new token gets all attempts again": {
			source: &tokens{current: "old", next: "new"}, retries: 1, answers: []apitest.Response{busy, rejected, busy, found},
			sent: []string{"Bearer old", "Bearer old", "Bearer new", "Bearer new"}, rejected: []string{"old"}, confirmed: []string{"new"},
		},
		"there is no other token": {
			source: &tokens{current: "old"}, answers: []apitest.Response{rejected, found},
			sent: []string{"Bearer old"}, message: "the session has ended", failure: errSessionEnded, rejected: []string{"old"},
		},
		"the new token is rejected as well, and that is it": {
			source: &tokens{current: "old", next: "new"}, answers: []apitest.Response{rejected, rejected, found},
			sent: []string{"Bearer old", "Bearer new"}, message: "API token rejected by the server, please re-authenticate", rejected: []string{"old"},
		},
		"the same token again is not worth a request": {
			source: &tokens{current: "old", next: "old"}, answers: []apitest.Response{rejected, found},
			sent: []string{"Bearer old"}, message: "API token rejected by the server, please re-authenticate", rejected: []string{"old"},
		},
		"no token": {
			source: &tokens{}, answers: []apitest.Response{found},
			message: "no API token available",
		},
		// The token source says why, and what kind of failure it is.
		"no token, and why": {
			source: &tokens{failure: errNotLoggedIn}, answers: []apitest.Response{found},
			message: "not logged in", failure: errNotLoggedIn,
		},
		// Only a success shows that the token is good.
		"a failure confirms nothing": {
			source: &tokens{current: "old"}, answers: []apitest.Response{{Status: 404}},
			sent: []string{"Bearer old"}, message: "failed to fetch pet by ID " + rex + ": 404",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, func(doc *definition.Document, o *engine.Options) {
				doc.HTTP.Retries, o.Tokens = test.retries, test.source
			})
			shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Sequence: test.answers})

			got, err := shop.Request(t.Context(), getRex())
			switch {
			case test.message == "" && err != nil:
				t.Fatalf("Request: %v", err)
			case test.message == "":
				wantJSON(t, got, `{"id":"`+rex+`"}`)
			case strings.HasPrefix(test.message, "failed to fetch"):
				wantError(t, err, apikit.KindNotFound, test.message)
			case test.failure != nil:
				if !errors.Is(err, test.failure) || err.Error() != test.message || apikit.KindOf(err) != apikit.KindOf(test.failure) {
					t.Errorf("Request failed with %v, want the error of the token source as it is: %v", err, test.failure)
				}
			default:
				wantError(t, err, apikit.KindUnauthenticated, test.message)
				if code := apikit.ExitCode(err); code != apikit.ExitFailed {
					t.Errorf("ExitCode = %d, want %d", code, apikit.ExitFailed)
				}
			}
			if got := authorizations(shop.api); !slices.Equal(got, test.sent) {
				t.Errorf("requests were sent with %q, want %q", got, test.sent)
			}
			if !slices.Equal(test.source.rejected, test.rejected) {
				t.Errorf("Rejected was called with %q, want %q", test.source.rejected, test.rejected)
			}
			if !slices.Equal(test.source.confirmed, test.confirmed) {
				t.Errorf("Confirmed was called with %q, want %q", test.source.confirmed, test.confirmed)
			}
		})
	}
}

func TestRejectedTokenIsNotRemembered(t *testing.T) {
	source := &tokens{current: "old"}
	shop := open(t, func(_ *definition.Document, o *engine.Options) { o.Tokens = source })
	shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Sequence: []apitest.Response{
		{Status: 401}, {Body: map[string]any{"id": rex}},
	}})

	_, err := shop.Request(t.Context(), getRex())
	wantError(t, err, apikit.KindUnauthenticated, "the session has ended")
	if _, err := shop.Request(t.Context(), getRex()); err != nil {
		t.Errorf("the request after the rejected one: %v", err)
	}
}

func TestTokenOfTheRequestBypassesMemoryAndTheTokenSource(t *testing.T) {
	source := &tokens{current: "own", next: "new"}
	shop := open(t, func(_ *definition.Document, o *engine.Options) { o.Tokens = source })
	shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Body: map[string]any{"id": rex}})
	shop.api.Handle("PATCH", "/v1/pets/"+rex, apitest.Response{Status: 401})
	asGuest := getRex()
	asGuest.Token = "guest"
	request := func(c engine.Call) {
		t.Helper()
		if _, err := shop.Request(t.Context(), c); err != nil {
			t.Fatalf("Request: %v", err)
		}
	}

	// A request with a token of its own is sent every time and leaves no
	// answer behind for others.
	request(asGuest)
	request(asGuest)
	if source.asked != 0 || len(source.confirmed) != 0 {
		t.Errorf("the token source was asked %d times and told of %q", source.asked, source.confirmed)
	}
	request(getRex())
	// Nor is it answered with what others left behind.
	request(getRex())
	request(asGuest)
	if got, want := authorizations(shop.api), []string{"Bearer guest", "Bearer guest", "Bearer own", "Bearer guest"}; !slices.Equal(got, want) {
		t.Errorf("requests were sent with %q, want %q", got, want)
	}

	// When it is rejected, there is no other token to try.
	_, err := shop.Request(t.Context(), engine.Call{Method: http.MethodPatch, URL: rexURL, Token: "guest"})
	wantError(t, err, apikit.KindUnauthenticated, "the token of the steps was rejected by the server")
	if len(source.rejected) != 0 || !slices.Equal(source.confirmed, []string{"own"}) {
		t.Errorf("the token source was told of the rejected %q and the confirmed %q", source.rejected, source.confirmed)
	}
}

func TestRequestsWithoutACredential(t *testing.T) {
	shop := open(t, nil)
	shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Body: map[string]any{"id": rex}})
	shop.api.Handle("GET", "/v1/owners/"+rex, apitest.Response{Status: 401, Raw: "who are you?"})

	if _, err := shop.Request(t.Context(), getRex()); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if header := shop.api.Requests()[0].Header; len(header) != 0 {
		t.Errorf("a request without a credential carries the headers %v", header)
	}

	// Without a credential a 401 is a failure like any other.
	_, err := shop.Request(t.Context(), engine.Call{Method: http.MethodGet, URL: mainAPI + "/owners/" + rex, Resource: "owners", ID: rex})
	wantError(t, err, apikit.KindFailed, "failed to fetch owner by ID "+rex+": 401 who are you?")
}

func TestAuthorize(t *testing.T) {
	shop := open(t, func(_ *definition.Document, o *engine.Options) {
		o.Tokens = &tokens{current: "s3cret"}
		o.Authorize = func(req *http.Request, token string) { req.Header.Set("X-Shop-Token", token) }
	})
	shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Body: map[string]any{"id": rex}})
	if _, err := shop.Request(t.Context(), getRex()); err != nil {
		t.Fatalf("Request: %v", err)
	}
	header := shop.api.Requests()[0].Header
	if header.Get("X-Shop-Token") != "s3cret" || header.Get("Authorization") != "" {
		t.Errorf("the request carries the headers %v, want the token in X-Shop-Token only", header)
	}
}

func TestHTTPError(t *testing.T) {
	tests := map[string]struct {
		call     engine.Call
		response apitest.Response
		kind     apikit.Kind
		message  string
		body     string
	}{
		"a missing item": {
			getRex(), apitest.Response{Status: 404, Raw: "no such pet"},
			apikit.KindNotFound, "failed to fetch pet by ID " + rex + ": 404 no such pet", "no such pet",
		},
		"a listing that fails": {
			engine.Call{Method: http.MethodGet, URL: rexURL, Resource: "pets"},
			apitest.Response{Status: 500, Raw: "<html><body><h1>Error</h1>\n<p>The kennel is on fire &amp; closed</p></body></html>"},
			apikit.KindFailed, "failed to fetch pets: 500 The kennel is on fire & closed", "The kennel is on fire & closed",
		},
		"a failure without a body": {
			engine.Call{Method: http.MethodGet, URL: rexURL, Resource: "pets"}, apitest.Response{Status: 400},
			apikit.KindFailed, "failed to fetch pets: 400", "",
		},
		"a JSON object is shown as it came": {
			getRex(), apitest.Response{Status: 422, Raw: `{"message": "<b>no</b>",` + "\n\n" + ` "code": 7}`},
			apikit.KindFailed, "failed to fetch pet by ID " + rex + `: 422 {"message": "<b>no</b>",` + "\n\n" + ` "code": 7}`, `{"message": "<b>no</b>",` + "\n\n" + ` "code": 7}`,
		},
		// Only a GET tells that something does not exist.
		"a call to something missing has failed": {
			engine.Call{Method: http.MethodPost, URL: rexURL, Verb: "apply", Resource: "pets", ID: rex}, apitest.Response{Status: 404, Raw: "no such pet"},
			apikit.KindFailed, "failed to apply pet by ID " + rex + ": 404 no such pet", "no such pet",
		},
		"the name of one item comes from the definition": {
			engine.Call{Method: http.MethodGet, URL: rexURL, Resource: "mice", ID: "m1"}, apitest.Response{Status: 403, Raw: "keep out"},
			apikit.KindFailed, "failed to fetch mouse by ID m1: 403 keep out", "keep out",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := open(t, nil)
			shop.api.Handle(test.call.Method, "/v1/pets/"+rex, test.response)

			_, err := shop.Request(t.Context(), test.call)
			wantError(t, err, test.kind)
			if err.Error() != test.message {
				t.Errorf("message:\n got %q\nwant %q", err, test.message)
			}
			failure, ok := errors.AsType[*engine.HTTPError](err)
			if !ok {
				t.Fatalf("the error wraps no HTTPError: %v", err)
			}
			want := engine.HTTPError{
				Method: test.call.Method, URL: rexURL, Status: test.response.Status, Body: test.body,
				Resource: test.call.Resource, ID: test.call.ID,
			}
			if *failure != want {
				t.Errorf("HTTPError:\n got %+v\nwant %+v", *failure, want)
			}
		})
	}

	failure := &engine.HTTPError{Method: "GET", URL: rexURL, Status: 404, Body: "no such pet"}
	if got, want := failure.Error(), "GET "+rexURL+": 404 no such pet"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	failure.Body = ""
	if got, want := failure.Error(), "GET "+rexURL+": 404"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestCleanBody(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"nothing":    {"", ""},
		"plain text": {"no such pet", "no such pet"},
		"a page of a gateway": {
			"<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\r\n<center><h1>502 Bad Gateway</h1></center>\r\n<hr><center>kennel/1.2</center>\r\n</body>\r\n</html>\r\n",
			"502 Bad Gateway\n502 Bad Gateway\nkennel/1.2",
		},
		"character references": {"<p>Tom &amp; Jerry &lt;3 &quot;cheese&quot; &#8211; &euro;5</p>", `Tom & Jerry <3 "cheese" – €5`},
		"lines that say nothing": {
			"<h1>Error</h1>\n\n  ERROR  \n<pre>Cannot GET /v1/pets</pre>\n \t\nerror: closed\n",
			"Cannot GET /v1/pets\nerror: closed",
		},
		"scripts, styles and comments hold no text": {
			"<style>\nbody { color: red }\n</style><!-- a\ncomment --><SCRIPT type=\"text/javascript\">alert('<b>')\n</SCRIPT >Closed<br/>today",
			"Closedtoday",
		},
		"a declaration and an instruction": {"<?xml version=\"1.0\"?><!DOCTYPE html>\n<error>full</error>", "full"},
		"a tag over two lines":             {"<a\n href=\"/\">home</a>", "home"},
		"signs that are no tags":           {"1 < 2 and 3 > 2, a<b", "1 < 2 and 3 > 2, a<b"},
		"a JSON object as it came":         {"{\"message\": \"<b>no</b> &amp; no\"}\n\n", "{\"message\": \"<b>no</b> &amp; no\"}\n\n"},
		"a JSON list is text":              {"[\n  \"closed\"\n\n]", "[\n\"closed\"\n]"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := engine.CleanBody(test.body); got != test.want {
				t.Errorf("CleanBody(%q)\n got %q\nwant %q", test.body, got, test.want)
			}
		})
	}
}

func TestCredentialStaysOutOfErrorsAndLogs(t *testing.T) {
	var log bytes.Buffer
	var lines sync.Mutex
	shop := open(t, func(doc *definition.Document, o *engine.Options) {
		doc.HTTP.Retries = 1
		o.Tokens = &tokens{current: "s3cret"}
		// A credential that travels in the URL shows in what package http
		// reports.
		o.Authorize = func(req *http.Request, token string) {
			query := req.URL.Query()
			query.Set("key", token)
			req.URL.RawQuery = query.Encode()
		}
		o.Log = slog.New(slog.NewTextHandler(lockedWriter{&lines, &log}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	shop.api.Handle("GET", "/v1/pets/"+rex+"?key=s3cret", apitest.Response{Err: errors.New("connection refused")})
	shop.api.Handle("GET", "/v1/owners/"+rex+"?key=s3cret", apitest.Response{Body: map[string]any{"id": rex}})

	_, err := shop.Request(t.Context(), getRex())
	wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch pets: GET "+rexURL+": connection refused")
	if _, err := shop.Request(t.Context(), engine.Call{Method: http.MethodGet, URL: mainAPI + "/owners/" + rex}); err != nil {
		t.Fatalf("Request: %v", err)
	}

	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the error tells the credential: %v", err)
	}
	logged := log.String()
	if strings.Contains(logged, "s3cret") {
		t.Errorf("the log tells the credential:\n%s", logged)
	}
	// One line per request that was sent.
	want := []string{
		`level=DEBUG msg=request method=GET url=` + rexURL + ` attempt=1 error="connection refused"`,
		`level=DEBUG msg=request method=GET url=` + rexURL + ` attempt=2 error="connection refused"`,
		`level=DEBUG msg=request method=GET url=` + mainAPI + `/owners/` + rex + ` attempt=1 status=200`,
	}
	got := strings.Split(strings.TrimSpace(logged), "\n")
	if len(got) != len(want) {
		t.Fatalf("logged %d lines, want %d:\n%s", len(got), len(want), logged)
	}
	for i, line := range got {
		if !strings.Contains(line, want[i]) || !strings.Contains(line, " duration=") {
			t.Errorf("line %d: %s\nwant it to hold: %s and a duration", i+1, line, want[i])
		}
	}
}

func TestLogNeverHoldsTheToken(t *testing.T) {
	var log bytes.Buffer
	var lines sync.Mutex
	shop := open(t, func(_ *definition.Document, o *engine.Options) {
		o.Tokens = &tokens{current: "s3cret", next: "n3w"}
		o.Log = slog.New(slog.NewJSONHandler(lockedWriter{&lines, &log}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	shop.api.Handle("GET", "/v1/pets/"+rex, apitest.Response{Sequence: []apitest.Response{
		{Status: 401}, {Body: map[string]any{"id": rex, "token": "t0ken-of-the-pet"}},
	}})
	if _, err := shop.Request(t.Context(), getRex()); err != nil {
		t.Fatalf("Request: %v", err)
	}
	logged := log.String()
	for _, secret := range []string{"s3cret", "n3w", "Bearer", "t0ken-of-the-pet"} {
		if strings.Contains(logged, secret) {
			t.Errorf("the log holds %q:\n%s", secret, logged)
		}
	}
	if got := strings.Count(logged, "\n"); got != 2 {
		t.Errorf("logged %d lines for 2 requests:\n%s", got, logged)
	}
}

// lockedWriter writes to a buffer that several goroutines log to.
type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
