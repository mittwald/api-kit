package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// Store keeps one value per account between runs, such as the keyring of
// the operating system. A [Manager] keeps one session in it, under
// [Options.Account].
type Store interface {
	// Get returns the value stored for account. For an account the store
	// holds nothing for, it returns an error that wraps [ErrNotFound].
	Get(account string) (string, error)
	// Set stores value for account in place of an earlier one.
	Set(account, value string) error
	// Delete removes the value stored for account. For an account the store
	// holds nothing for, it returns nil or an error that wraps
	// [ErrNotFound].
	Delete(account string) error
}

// ErrNotFound is what a [Store] returns for an account it holds nothing
// for. It is how a Manager tells a session that is absent from a store that
// is broken: an error that wraps ErrNotFound, or an empty value, means that
// nothing is stored. Any other error means that the store does not work; a
// run then goes on without a stored session, the error is logged as a
// warning, and [Manager.Logout] returns it.
//
// A Store of another library reports an absent account with an error of its
// own, which a Manager takes for a broken store. Wrap such a Store, so that
// Get and Delete return ErrNotFound in place of that error.
var ErrNotFound = errors.New("auth: nothing is stored for the account")

// Doer sends an HTTP request. An [*http.Client] is one.
type Doer interface {
	// Do sends the request and returns the response.
	Do(*http.Request) (*http.Response, error)
}

// UI is how a Manager reaches the user. The command layer implements it.
// Its methods are called while the Manager is busy, one at a time; they
// must not call the Manager.
type UI interface {
	// Interactive reports whether there is a user to ask: the run is
	// attached to a terminal.
	Interactive() bool
	// Ask shows the hint of the prompt, if it has one, asks its question
	// and returns the answer. The answer is not echoed when the prompt is
	// secret.
	Ask(ctx context.Context, p definition.Prompt) (string, error)
	// OpenBrowser opens the URL in the browser of the user and returns
	// without waiting for what happens there.
	OpenBrowser(ctx context.Context, url string) error
	// Notify shows a notice or a warning: one or more complete sentences.
	Notify(text string)
}

// Options configure a [Manager].
type Options struct {
	// Config is the auth section of the definition, as [definition.Load]
	// returns it in [definition.Document.Auth].
	Config definition.Auth
	// BaseURL returns the base URL of an API in the environment of the run.
	// It is required.
	BaseURL func(api string) (string, error)
	// HTTP sends the requests that check and renew tokens and that log in.
	// Nil means [http.DefaultClient], which never times out.
	HTTP Doer
	// Store keeps the session between runs. Nil means that nothing is
	// remembered, as does a definition that does not list the store among
	// its sources.
	Store Store
	// Account is the key of the session in Store. Sessions of different
	// environments need different accounts. It is required with a Store.
	Account string
	// Getenv returns the value of an environment variable. Nil means
	// [os.Getenv].
	Getenv func(string) string
	// Explicit is a token given for this run alone, on the command line for
	// example. With it no other source is read, no session is renewed and
	// nobody is asked to log in.
	Explicit string
	// UI reaches the user. Nil means that the run is never interactive.
	UI UI
	// AppName names the tool on the page a browser login ends on.
	AppName string
	// Log receives what a verbose run shows, and warnings about a store or
	// an API that does not answer. It never receives a token. Nil discards
	// everything.
	Log *slog.Logger
	// Now returns the current time. Nil means [time.Now].
	Now func() time.Time
}

// SourceKind says what kind of place a token comes from.
type SourceKind uint8

// The kinds of places a token comes from. The zero SourceKind means that
// there is no token.
const (
	// SourceExplicit is the token given for this run, [Options.Explicit].
	SourceExplicit SourceKind = iota + 1
	// SourceProcess is the token an earlier call of this Manager settled
	// on.
	SourceProcess
	// SourceStore is the session in the [Store].
	SourceStore
	// SourceEnv is an environment variable.
	SourceEnv
	// SourceRefresh is a session that was renewed just now.
	SourceRefresh
	// SourceLogin is a login that happened just now.
	SourceLogin
)

// Source says where a token comes from.
type Source struct {
	// Kind is the kind of place.
	Kind SourceKind
	// Name is the environment variable of [SourceEnv] and the login method
	// of [SourceLogin]. It is empty for the other kinds.
	Name string
}

