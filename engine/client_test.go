package engine_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// The tests in this file leave the client to the engine, so they need an
// API that listens: one on this machine.

// local returns an engine with a client of its own for an API at base.
// change may adjust the definition and the options first.
func local(t *testing.T, base string, change func(doc *definition.Document, o *engine.Options)) (*engine.Engine, *pauses) {
	t.Helper()
	source := "environments:\n  production:\n    apis:\n      main: " + base + "/v1\n"
	doc, _, err := definition.Load(definition.Source{Name: "local.yaml", Data: []byte(source)}, definition.LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	waits := &pauses{}
	options := engine.Options{Definition: doc, Sleep: waits.sleep}
	if change != nil {
		change(doc, &options)
	}
	made, err := engine.New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return made, waits
}

// headers records the headers of the requests a server received.
type headers struct {
	mu   sync.Mutex
	seen []http.Header
}

// answer records the headers of a request and answers it with a list.
func (h *headers) answer(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.seen = append(h.seen, r.Header.Clone())
	h.mu.Unlock()
	_, _ = w.Write([]byte(`[{"id":"0a1b2c3d"}]`))
}

// values returns the values of a header in every request received.
func (h *headers) values(name string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var values []string
	for _, header := range h.seen {
		values = append(values, header.Get(name))
	}
	return values
}

func TestOwnClientKeepsTheCredentialFromOtherHosts(t *testing.T) {
	tests := map[string]struct {
		authorize func(req *http.Request, token string)
		header    string
		value     string
	}{
		"the header of the engine": {nil, "Authorization", "Bearer s3cret"},
		// Package http knows nothing of this one.
		"a header of the definition": {
			func(req *http.Request, token string) { req.Header.Set("X-Shop-Token", token) }, "X-Shop-Token", "s3cret",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var elsewhere, home headers
			other := httptest.NewServer(http.HandlerFunc(elsewhere.answer))
			t.Cleanup(other.Close)
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/pets", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, other.URL+"/v1/pets", http.StatusTemporaryRedirect)
			})
			mux.HandleFunc("/v1/owners", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/v1/people", http.StatusTemporaryRedirect)
			})
			mux.HandleFunc("/v1/people", home.answer)
			api := httptest.NewServer(mux)
			t.Cleanup(api.Close)

			shop, _ := local(t, api.URL, func(_ *definition.Document, o *engine.Options) {
				o.Tokens, o.Authorize = &tokens{current: "s3cret"}, test.authorize
			})
			for _, resource := range []string{"pets", "owners"} {
				result, err := shop.Get(t.Context(), engine.GetRequest{Resource: resource, Filters: []engine.Filter{{Key: "trace", Value: "on"}}})
				if err != nil {
					t.Fatalf("Get %s: %v", resource, err)
				}
				wantJSON(t, result.Value, `[{"id":"0a1b2c3d"}]`)
			}

			if got := elsewhere.values(test.header); !slices.Equal(got, []string{""}) {
				t.Errorf("the other host received %s %q, want one request without it", test.header, got)
			}
			if got := home.values(test.header); !slices.Equal(got, []string{test.value}) {
				t.Errorf("the host itself received %s %q after a redirect, want %q", test.header, got, test.value)
			}
		})
	}
}

func TestOwnClientStopsFollowingRedirects(t *testing.T) {
	var hops atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, "/v1/pets", http.StatusFound)
	}))
	t.Cleanup(api.Close)

	shop, waits := local(t, api.URL, nil)
	_, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
	wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch pets", "stopped after 10 redirects")
	// The definition allows two retries, which would go the same way.
	if got := hops.Load(); got != 10 || len(waits.all()) != 0 {
		t.Errorf("%d requests were sent with %d pauses, want 10 and none", got, len(waits.all()))
	}
}

func TestOwnClientChecksCertificatesUnlessToldNotTo(t *testing.T) {
	var seen headers
	api := httptest.NewTLSServer(http.HandlerFunc(seen.answer))
	t.Cleanup(api.Close)
	once := func(doc *definition.Document, _ *engine.Options) { doc.HTTP.Retries = 0 }

	shop, _ := local(t, api.URL, once)
	_, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
	wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch pets", "certificate")
	if got := len(seen.values("Accept")); got != 0 {
		t.Errorf("%d requests reached an API whose certificate cannot be verified", got)
	}

	shop, _ = local(t, api.URL, func(doc *definition.Document, o *engine.Options) {
		once(doc, o)
		o.Insecure = true
	})
	result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
	if err != nil {
		t.Fatalf("Get with Insecure: %v", err)
	}
	wantJSON(t, result.Value, `[{"id":"0a1b2c3d"}]`)
}

func TestOwnClientWaitsAsLongAsTheDefinitionSays(t *testing.T) {
	const patience = 50 * time.Millisecond
	impatient := func(doc *definition.Document, _ *engine.Options) {
		doc.HTTP.TimeoutSeconds, doc.HTTP.Retries = patience.Seconds(), 1
	}

	t.Run("an API that does not answer is asked again", func(t *testing.T) {
		var requests atomic.Int32
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requests.Add(1) == 1 {
				// Say nothing until the client has given up.
				<-r.Context().Done()
				return
			}
			_, _ = w.Write([]byte(`[]`))
		}))
		t.Cleanup(api.Close)

		shop, waits := local(t, api.URL, impatient)
		result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		wantJSON(t, result.Value, `[]`)
		if got := requests.Load(); got != 2 || !slices.Equal(waits.all(), []time.Duration{time.Second}) {
			t.Errorf("%d requests were sent with the pauses %v, want 2 with one pause", got, waits.all())
		}
	})

	t.Run("an answer that takes its time is not cut off", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// The headers arrive in time, the listing after it.
			w.WriteHeader(http.StatusOK)
			_ = http.NewResponseController(w).Flush()
			time.Sleep(3 * patience)
			_, _ = w.Write([]byte(`[{"id":"0a1b2c3d"}]`))
		}))
		t.Cleanup(api.Close)

		shop, waits := local(t, api.URL, impatient)
		result, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		wantJSON(t, result.Value, `[{"id":"0a1b2c3d"}]`)
		if len(waits.all()) != 0 {
			t.Errorf("the request was sent again after %v", waits.all())
		}
	})

	t.Run("an API that cannot be reached", func(t *testing.T) {
		api := httptest.NewServer(http.NotFoundHandler())
		base := api.URL
		api.Close()

		shop, waits := local(t, base, impatient)
		_, err := shop.Get(t.Context(), engine.GetRequest{Resource: "pets"})
		wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch pets: GET "+base+"/v1/pets?limit=100: ")
		if len(waits.all()) != 1 {
			t.Errorf("paused %d times, want once before the second attempt", len(waits.all()))
		}
	})
}
