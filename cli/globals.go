package cli

import (
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mittwald/cli-kit/cobrax"
	"github.com/mittwald/cli-kit/term"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/cli/argv"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/output"
)

// Names of the options every command has.
const (
	flagVerbose        = "verbose"
	flagOutput         = "output"
	flagInsecure       = "insecure"
	flagNoEllipsis     = "no-ellipsis"
	flagNoJoins        = "no-joins"
	flagLinks          = "links"
	flagFailEmpty      = "fail-empty"
	flagConfigOverride = "config-override"
	flagToken          = "token"
	flagAPI            = "api"
	flagEnv            = "env"
	flagColor          = "color"
)

// globals are the values of the options every command has.
type globals struct {
	verbose        bool
	output         string
	insecure       bool
	noEllipsis     bool
	noJoins        bool
	links          string
	failEmpty      bool
	configOverride bool
	token          string
	api            string
	env            string
}

// register adds the options every command has to the root command.
func (g *globals) register(root *cobra.Command) {
	flags := root.PersistentFlags()
	flags.BoolVar(&g.verbose, flagVerbose, false, "log the requests that are sent")
	flags.StringVarP(&g.output, flagOutput, "o", string(output.Text),
		"output format: "+strings.Join(output.Formats(), ", ")+"; jsonpath and columns take an argument, as in jsonpath='$.name' or columns=id,name")
	if err := root.RegisterFlagCompletionFunc(flagOutput, completeOutput); err != nil {
		panic(err) // the flag was registered above
	}
	// "-o jsonpath EXPR" is the older spelling of "-o jsonpath=EXPR".
	flags.Lookup(flagOutput).Annotations = map[string][]string{
		argv.AnnotationWords: {argv.WordsPair + string(output.JSONPath) + "," + string(output.Columns)},
	}
	flags.BoolVar(&g.insecure, flagInsecure, false, "do not verify the certificate of the API")
	flags.BoolVar(&g.noEllipsis, flagNoEllipsis, false, "do not shorten long values in tables")
	flags.BoolVar(&g.noJoins, flagNoJoins, false, "do not fetch the values of related resources")
	flags.StringVar(&g.links, flagLinks, term.ColorAuto.String(), "when to make the first column of a table a link: auto, always or never")
	flags.Lookup(flagLinks).NoOptDefVal = term.ColorAlways.String()
	if err := root.RegisterFlagCompletionFunc(flagLinks, cobrax.CompleteValues(term.ColorModeNames...)); err != nil {
		panic(err) // the flag was registered two lines up
	}
	flags.BoolVar(&g.failEmpty, flagFailEmpty, false, "exit with code 3 when nothing is printed")
	flags.BoolVar(&g.configOverride, flagConfigOverride, false, "let the user's definition file replace values instead of only adding to them")
	flags.StringVar(&g.token, flagToken, "", "token to authenticate with, instead of the stored session")
	flags.StringVar(&g.api, flagAPI, "", "API to send requests to, instead of the one the resource names")
	flags.StringVar(&g.env, flagEnv, "", "environment of the API to use")
	cobrax.AddColorFlag(root, "when to use color: auto, always or never")
}

// aliases adds the older spellings of options every command has. Each is a
// hidden switch that sets the option it stands for.
func (a *App) aliases(root *cobra.Command) {
	flags := root.PersistentFlags()
	for _, alias := range a.opts.FlagAliases {
		flags.BoolFunc(alias.Old, "same as --"+alias.New+"="+alias.Value, func(string) error {
			if !alias.Silent {
				a.deprecated("--"+alias.Old, "--"+alias.New+"="+alias.Value)
			}
			return flags.Set(alias.New, alias.Value)
		})
		flags.Lookup(alias.Old).Hidden = true
	}
}

// linked reports whether tables written to w carry links: always or never
// when the user says so, otherwise when w is a terminal, where a link can
// be clicked.
func (g *globals) linked(w io.Writer) (bool, error) {
	mode, err := term.ParseColorMode(g.links)
	if err != nil {
		return false, apikit.Errorf(apikit.KindUsage, "invalid value %q for --%s: use auto, always or never", g.links, flagLinks)
	}
	switch mode {
	case term.ColorAlways:
		return true, nil
	case term.ColorNever:
		return false, nil
	}
	return term.IsTerminal(w), nil
}

// spec returns the parsed --output value.
func (g *globals) spec() (output.Spec, error) {
	return output.Parse(g.output)
}

// reserved returns the names a definition cannot take for its calls and
// parameters, because the command line uses them itself.
func reserved(root *cobra.Command) definition.Reserved {
	names := definition.Reserved{
		// Commands that are added after the definition is loaded.
		Commands:               []string{"get", "config", "apply", "auth", "completion", "help"},
		CallOptions:            []string{"if", "if-none", "if-any"},
		TemplateCallOptions:    []string{"template", "template-var", "template-var-file", "dry-run"},
		TemplateOptionSuffixes: []string{"-template", "-template-file"},
		GlobalOptions:          []string{"help", "version"},
	}
	for _, command := range root.Commands() {
		names.Commands = append(names.Commands, command.Name())
	}
	root.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		names.GlobalOptions = append(names.GlobalOptions, flag.Name)
	})
	return names
}

// configOverrideRequested reports whether the command line asks for
// --config-override. The definition decides which commands exist, so this
// one option is looked for before the command line is parsed. Words after
// "--" are arguments, not options.
func configOverrideRequested(args []string) bool {
	requested := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if name != "--"+flagConfigOverride {
			continue
		}
		requested = true
		if hasValue {
			if parsed, err := strconv.ParseBool(value); err == nil {
				requested = parsed
			}
		}
	}
	return requested
}
