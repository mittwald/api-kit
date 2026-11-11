package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/definition"
)

// spec is the definition the tests work from. Every token, refresh token
// and password in the tests ends in "-token" or "-secret", which no
// message does: that is how the fixture finds a secret that leaked.
const spec = `
environments:
  production:
    apis:
      main: https://api.example.test/v1/
auth:
  header: {name: X-Token, value: "${token}"}
  sources:
    - store
    - {env: EXAMPLE_TOKEN, refreshEnv: EXAMPLE_REFRESH_TOKEN}
    - {env: EXAMPLE_CI_TOKEN, refreshEnv: EXAMPLE_CI_REFRESH_TOKEN}
    - {env: EXAMPLE_FIXED_TOKEN}
  validate:
    path: tokens/check
    body: {}
  refresh:
    method: PUT
    path: sessions
    body: {refreshToken: "${refreshToken}"}
  login:
    fallback: password
    methods:
      password:
        kind: password
        prompts:
          - {name: username, label: Username}
          - {name: password, label: Password, secret: true}
        request: {path: login, body: {email: "${username}", password: "${password}"}}
        session: {token: {field: token, match: "^[^:]+:[^:]+"}, refreshToken: refreshToken, expiresAt: expires}
      sso:
        kind: browser
        url: {path: sso/start, query: {redirectUri: "${callbackUrl}"}}
        callback: {submit: {path: tokens/show}}
        prompt: {label: Paste the token, hint: Copy the token from the browser response.}
        session: {token: [accessToken, token], refreshToken: refreshToken, expiresAt: [expiresAt, expires]}
`

// The requests of the definition, as the fake API knows them.
const (
	checkRoute   = "POST /v1/tokens/check"
	refreshRoute = "PUT /v1/sessions"
	loginRoute   = "POST /v1/login"
)

const account = "production"

// start is the time of the injected clock.
var start = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

// fixture is a Manager's surroundings: a fake API, a store, a user and an
// environment. A test changes them, and the options, before it calls
// manager.
type fixture struct {
	t       *testing.T
	api     *fakeAPI
	store   *memoryStore
	ui      *fakeUI
	env     map[string]string
	log     *bytes.Buffer
	options auth.Options
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureOf(t, spec)
}

// newFixtureOf returns a fixture for a definition of its own.
func newFixtureOf(t *testing.T, source string) *fixture {
	t.Helper()
	doc, _, err := definition.Load(definition.Source{Name: "spec.yaml", Data: []byte(source)}, definition.LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := &fixture{
		t:     t,
		api:   &fakeAPI{t: t, verdicts: map[string]int{}, sessions: map[string]string{}, routes: map[string]func(call) reply{}},
		store: &memoryStore{values: map[string]string{}},
		ui:    &fakeUI{t: t, answers: map[string]string{}},
		env:   map[string]string{},
		log:   &bytes.Buffer{},
	}
	f.options = auth.Options{
		Config: doc.Auth,
		BaseURL: func(api string) (string, error) {
			url, ok := doc.API("", api)
			if !ok {
				return "", fmt.Errorf("no API %q", api)
			}
			return url, nil
		},
		HTTP:    f.api,
		Store:   f.store,
		Account: account,
		Getenv:  func(name string) string { return f.env[name] },
		UI:      f.ui,
		AppName: "petctl",
		Log:     slog.New(slog.NewTextHandler(f.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:     func() time.Time { return start },
	}
	t.Cleanup(func() {
		shown := f.log.String() + strings.Join(f.ui.notices, "\n")
		for _, mark := range []string{"-token", "-secret"} {
			if strings.Contains(shown, mark) {
				t.Errorf("a secret was logged or shown:\n%s", shown)
			}
		}
	})
	return f
}

// export puts a session into the environment: the token into the first
// variable of the definition, the refresh token into the one paired with
// it.
func (f *fixture) export(token, refreshToken string) {
	f.env["EXAMPLE_TOKEN"] = token
	f.env["EXAMPLE_REFRESH_TOKEN"] = refreshToken
}

// manager returns a Manager for the fixture as it stands.
func (f *fixture) manager() *auth.Manager {
	f.t.Helper()
	manager, err := auth.New(f.options)
	if err != nil {
		f.t.Fatalf("New: %v", err)
	}
	return manager
}

// call is a request the fake API received.
type call struct {
	Route  string // the method and the path
	URL    string
	Header string // the value of the header that carries the token
	Body   string
}

// reply is what the fake API answers with.
type reply struct {
	status int
	body   string
	err    error
}

// fakeAPI is a Doer that answers the requests of the definition and keeps
// what it received.
type fakeAPI struct {
	t  *testing.T
	mu sync.Mutex
	// verdicts are the statuses the validation answers with, by token.
	// Other tokens get status 401.
	verdicts map[string]int
	// sessions are the bodies the refresh answers with, by refresh token.
	// Other refresh tokens get status 400.
	sessions map[string]string
	// routes answer requests in place of the above.
	routes map[string]func(call) reply
	calls  []call
}

// valid makes the validation accept the tokens.
func (a *fakeAPI) valid(tokens ...string) {
	for _, token := range tokens {
		a.verdicts[token] = http.StatusNoContent
	}
}

// on sets what answers the requests of a route.
func (a *fakeAPI) on(route string, answer func(call) reply) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routes[route] = answer
}

// Do implements auth.Doer.
func (a *fakeAPI) Do(req *http.Request) (*http.Response, error) {
	received := call{Route: req.Method + " " + req.URL.Path, URL: req.URL.String(), Header: req.Header.Get("X-Token")}
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		received.Body = string(body)
	}
	a.mu.Lock()
	a.calls = append(a.calls, received)
	answer := a.routes[received.Route]
	a.mu.Unlock()

	var answered reply
	switch {
	case answer != nil:
		answered = answer(received)
	case received.Route == checkRoute:
		answered.status = http.StatusUnauthorized
		if status, ok := a.verdicts[received.Header]; ok {
			answered.status = status
		}
	case received.Route == refreshRoute:
		var sent struct{ RefreshToken string }
		if err := json.Unmarshal([]byte(received.Body), &sent); err != nil {
			a.t.Errorf("the refresh request has the body %q: %v", received.Body, err)
		}
		answered = reply{status: http.StatusBadRequest, body: `{"message": "unknown session"}`}
		if session, ok := a.sessions[sent.RefreshToken]; ok {
			answered = reply{status: http.StatusOK, body: session}
		}
	default:
		a.t.Errorf("unexpected request: %s", received.Route)
		answered.status = http.StatusNotFound
	}
	if answered.err != nil {
		return nil, answered.err
	}
	return &http.Response{
		StatusCode: answered.status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(answered.body)),
		Request:    req,
	}, nil
}