// String describes the source in a few words, for a log.
func (s Source) String() string {
	switch s.Kind {
	case SourceExplicit:
		return "explicit token"
	case SourceProcess:
		return "token of this process"
	case SourceStore:
		return "stored session"
	case SourceEnv:
		return "environment variable " + s.Name
	case SourceRefresh:
		return "refreshed session"
	case SourceLogin:
		return "login " + s.Name
	}
	return "none"
}

// The errors of a run that has no token to use. Both are of kind
// [apikit.KindUnauthenticated]. Test for them with [errors.Is]: a Manager
// mostly returns them inside an error that says more.
var (
	// ErrNoToken means that no source has a token the API accepts, and
	// that none could be obtained. Where a login would help, the message of
	// the error says that one is needed; it names no command.
	ErrNoToken error = apikit.Errorf(apikit.KindUnauthenticated, "no valid API token is available")
	// ErrInconclusive means that the API gave no verdict on a token: the
	// validation answered with a status that proves nothing, or not at all.
	// Such a token is not used, it is not given up either, and nobody is
	// asked to log in. The run is worth another try later.
	ErrInconclusive error = apikit.Errorf(apikit.KindUnauthenticated,
		"token validation is unavailable, so no token could be proven valid")
)

var (
	// errLoginNeeded is ErrNoToken where a login would help.
	errLoginNeeded error = &apikit.Error{Kind: apikit.KindUnauthenticated, Msg: "a login is needed", Err: ErrNoToken}
	// errExplicit is ErrNoToken for a run with a token of its own.
	errExplicit error = &apikit.Error{Kind: apikit.KindUnauthenticated, Msg: "the token given for this run is not valid", Err: ErrNoToken}
)

// StaleError means that the API rejected the token in the [Store] and that
// its session could not be renewed. The token stays where it is: only a
// login replaces it. A StaleError is an [ErrNoToken].
type StaleError struct {
	// Account is the key of the session in the Store.
	Account string
}

// Error says which stored token is stale.
func (e *StaleError) Error() string {
	return fmt.Sprintf("the stored token for %s was rejected and cannot be refreshed", e.Account)
}

// Unwrap returns [ErrNoToken].
func (e *StaleError) Unwrap() error { return ErrNoToken }

// verdict is what is known about a token.
type verdict uint8

const (
	// verdictNone means that the API was not asked.
	verdictNone verdict = iota
	// verdictValid means that the API accepts the token.
	verdictValid
	// verdictInvalid means that the API rejects the token.
	verdictInvalid
	// verdictUnknown means that the API was asked and gave no verdict.
	verdictUnknown
)

// A Manager supplies the token for the requests of one run. It finds the
// token, renews its session before it expires and after the API rejected
// it, and runs logins. A Manager is safe for concurrent use.
type Manager struct {
	o      Options
	header expr.Template

	// mu is held for the whole of every call that reads or changes what
	// follows, requests and prompts included. A refresh token can be
	// redeemed once, so no two goroutines may rotate a session at a time.
	mu sync.Mutex
	// token is the token this process settled on, "" for none yet.
	token string
	// confirmed reports that the API accepted token.
	confirmed bool
	// rejected holds the tokens the API rejected in this process. Asking
	// about one again could only fail again.
	rejected map[string]bool
}

