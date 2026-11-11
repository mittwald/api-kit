package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mittwald/cli-kit/buildinfo"
	"github.com/mittwald/cli-kit/cobrax"
	"github.com/mittwald/cli-kit/live"

	apikit "github.com/mittwald/api-kit"
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
}

// App is a tool built from [Options].
type App struct {
	opts Options
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

// Build returns the root command of the tool, with all subcommands.
func (a *App) Build() *cobra.Command {
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
	root.SetOut(a.opts.Streams.Out)
	root.SetErr(a.opts.Streams.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return apikit.Errorf(apikit.KindUsage, "%w", err)
	})

	cobrax.AddColorFlag(root, "when to use color: auto, always or never")
	cobrax.SetVersion(root, a.opts.Build)
	root.AddCommand(cobrax.NewVersionCommand(a.opts.Build))

	cobrax.NoFileCompletions(root)
	cobrax.NoFileArgs(root)
	cobrax.HardenCompletion(root)
	return root
}

// Execute runs the tool with args (the command line without the program
// name), prints any error to the error stream and returns the exit code.
func (a *App) Execute(ctx context.Context, args []string) int {
	root := a.Build()
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	return a.report(cmd, err)
}

// report prints err the way its kind asks for and returns the exit code.
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
		writeLine(w, "Run '"+cmd.CommandPath()+" --help' for usage.")
	case apikit.KindCanceled:
		// The user interrupted the run and knows it.
	default:
		writeLine(w, "ERROR: "+err.Error())
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

// errSilent ends the run with an exit code and prints nothing, because the
// command has already said what there is to say.
type errSilent struct {
	code int
}

func (e errSilent) Error() string { return "exit" }

// writeLine writes text and a newline. A failed write to the error stream
// has nowhere to be reported, so it is ignored.
func writeLine(w io.Writer, text string) {
	_, _ = io.WriteString(w, text+"\n")
}