// sent returns the requests of a route, in the order they arrived.
func (a *fakeAPI) sent(route string) []call {
	a.mu.Lock()
	defer a.mu.Unlock()
	var calls []call
	for _, received := range a.calls {
		if received.Route == route {
			calls = append(calls, received)
		}
	}
	return calls
}

// tokens returns the tokens that the requests of a route carried.
func (a *fakeAPI) tokens(route string) []string {
	var tokens []string
	for _, received := range a.sent(route) {
		tokens = append(tokens, received.Header)
	}
	return tokens
}

// memoryStore is a Store in memory.
type memoryStore struct {
	mu     sync.Mutex
	values map[string]string
	// broken is returned by every method when it is set.
	broken error
	reads  int
}

func (s *memoryStore) Get(account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.broken != nil {
		return "", s.broken
	}
	value, ok := s.values[account]
	if !ok {
		return "", fmt.Errorf("account %s: %w", account, auth.ErrNotFound)
	}
	return value, nil
}

func (s *memoryStore) Set(account, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken != nil {
		return s.broken
	}
	s.values[account] = value
	return nil
}

func (s *memoryStore) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken != nil {
		return s.broken
	}
	if _, ok := s.values[account]; !ok {
		return auth.ErrNotFound
	}
	delete(s.values, account)
	return nil
}

// put stores a value as another process would.
func (s *memoryStore) put(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[account] = value
}

// stored returns the value of the account of the tests.
func (s *memoryStore) stored() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[account]
}

// fakeUI is a user: it answers prompts by their label and keeps what it
// was shown.
type fakeUI struct {
	t           *testing.T
	interactive bool
	answers     map[string]string
	// abort is what Ask returns when it is set: the user gave no answer.
	abort error
	// browse stands in for the browser.
	browse  func(ctx context.Context, url string) error
	asked   []definition.Prompt
	notices []string
	opened  []string
}

func (u *fakeUI) Interactive() bool { return u.interactive }

func (u *fakeUI) Ask(_ context.Context, p definition.Prompt) (string, error) {
	u.asked = append(u.asked, p)
	if u.abort != nil {
		return "", u.abort
	}
	answer, ok := u.answers[p.Label]
	if !ok {
		u.t.Errorf("unexpected prompt %q", p.Label)
		return "", errors.New("no answer")
	}
	return answer, nil
}

func (u *fakeUI) OpenBrowser(ctx context.Context, url string) error {
	u.opened = append(u.opened, url)
	if u.browse == nil {
		return nil
	}
	return u.browse(ctx, url)
}

func (u *fakeUI) Notify(text string) { u.notices = append(u.notices, text) }

// record returns a session as it is stored.
func record(token, refreshToken, expiresAt string) string {
	fields := []string{fmt.Sprintf("%q:%q", "token", token)}
	if refreshToken != "" {
		fields = append(fields, fmt.Sprintf("%q:%q", "refreshToken", refreshToken))
	}
	if expiresAt != "" {
		fields = append(fields, fmt.Sprintf("%q:%q", "expiresAt", expiresAt))
	}
	return "{" + strings.Join(fields, ",") + "}"
}

// wantUnauthenticated checks that err means "no usable credential" and
// that it is, or is not, each of the errors of the package.
func wantUnauthenticated(t *testing.T, err error, noToken, inconclusive, stale bool) {
	t.Helper()
	if err == nil {
		t.Fatal("no error, want one of kind unauthenticated")
	}
	if kind := apikit.KindOf(err); kind != apikit.KindUnauthenticated {
		t.Errorf("KindOf(%v) = %v, want %v", err, kind, apikit.KindUnauthenticated)
	}
	if got := errors.Is(err, auth.ErrNoToken); got != noToken {
		t.Errorf("errors.Is(%v, ErrNoToken) = %v, want %v", err, got, noToken)
	}
	if got := errors.Is(err, auth.ErrInconclusive); got != inconclusive {
		t.Errorf("errors.Is(%v, ErrInconclusive) = %v, want %v", err, got, inconclusive)
	}
	failure, got := errors.AsType[*auth.StaleError](err)
	if got != stale {
		t.Errorf("%v is a *StaleError: %v, want %v", err, got, stale)
	}
	if got && failure.Account != account {
		t.Errorf("StaleError.Account = %q, want %q", failure.Account, account)
	}
	for _, mark := range []string{"-token", "-secret"} {
		if strings.Contains(err.Error(), mark) {
			t.Errorf("the error holds a secret: %v", err)
		}
	}
}