// New returns a Manager for the options. It sends no request and reads no
// source yet.
func New(o Options) (*Manager, error) {
	switch {
	case o.Config.Header.Name == "":
		return nil, errors.New("auth: Options.Config names no header; take it from a loaded definition")
	case o.BaseURL == nil:
		return nil, errors.New("auth: Options.BaseURL is required")
	case o.Store != nil && o.Account == "":
		return nil, errors.New("auth: Options.Account is required with a Store")
	}
	if !slices.ContainsFunc(o.Config.Sources, func(source definition.AuthSource) bool { return source.Store }) {
		o.Store = nil
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.Explicit = strings.TrimSpace(o.Explicit)
	return &Manager{o: o, header: expr.ParseTemplate(o.Config.Header.Value), rejected: map[string]bool{}}, nil
}

// Token returns the token to send a request with. It answers without
// asking the API: the first source that has a token wins, and the request
// shows whether the token is good. Only a stored session that is about to
// expire is renewed first. When no source has a token, an interactive run
// falls back to a login; any other run fails with an [ErrNoToken].
func (m *Manager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	token, _, err := m.resolve(ctx, false, true)
	return token, err
}

// Rejected is to be called when the API answered a request that carried
// token with status 401. It returns the token to send the request with once
// more, or an error when there is none: an [ErrNoToken], an
// [ErrInconclusive] or a [*StaleError].
//
// The session the token belongs to is renewed first. When that does not
// work, the token is not offered again in this process and the other
// sources are asked, each token checked with the API; an interactive run
// falls back to a login in the end. Callers that were rejected at the same
// time get the token the first of them obtained: a refresh token is
// redeemed once.
func (m *Manager) Rejected(ctx context.Context, token string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Another goroutine was rejected with the same token and has replaced
	// it in the meantime.
	if m.confirmed && m.token != "" && m.token != token {
		return m.token, nil
	}
	// The session is rotated before the token is given up: a token that is
	// no candidate anymore has lost the refresh token that belongs to it.
	fresh := m.rotate(ctx, token)
	m.rejected[token] = true
	if fresh != "" {
		m.token, m.confirmed = fresh, true
		return fresh, nil
	}
	if m.token == token {
		m.token, m.confirmed = "", false
	}
	if m.o.Explicit != "" {
		return "", errExplicit
	}
	fresh, _, err := m.resolve(ctx, true, true)
	if err == nil && fresh == token {
		return "", ErrNoToken
	}
	return fresh, err
}

// Confirmed is to be called when a request that carried token succeeded.
// The token is then used without further checks.
func (m *Manager) Confirmed(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token == m.token {
		m.confirmed = true
	}
}

// Authorize sets the header that carries the token on req.
func (m *Manager) Authorize(req *http.Request, token string) {
	value, _ := m.header.Render(lookup(map[string]string{definition.VariableToken: token}), expr.RenderOptions{})
	req.Header.Set(m.o.Config.Header.Name, value)
}

// Resolve returns a token the API accepts, and where it comes from. Unlike
// [Manager.Token] it asks the API about each candidate, unless the
// definition has no validation, in which case the first candidate is taken
// as it is.
//
// A candidate the API rejects has its session renewed, or else is not
// offered again. A candidate the API gives no verdict on is skipped. When
// nothing is left, the run is interactive, interactive is true and every
// verdict was clear, Resolve falls back to a login. Otherwise it fails with
// an [ErrNoToken], an [ErrInconclusive] or a [*StaleError].
func (m *Manager) Resolve(ctx context.Context, interactive bool) (string, Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resolve(ctx, true, interactive)
}

// Status reports where the token of this run comes from and whether the
// API has accepted it. It resolves the token as [Manager.Resolve] does, and
// never asks the user. validated is false for a token the API could not be
// asked about, because the definition has no validation.
func (m *Manager) Status(ctx context.Context) (source Source, validated bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, source, err = m.resolve(ctx, true, false)
	return source, err == nil && m.confirmed, err
}

// Logout removes the session from the [Store] and forgets the token of
// this process. It reports whether a session was stored.
func (m *Manager) Logout() (existed bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token, m.confirmed = "", false
	if m.o.Store == nil {
		return false, nil
	}
	if value, err := m.o.Store.Get(m.o.Account); err == nil && value != "" {
		existed = true
	}
	if err := m.o.Store.Delete(m.o.Account); err != nil && !errors.Is(err, ErrNotFound) {
		return existed, fmt.Errorf("cannot remove the stored session: %w", err)
	}
	return existed, nil
}

// Methods returns the login methods of the definition, in document order.
func (m *Manager) Methods() []definition.LoginMethod {
	return slices.Clone(m.o.Config.Login.Methods)
}

// candidate is a token that may be tried, and where it comes from.
type candidate struct {
	token  string
	source Source
}

// candidates returns the tokens to try, in order: the one given for the
// run and nothing else, or the one this process settled on, then those of
// the sources of the definition. A token the API rejected is left out, and
// a token that several sources hold is listed for the first of them.
func (m *Manager) candidates(stored session) []candidate {
	if m.o.Explicit != "" {
		return []candidate{{token: m.o.Explicit, source: Source{Kind: SourceExplicit}}}
	}
	var candidates []candidate
	add := func(token string, source Source) {
		token = strings.TrimSpace(token)
		known := slices.ContainsFunc(candidates, func(other candidate) bool { return other.token == token })
		if token != "" && !known && !m.rejected[token] {
			candidates = append(candidates, candidate{token: token, source: source})
		}
	}
	add(m.token, Source{Kind: SourceProcess})
	for _, source := range m.o.Config.Sources {
		if source.Store {
			add(stored.Token, Source{Kind: SourceStore})
		} else {
			add(m.o.Getenv(source.Env), Source{Kind: SourceEnv, Name: source.Env})
		}
	}
	return candidates
}

// resolve finds the token of the run. With check, every candidate is put
// to the API first; without, the first one is taken as it is. interactive
// allows the fallback to a login.
func (m *Manager) resolve(ctx context.Context, check, interactive bool) (string, Source, error) {
	// The token this process settled on is answered from memory: always
	// once the API has accepted it, and until the API has rejected it for
	// a caller that leaves the proof to its request.
	if m.confirmed || (!check && m.token != "" && !m.rejected[m.token]) {
		source := Source{Kind: SourceProcess}
		if m.o.Explicit != "" {
			source.Kind = SourceExplicit
		}
		return m.token, source, nil
	}

	var stored session
	if m.o.Explicit == "" {
		stored = m.load()
		if m.expiring(stored) {
			m.o.Log.Debug("the stored session is about to expire; refreshing it")
			if fresh := m.refresh(ctx, stored.Token, stored.RefreshToken); fresh != "" {
				m.token, m.confirmed = fresh, true
				return fresh, Source{Kind: SourceRefresh}, nil
			}
			// Another process may have renewed the session in the meantime.
			stored = m.load()
		}
	}

	inconclusive := false
	for _, tried := range m.candidates(stored) {
		outcome := verdictNone
		if check {
			m.o.Log.Debug("validating a token", "source", tried.source)
			outcome = m.validate(ctx, tried.token)
		}
		switch outcome {
		case verdictNone, verdictValid:
			m.token, m.confirmed = tried.token, outcome == verdictValid
			return tried.token, tried.source, nil
		case verdictUnknown:
			inconclusive = true
		case verdictInvalid:
			// The session is rotated before the token is given up.
			fresh := m.rotate(ctx, tried.token)
			m.rejected[tried.token] = true
			if fresh != "" {
				m.token, m.confirmed = fresh, true
				return fresh, Source{Kind: SourceRefresh}, nil
			}
		}
	}

	// A verdict the API did not give is no reason to ask for credentials,
	// and a token given for the run is not to be replaced behind its back.
	ask := interactive && !inconclusive && m.o.Explicit == "" && m.o.UI != nil && m.o.UI.Interactive()
	if method, ok := m.method(""); ok && ask {
		m.o.UI.Notify("No valid API token is available. Log in to obtain a new one.")
		token, err := m.login(ctx, method)
		if err != nil {
			return "", Source{}, err
		}
		return token, Source{Kind: SourceLogin, Name: method.Name}, nil
	}
	return "", Source{}, m.failure(ctx, stored, inconclusive)
}

// failure returns the error of a run that found no token to use.
func (m *Manager) failure(ctx context.Context, stored session, inconclusive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	switch {
	case inconclusive:
		err = ErrInconclusive
	case m.o.Explicit != "":
		err = errExplicit
	case len(m.o.Config.Login.Methods) > 0:
		err = errLoginNeeded
	default:
		err = ErrNoToken
	}
	// The stored token is kept: removing it would hide why the tool stopped
	// working. What replaces it is a login, so the error says which token
	// it is about.
	if stored.Token == "" || !m.rejected[stored.Token] {
		return err
	}
	stale := &StaleError{Account: m.o.Account}
	if inconclusive {
		return errors.Join(stale, err)
	}
	return stale
}

// lookup returns the values of template variables in the form a template
// asks for them.
func lookup(variables map[string]string) func(name string) (any, bool) {
	return func(name string) (any, bool) {
		value, ok := variables[name]
		return value, ok
	}
}
