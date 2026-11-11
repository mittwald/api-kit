package engine_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// The base URLs of the pet shop in production.
const (
	mainAPI  = "https://api.example.test/v1"
	adminAPI = "https://admin.example.test/v1"
)

// extras adds to the pet shop what only the tests of the engine need.
const extras = `
filterIds:
  breedId:
    - {resource: breeds}
resources:
  # Requests for an order carry a header from a sub-resource.
  orders:
    headers:
      - {name: X-Order-Key, column: key, resource: access}
  access:
    parent: orders
  # A sub-resource with required filters.
  invoices:
    parent: owners
    requiredFilters: [year, state]
  # A path and an ID field of its own.
  kennels:
    apiResource: boarding/kennels
    idField: code
  # A name for one item that is not the plural without its "s".
  mice:
    singular: mouse
  # Aliases without a field, with two parameters in the query, and one
  # without a pattern.
  toys:
    idAliases:
      - {match: "^t:", query: " label = ${id} , kind=toy "}
      - {match: "^t:b", query: "name=${id}"}
      - {query: "any=${id}"}
  # An alias whose field is no path.
  cages:
    idAliases:
      - {match: "^c", query: "q=${id}", field: "tag..code"}
  # An ID search with paths, and one without a pattern.
  shelters:
    idSearch:
      - {column: name}
      - {match: "^[0-9]", not: true, column: address.city, idColumn: ref.id}
`

// load loads the pet shop with the overlays merged into it.
func load(t *testing.T, overlays ...string) *definition.Document {
	t.Helper()
	options := definition.LoadOptions{}
	for _, overlay := range overlays {
		options.Overlays = append(options.Overlays, definition.Source{Name: "overlay.yaml", Data: []byte(overlay)})
	}
	doc, warnings, err := definition.Load(definition.Source{Name: "petshop.yaml", Data: []byte(apitest.PetShop)}, options)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("Load warned: %v", warnings)
	}
	return doc
}

// shop is an engine for the pet shop, with the fake API it talks to and
// what it was told to wait between attempts.
type shop struct {
	*engine.Engine
	doc    *definition.Document
	api    *apitest.Server
	pauses *pauses
}

// pauses records the waits between the attempts of a request and does not
// wait.
type pauses struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (p *pauses) sleep(ctx context.Context, d time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waits = append(p.waits, d)
	return ctx.Err()
}

func (p *pauses) all() []time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.waits)
}

// open returns an engine for the pet shop with the test's extras. change
// may adjust the definition and the options before the engine is made.
func open(t *testing.T, change func(doc *definition.Document, o *engine.Options)) *shop {
	t.Helper()
	doc := load(t, extras)
	api := apitest.New(t)
	waits := &pauses{}
	options := engine.Options{Definition: doc, HTTP: api.Client(), Sleep: waits.sleep}
	if change != nil {
		change(doc, &options)
	}
	made, err := engine.New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &shop{Engine: made, doc: doc, api: api, pauses: waits}
}

// errSessionEnded is what tokens fails with when it has no other token.
var errSessionEnded = apikit.Errorf(apikit.KindUnauthenticated, "the session has ended")

// tokens is a token source that hands out one token, and another one after
// the first was rejected.
type tokens struct {
	mu sync.Mutex
	// current is the token handed out.
	current string
	// next replaces current when it is rejected. Empty means there is no
	// other token.
	next string
	// failure is what Token fails with.
	failure error

	asked     int
	rejected  []string
	confirmed []string
}

func (s *tokens) Token(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked++
	return s.current, s.failure
}

func (s *tokens) Rejected(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = append(s.rejected, token)
	if s.next == "" {
		return "", errSessionEnded
	}
	s.current, s.next = s.next, ""
	return s.current, nil
}

func (s *tokens) Confirmed(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.confirmed = append(s.confirmed, token)
}

// decode parses JSON for a test.
func decode(t *testing.T, text string) any {
	t.Helper()
	value, err := apikit.Unmarshal([]byte(text))
	if err != nil {
		t.Fatalf("Unmarshal(%s): %v", text, err)
	}
	return value
}

// encode returns v as JSON.
func encode(t *testing.T, v any) string {
	t.Helper()
	data, err := apikit.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(data)
}

// wantJSON checks that got encodes to the wanted JSON, key order and
// number spelling included.
func wantJSON(t *testing.T, got any, want string) {
	t.Helper()
	if text := encode(t, got); text != want {
		t.Errorf("got %s, want %s", text, want)
	}
}

// wantError checks the kind of an error and that its message holds every
// given part.
func wantError(t *testing.T, err error, kind apikit.Kind, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want one of kind %q", kind)
	}
	if got := apikit.KindOf(err); got != kind {
		t.Errorf("KindOf(%q) = %q, want %q", err, got, kind)
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not contain %q", err, part)
		}
	}
}

// sent returns the requests the fake API received, one "METHOD host/path"
// each, with the query as it was sent.
func sent(api *apitest.Server) []string {
	var lines []string
	for _, request := range api.Requests() {
		line := request.Method + " https://" + request.Host + request.Path
		if request.Query != "" {
			line += "?" + request.Query
		}
		lines = append(lines, line)
	}
	return lines
}

// wantSent checks which requests the fake API received, in order.
func wantSent(t *testing.T, api *apitest.Server, want ...string) {
	t.Helper()
	if got := sent(api); !slices.Equal(got, want) {
		t.Errorf("requests sent:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// list returns a JSON list of items that have nothing but an ID each.
func list(ids ...string) []any {
	items := make([]any, len(ids))
	for i, id := range ids {
		items[i] = map[string]any{"id": id}
	}
	return items
}

// ids returns the IDs of the items of a list.
func ids(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("got %s, want a list", encode(t, value))
	}
	out := make([]string, len(items))
	for i, item := range items {
		object, _ := item.(*apikit.Object)
		id, _ := object.Get("id")
		out[i] = apikit.Text(id)
	}
	return out
}

func TestNew(t *testing.T) {
	doc := load(t)
	api := apitest.New(t)

	if _, err := engine.New(engine.Options{Definition: doc, HTTP: api.Client(), Environment: "staging", ForceAPI: "admin"}); err != nil {
		t.Errorf("New with an environment and an API of the definition: %v", err)
	}

	tests := map[string]struct {
		options engine.Options
		kind    apikit.Kind
		message string
	}{
		"no definition":       {engine.Options{}, apikit.KindDefinition, "needs a definition"},
		"unknown environment": {engine.Options{Definition: doc, Environment: "test"}, apikit.KindUsage, `unknown environment "test", the definition has: production, staging`},
		"unknown API":         {engine.Options{Definition: doc, ForceAPI: "public"}, apikit.KindUsage, `unknown API "public", the definition has: main, admin`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := engine.New(test.options)
			wantError(t, err, test.kind, test.message)
		})
	}
}

func TestResultItems(t *testing.T) {
	tests := map[string]struct {
		value string
		want  string
	}{
		"a list is taken as it is":   {`[{"id":"a"},{"id":"b"}]`, `[{"id":"a"},{"id":"b"}]`},
		"an empty list has none":     {`[]`, `[]`},
		"nothing has none":           {`null`, `null`},
		"an object is one item":      {`{"id":"a"}`, `[{"id":"a"}]`},
		"so is anything else":        {`"a"`, `["a"]`},
		"an empty object is an item": {`{}`, `[{}]`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			result := &engine.Result{Value: decode(t, test.value)}
			items := result.Items()
			if test.want == "null" {
				if items != nil {
					t.Errorf("Items() = %v, want nil", items)
				}
				return
			}
			wantJSON(t, items, test.want)
		})
	}
}
