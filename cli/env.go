package cli

import (
	"github.com/spf13/cobra"

	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// CommandFactory builds a command a host adds to its tool, next to the
// ones this package generates. It is called once, before the definition is
// loaded, so that the name of the command is known when the definition is
// checked; the [Env] it gets is for use when the command runs.
type CommandFactory func(env *Env) *cobra.Command

// Env gives the commands of a host what the built-in commands work with.
// Its methods are for use while a command runs.
type Env struct {
	app *App
	doc *definition.Document
}

// Definition returns the loaded definition, nil for a tool without one.
func (e *Env) Definition() *definition.Document { return e.doc }

// Streams returns the standard streams of the tool. Results go to the
// output stream of cmd rather than to Streams().Out, which lacks the
// bookkeeping behind --fail-empty.
func (e *Env) Streams() Streams { return e.app.opts.Streams }

// Engine returns an engine for the options of this run: the environment,
// the API and the token the user chose.
func (e *Env) Engine() (*engine.Engine, error) {
	session, err := e.app.session(e.doc)
	if err != nil {
		return nil, err
	}
	return session.engine, nil
}

// Render writes a result to the output stream of cmd in the format the
// user asked for with --output, as the built-in commands do.
func (e *Env) Render(cmd *cobra.Command, result *engine.Result) error {
	spec, err := e.app.globals.spec()
	if err != nil {
		return err
	}
	session, err := e.app.session(e.doc)
	if err != nil {
		return err
	}
	return e.app.render(cmd, session, result, view{spec: spec})
}

// Deprecated tells the user on the error stream that what they used still
// works but has a replacement.
func (e *Env) Deprecated(what, replacement string) {
	e.app.deprecated(what, replacement)
}
