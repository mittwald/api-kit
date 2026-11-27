package cli

import (
	"context"
	"log/slog"
	"strings"

	"github.com/mittwald/cli-kit/logx"
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
		// Every record, also those at the debug level, as one plain line
		// with its credentials redacted.
		logger = slog.New(logx.NewLineHandler(a.errs, &logx.Options{Level: slog.LevelDebug}))
		writeLine(a.errs, "Version: "+a.opts.Build.String())
	} else if !a.completing {
		// The log and the status line would get into each other's way. The
		// line is drawn to the stream itself: what the tool writes goes
		// through a.out and a.errs, which take the line off first.
		a.status = newStatus(a.opts.Streams.Err)
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
		Formats:     a.opts.Formats,
	})
	if err != nil {
		return nil, failed(err)
	}
	a.current = &session{doc: doc, environment: environment, engine: built, auth: manager}
	return a.current, nil
}

// ui returns who asks the user for credentials: what the host passed in, or
// the terminal, with the status line kept off the terminal meanwhile.
func (a *App) ui() auth.UI {
	if a.completing {
		// A completion must not ask for a password.
		return nil
	}
	var ui auth.UI = terminal{in: a.opts.Streams.In, out: a.opts.Streams.Out, err: a.opts.Streams.Err, lines: &pipedLines{}}
	if a.opts.UI != nil {
		ui = a.opts.UI
	}
	if a.status == nil {
		return ui
	}
	return heldUI{UI: ui, status: a.status}
}

// heldUI is the UI of a run with a status line: it takes the line off the
// terminal and keeps it off while the user is told or asked something, so
// that the line is not drawn into the middle of a prompt or over a notice.
type heldUI struct {
	auth.UI
	status *status
}

// Ask asks with the status line kept off the terminal.
func (u heldUI) Ask(ctx context.Context, p definition.Prompt) (string, error) {
	defer u.status.pause()()
	return u.UI.Ask(ctx, p)
}

// Notify tells the user something with the status line kept off the
// terminal.
func (u heldUI) Notify(text string) {
	defer u.status.pause()()
	u.UI.Notify(text)
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
	// auth.ErrNotFound is secret.ErrNotFound: a store of cli-kit needs no
	// adapter.
	return secret.Fallback(secret.Keyring{Service: a.opts.Name}, files), nil
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
