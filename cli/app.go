package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mittwald/cli-kit/buildinfo"
	"github.com/mittwald/cli-kit/cobrax"
	"github.com/mittwald/cli-kit/live"
	"github.com/mittwald/cli-kit/term"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/cli/argv"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// Streams are the standard streams a tool reads from and writes to.
type Streams struct {
	// In is read for prompts and for values given as "-". Nil means
	// os.Stdin.
	In io.Reader
	// Out receives results. Nil means os.Stdout.
	Out io.Writer
	// Err receives errors, warnings and notices. Nil means os.Stderr.
	Err io.Writer
}

// Overlay is a file that extends the definition of a tool.
type Overlay struct {
	// Path is the file, a definition document in YAML.
	Path string
	// Optional lets the file be absent.
	Optional bool
}

// Options describe the tool to build.
type Options struct {
	// Name is the program name shown in help and messages. Empty means the
	// base name of os.Args[0], without a ".exe" suffix.
	Name string
	// Short is the one-line description shown in help.
	Short string
	// Long is the longer description shown in the root command's help.
	Long string
	// Build is the build information behind --version and the version
	// command.
	Build buildinfo.Info
	// Streams are the standard streams; the zero value uses those of the
	// process.
	Streams Streams
	// Definition describes the API the tool is for. Without one the tool
	// has no commands but version and completion.
	Definition definition.Source
	// Templates holds the call templates the tool ships, as YAML files at
	// its root. Nil means it ships none.
	Templates fs.FS
	// Overlays are files a user may extend the definition with. They are
	// merged in order; by default they can add to the definition only, and
	// --config-override lets them replace values.
	Overlays []Overlay
	// EnvPrefix starts the names of the environment variables that tune a
	// run, as in <prefix>_HTTP_TIMEOUT. Empty means there are none.
	EnvPrefix string
	// Store keeps sessions between runs. Nil means the keyring of the
	// operating system, with files in the user's configuration directory
	// where there is none.
	Store auth.Store
	// HTTP sends the requests. Nil means a client made for the definition;
	// tests pass a fake.
	HTTP engine.Doer
	// Getenv reads the environment. Nil means os.Getenv.
	Getenv func(string) string
	// UI asks the user for credentials and opens the browser for a login.
	// Nil means the terminal the tool runs in.
	UI auth.UI
	// Commands are commands of the host, added next to the generated ones.
	Commands []CommandFactory
	// FlagAliases are older spellings of options every command has. They
	// keep working, are not shown in help, and tell the user about the
	// spelling to use instead.
	FlagAliases []FlagAlias
}

// FlagAlias is an older spelling of an option every command has: a switch
// that stands for an option with a value, as --no-colors stands for
// --color=never.
type FlagAlias struct {
	// Old is the name of the switch, without dashes.
	Old string
	// New is the name of the option it stands for.
	New string
	// Value is the value it gives that option.
	Value string
	// Silent keeps the user from being told about the replacement, for a
	// spelling that is as good as the new one.
	Silent bool
}

// App is a tool built from [Options].
type App struct {
	opts Options
	// globals are the values of the options every command has, filled when
	// a command line is parsed.
	globals *globals
	// out wraps the output stream to know whether anything was printed.
	out *tracker
	// hasLogin says whether the definition describes a way to log in.
	hasLogin bool
	// current is the session of this run, made when a command first needs
	// it.
	current *session
	// told holds what the user was told is deprecated, so that each thing
	// is said once.
	told map[string]bool
	// status shows on a terminal what a long run is busy with; nil when
	// there is no terminal for it.
	status *status
	// completing is true while the tool answers a completion request of the
	// shell. Nobody is asked anything then.
	completing bool
	// pending is the value of several words that the word being completed
	// belongs to, if any.
	pending *argv.Pending
}

// New returns the tool described by o. It fills in defaults and does no
// input or output.
func New(o Options) *App {
	if o.Name == "" {
		o.Name = strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	}
	if o.Streams.In == nil {
		o.Streams.In = os.Stdin
	}
	if o.Streams.Out == nil {
		o.Streams.Out = os.Stdout
	}
	if o.Streams.Err == nil {
		o.Streams.Err = os.Stderr
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	return &App{opts: o}
}

// Run builds the tool described by o, runs it with args (the command line
// without the program name) and returns the exit code. An interrupt signal
// cancels the run; a second one ends the process at once.
func Run(ctx context.Context, o Options, args []string) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	// After the first interrupt the signal gets its default meaning back,
	// so a run that does not stop on cancellation can still be killed.
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := New(o).Execute(ctx, args)
	live.RestoreAll()
	return code
}

