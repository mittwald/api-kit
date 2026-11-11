package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/mittwald/cli-kit/secret"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// session is what a command works with: the definition, and the engine and
// the credentials for the options of this run.
type session struct {
	doc         *definition.Document
	environment string
	engine      *engine.Engine
	auth        *auth.Manager
}

// session returns the engine and the credentials for the options of this
// run. They are made once: a run has one cache of answers and one token.
func (a *App) session(doc *definition.Document) (*session, error) {
	if a.current != nil {
		return a.current, nil
	}
	g := a.globals
	environment := g.env
	if environment == "" && len(doc.Environments) > 0 {
		environment = doc.Environments[0].Name
	}
	if _, ok := environmentOf(doc, environment); !ok {
		return nil, apikit.Errorf(apikit.KindUsage, "unknown environment %q, the definition has: %s", environment, strings.Join(environmentNames(doc), ", "))
	}

	logger := slog.New(slog.DiscardHandler)
	if g.verbose {
		logger = slog.New(&lineHandler{w: a.opts.Streams.Err, mu: &sync.Mutex{}})
		writeLine(a.opts.Streams.Err, "Version: "+a.opts.Build.String())
	} else if !a.completing {
		// The log and the status line would get into each other's way.
		a.status = newStatus(a.opts.Streams.Err)
		a.out.before = a.status.clear
	}

	store, err := a.store()
	if err != nil {
		return nil, err
	}
	// The requests of the engine and those that check and renew the token
	// go through one client, so that timeouts and --insecure hold for both.
	client := a.opts.HTTP
	if client == nil {
		client = engine.NewClient(doc.HTTP, g.insecure)
	}
	manager, err := auth.New(auth.Options{
		Config: doc.Auth,
		BaseURL: func(api string) (string, error) {
			url, ok := doc.API(environment, api)
			if !ok {
				return "", apikit.Errorf(apikit.KindDefinition, "environment %q has no API %q", environment, api)
			}
			return url, nil
		},
		HTTP:  client,
		Store: store,
		// One session per environment: a token of one is no use in another.
		Account:  environment,
		Getenv:   a.opts.Getenv,
		Explicit: g.token,
		UI:       a.ui(),
		AppName:  a.opts.Name,
		Log:      logger,
	})
	if err != nil {
		return nil, failed(err)
	}
	built, err := engine.New(engine.Options{
		Definition:  doc,
		Environment: environment,
		ForceAPI:    g.api,
		HTTP:        client,
		Tokens:      manager,
		Authorize:   manager.Authorize,
		Log:         logger,
		Progress:    a.status.report,
		Stdin:       a.opts.Streams.In,
		Templates:   a.opts.Templates,
	})
	if err != nil {
		return nil, failed(err)
	}
	a.current = &session{doc: doc, environment: environment, engine: built, auth: manager}
	return a.current, nil
}

// ui returns who asks the user for credentials: what the host passed in, or
// the terminal.
func (a *App) ui() auth.UI {
	if a.completing {
		// A completion must not ask for a password.
		return nil
	}
	if a.opts.UI != nil {
		return a.opts.UI
	}
	return terminal{in: a.opts.Streams.In, out: a.opts.Streams.Out, err: a.opts.Streams.Err}
}

// store returns where sessions are kept between runs: what the host passed
// in, or the keyring of the operating system with files as a fallback.
func (a *App) store() (auth.Store, error) {
	if a.opts.Store != nil {
		return a.opts.Store, nil
	}
	files, err := secret.NewFileStore(a.opts.Name)
	if err != nil {
		return nil, apikit.Errorf(apikit.KindFailed, "cannot set up the session store: %w", err)
	}
	return secretStore{store: secret.Fallback(secret.Keyring{Service: a.opts.Name}, files)}, nil
}

// secretStore makes a store of cli-kit one that package auth understands:
// the two differ in the error that says nothing is stored.
type secretStore struct {
	store secret.Store
}

func (s secretStore) Get(account string) (string, error) {
	value, err := s.store.Get(account)
	if errors.Is(err, secret.ErrNotFound) {
		return "", auth.ErrNotFound
	}
	return value, err
}

func (s secretStore) Set(account, value string) error {
	return s.store.Set(account, value)
}

func (s secretStore) Delete(account string) error {
	err := s.store.Delete(account)
	if errors.Is(err, secret.ErrNotFound) {
		return auth.ErrNotFound
	}
	return err
}

func environmentOf(doc *definition.Document, name string) (definition.Environment, bool) {
	for _, environment := range doc.Environments {
		if environment.Name == name {
			return environment, true
		}
	}
	return definition.Environment{}, false
}

func environmentNames(doc *definition.Document) []string {
	names := make([]string, len(doc.Environments))
	for i, environment := range doc.Environments {
		names[i] = environment.Name
	}
	return names
}

// lineHandler writes log records as plain lines: the message, then the
// attributes as key=value. It is what --verbose shows.
type lineHandler struct {
	w  io.Writer
	mu *sync.Mutex
}

func (h *lineHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *lineHandler) Handle(_ context.Context, record slog.Record) error {
	var line strings.Builder
	line.WriteString(record.Message)
	record.Attrs(func(attr slog.Attr) bool {
		line.WriteString(" " + attr.Key + "=" + attr.Value.String())
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	writeLine(h.w, line.String())
	return nil
}

func (h *lineHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *lineHandler) WithGroup(string) slog.Handler { return h }