// Build loads the definition and returns the root command of the tool with
// all subcommands. args is the command line the tool is about to run with;
// it is only searched for --config-override, which changes how the
// definition is loaded and so has to be known before the commands exist.
func (a *App) Build(args []string) (*cobra.Command, error) {
	a.globals = &globals{}
	a.out = &tracker{w: a.opts.Streams.Out}
	a.current, a.told = nil, map[string]bool{}

	root := &cobra.Command{
		Use:           a.opts.Name,
		Short:         a.opts.Short,
		Long:          a.opts.Long,
		SilenceErrors: true,
		SilenceUsage:  true,
		// Without a command there is nothing to do: show the help and fail,
		// so a script that lost its arguments does not pass silently.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return errSilent{code: apikit.ExitFailed}
		},
	}
	root.SetIn(a.opts.Streams.In)
	root.SetOut(a.out)
	root.SetErr(a.opts.Streams.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return apikit.Errorf(apikit.KindUsage, "%w", err)
	})

	a.globals.register(root)
	a.aliases(root)
	cobrax.SetVersion(root, a.opts.Build)
	root.AddCommand(cobrax.NewVersionCommand(a.opts.Build))

	// The commands of the host come first: their names are taken when the
	// definition is checked.
	env := &Env{app: a}
	for _, factory := range a.opts.Commands {
		root.AddCommand(factory(env))
	}
	if len(a.opts.Definition.Data) > 0 {
		doc, err := a.load(root, configOverrideRequested(args))
		if err != nil {
			return nil, err
		}
		env.doc = doc
		a.hasLogin = len(doc.Auth.Login.Methods) > 0
		root.AddCommand(a.getCommand(doc), a.authCommand(doc), a.configCommand(doc), a.applyCommand(doc))
		root.AddCommand(a.callCommands(doc)...)
	}

	cobrax.NoFileCompletions(root)
	cobrax.NoFileArgs(root)
	cobrax.HardenCompletion(root)
	return root, nil
}

// Execute runs the tool with args (the command line without the program
// name), prints any error to the error stream and returns the exit code.
func (a *App) Execute(ctx context.Context, args []string) int {
	root, err := a.Build(args)
	if err != nil {
		return a.report(nil, err)
	}
	// Spellings cobra does not know are rewritten into ones it parses.
	rewritten := argv.Rewrite(root, args)
	for _, notice := range rewritten.Notices {
		if notice.Kind == argv.PairedValue {
			a.deprecated(strings.Join(notice.Was, " "), strings.Join(notice.Now, " "))
		}
	}
	a.pending = rewritten.Pending
	a.completing = len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd)
	root.SetArgs(rewritten.Args)
	cmd, err := root.ExecuteContextC(ctx)
	a.status.clear()
	code := a.report(cmd, err)
	if code == apikit.ExitOK && a.globals.failEmpty && !a.out.printed {
		return apikit.ExitSkipped
	}
	return code
}

// report prints err the way its kind asks for and returns the exit code.
// cmd is the command the error came from, nil when none ran.
func (a *App) report(cmd *cobra.Command, err error) int {
	if err == nil {
		return apikit.ExitOK
	}
	if silent, ok := errors.AsType[errSilent](err); ok {
		return silent.code
	}
	w := a.opts.Streams.Err
	kind := kindOf(err)
	switch kind {
	case apikit.KindSkipped:
		// Not an error: the caller asked for nothing to happen in this case.
		writeLine(w, err.Error())
	case apikit.KindUsage:
		writeLine(w, "ERROR: "+err.Error())
		if cmd != nil {
			writeLine(w, "Run '"+cmd.CommandPath()+" --help' for usage.")
		}
	case apikit.KindCanceled:
		// The user interrupted the run and knows it.
	default:
		writeLine(w, "ERROR: "+err.Error())
		if hint := a.hint(err, a.hasLogin); hint != "" {
			writeLine(w, hint)
		}
	}
	return kind.ExitCode()
}

// kindOf classifies an error that came out of the command tree. The commands
// of this package return errors that carry a kind. Anything else was raised
// by cobra itself while it parsed the command line (an unknown command, a
// wrong number of arguments, flags that may not be combined) and is therefore
// a usage error.
func kindOf(err error) apikit.Kind {
	if _, ok := errors.AsType[*apikit.Error](err); ok {
		return apikit.KindOf(err)
	}
	if errors.Is(err, context.Canceled) {
		return apikit.KindCanceled
	}
	if _, ok := errors.AsType[*cobrax.ScriptError](err); ok {
		return apikit.KindFailed
	}
	return apikit.KindUsage
}

// failed gives an error that came out of a command's own work a kind, so
// that it is not taken for a mistake on the command line. Errors that have
// a kind already, and a canceled run, pass through.
func failed(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*apikit.Error](err); ok || errors.Is(err, context.Canceled) {
		return err
	}
	return apikit.Errorf(apikit.KindFailed, "%w", err)
}

// errSilent ends the run with an exit code and prints nothing, because the
// command has already said what there is to say.
type errSilent struct {
	code int
}

func (e errSilent) Error() string { return "exit" }

// writeLine writes text and a newline. A failed write to the error stream
// has nowhere to be reported, so it is ignored.
// writeLine writes a line of what the tool has to say: an error, a warning,
// a hint, a line of the log. Such a line is for a person, wherever the
// stream leads, and much of it is not the tool's own words: the message of
// an error is often what the API answered. Its escape sequences and control
// characters are left out, so that nothing in it can clear the screen, write
// to the clipboard or pass a word off as the tool's own, also once a log it
// was kept in is shown.
func writeLine(w io.Writer, text string) {
	_, _ = io.WriteString(w, term.Plain(text)+"\n")
}

// tracker passes writes through and remembers whether anything but white
// space was written, for --fail-empty.
type tracker struct {
	w       io.Writer
	printed bool
	// before is called ahead of every write, to take a status line off the
	// terminal that the output would otherwise run into.
	before func()
}

// Unwrap returns the stream behind the tracker, so that code that asks
// whether the output is a terminal sees the real one.
func (t *tracker) Unwrap() io.Writer { return t.w }

func (t *tracker) Write(p []byte) (int, error) {
	if t.before != nil {
		t.before()
	}
	if !t.printed && strings.TrimSpace(string(p)) != "" {
		t.printed = true
	}
	return t.w.Write(p)
}
