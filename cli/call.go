package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mittwald/cli-kit/term"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/cli/argv"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
	"github.com/mittwald/api-kit/output"
)

// Names of the options every call has, and of those a call gets when one of
// its parameters takes a template.
const (
	flagIf              = "if"
	flagIfNone          = "if-none"
	flagIfAny           = "if-any"
	flagTemplate        = "template"
	flagTemplateVar     = "template-var"
	flagTemplateVarFile = "template-var-file"
	flagDryRun          = "dry-run"
	suffixTemplate      = "-template"
	suffixTemplateFile  = "-template-file"
)

// callCommands returns one command for every call of the definition, with a
// subcommand for each resource that has the call: "rename pets ID".
func (a *App) callCommands(doc *definition.Document) []*cobra.Command {
	var commands []*cobra.Command
	for _, use := range doc.CallNames() {
		command := &cobra.Command{
			Use:   use.Name + " RESOURCE",
			Short: "Run " + use.Name + " on " + strings.Join(use.Resources, ", "),
			Args:  cobra.ArbitraryArgs,
			RunE: func(_ *cobra.Command, args []string) error {
				if len(args) > 0 {
					return apikit.Errorf(apikit.KindUsage, "%s is not available for %s, only for %s", use.Name, args[0], strings.Join(use.Resources, ", "))
				}
				return apikit.Errorf(apikit.KindUsage, "%s needs a resource: %s", use.Name, strings.Join(use.Resources, ", "))
			},
		}
		for _, resource := range use.Resources {
			call, _ := doc.Call(resource, use.Name)
			leaf := a.callLeaf(doc, call, resource, false)
			// A resource may be named in the singular.
			if singular := doc.Resource(resource).Singular; singular != resource {
				leaf.Aliases = []string{singular}
			}
			command.AddCommand(leaf)
		}
		if len(use.Resources) == 1 {
			command.Short = command.Commands()[0].Short
		}
		commands = append(commands, command)
	}
	return commands
}

// applyCommand returns the older way to run a call, "apply RESOURCE [ID]
// CALL". It also takes the aliases of calls and of their parameters, which
// the call commands do not have.
func (a *App) applyCommand(doc *definition.Document) *cobra.Command {
	apply := &cobra.Command{
		Use:    "apply RESOURCE [ID] CALL",
		Short:  "Run a call on a resource",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return apikit.Errorf(apikit.KindUsage, "%s has no calls", args[0])
			}
			return apikit.Errorf(apikit.KindUsage, "apply needs a resource and a call")
		},
	}
	for _, resource := range doc.Resources() {
		calls := doc.Calls(resource.Name)
		if len(calls) == 0 {
			continue
		}
		var names []string
		for _, call := range calls {
			names = append(names, call.Name)
			names = append(names, call.Aliases...)
		}
		group := &cobra.Command{
			Use:   resource.Name + " [ID] CALL",
			Short: "Run a call on " + resource.Name,
			Args:  cobra.ArbitraryArgs,
			// The ID may stand in front of the call, as it always did.
			Annotations: map[string]string{argv.AnnotationIDFirst: "true"},
			RunE: func(_ *cobra.Command, args []string) error {
				if len(args) > 1 {
					return apikit.Errorf(apikit.KindUsage, "%s is not available for %s, only %s", args[1], resource.Name, strings.Join(names, ", "))
				}
				return apikit.Errorf(apikit.KindUsage, "apply %s needs a call: %s", resource.Name, strings.Join(names, ", "))
			},
		}
		if resource.Singular != resource.Name {
			group.Aliases = []string{resource.Singular}
		}
		for _, name := range names {
			call, _ := doc.Call(resource.Name, name)
			group.AddCommand(a.callLeaf(doc, call, name, true))
		}
		apply.AddCommand(group)
	}
	return apply
}

// callLeaf returns the command that runs a call on a resource. use is the
// word the command is called by: the resource below a call command, the
// call below apply. legacy makes it the command below apply, which also
// takes the aliases of parameters and tells the user about the call
// command.
func (a *App) callLeaf(doc *definition.Document, call *definition.Call, use string, legacy bool) *cobra.Command {
	var helps []string
	for _, setter := range call.Setters {
		if setter.Help != "" && !slices.Contains(helps, setter.Help) {
			helps = append(helps, setter.Help)
		}
	}
	command := &cobra.Command{
		Use:   use,
		Short: strings.Join(helps, "; "),
		// Whether an ID belongs here is the engine's to say: its message
		// names what is missing.
		Args: cobra.MaximumNArgs(1),
	}
	if call.RequiresID {
		command.Use += " ID"
	}

	flags := command.Flags()
	templated := call.TemplateParameters()
	options := map[string][]string{} // parameter name -> the options that give it
	for _, parameter := range call.Parameters {
		help := parameter.Help
		if parameter.Required && !call.Shared() {
			help = strings.TrimSpace(help + " (required)")
		}
		names := []string{parameter.Name}
		if legacy {
			names = parameter.OptionNames()
		}
		for i, name := range names {
			addParameterFlag(flags, name, parameter.Type, help)
			if i > 0 {
				flags.Lookup(name).Hidden = true
			}
		}
		options[parameter.Name] = names
		if parameter.Template {
			flags.String(parameter.Name+suffixTemplate, "", parameter.Name+" as a template, with ${...} for values of the call and of --"+flagTemplateVar)
			flags.String(parameter.Name+suffixTemplateFile, "", parameter.Name+" as a template from a file, a URL, or - for standard input")
			command.MarkFlagsMutuallyExclusive(parameter.Name, parameter.Name+suffixTemplate, parameter.Name+suffixTemplateFile)
		}
		if choices := allChoices(parameter); len(choices) > 0 {
			for _, name := range names {
				_ = command.RegisterFlagCompletionFunc(name, completeChoices(call, parameter))
			}
		}
	}
	for _, parameter := range call.StepParameters {
		names := []string{parameter.Name}
		if legacy {
			names = parameter.OptionNames()
		}
		for i, name := range names {
			addParameterFlag(flags, name, parameter.Type, strings.TrimSpace(parameter.Help+" (default: "+apikit.Text(parameter.Default)+")"))
			if i > 0 {
				flags.Lookup(name).Hidden = true
			}
		}
		options[parameter.Name] = names
	}
	// Parameters that exclude each other cannot be given together. Whether
	// one of a group is missing shows only when the call runs, since a
	// template file may give it.
	for _, group := range slices.Concat(call.Exclusive, call.OneOf) {
		if len(group) > 1 {
			command.MarkFlagsMutuallyExclusive(group...)
		}
	}

	flags.StringArray(flagIf, nil, "run the call only when the condition holds, else exit with code 3: a field of the item, or of one a parameter names, as in status!=closed or owner.city=Berlin")
	flags.StringArray(flagIfNone, nil, "run the call only when no item of a listing meets the conditions, else exit with code 3: a resource, then conditions")
	flags.StringArray(flagIfAny, nil, "run the call only when an item of a listing meets the conditions, else exit with code 3: a resource, then conditions")
	for _, name := range []string{flagIfNone, flagIfAny} {
		flags.Lookup(name).Annotations = map[string][]string{argv.AnnotationWords: {argv.WordsJoin}}
	}
	if len(templated) > 0 {
		flags.String(flagTemplate, "", "file, URL or built-in template with parameters of the call; what is given here wins")
		flags.StringArray(flagTemplateVar, nil, "value of ${NAME} in the templates of this call, as NAME=VALUE")
		flags.StringArray(flagTemplateVarFile, nil, "like --"+flagTemplateVar+", with the value read from a file, a URL, or - for standard input, as NAME=PATH")
		flags.Bool(flagDryRun, false, "check the call and print its parameters, send nothing")
	}

	a.completeCall(doc, command, call, options)

	command.RunE = func(cmd *cobra.Command, args []string) error {
		spec, err := a.globals.spec()
		if err != nil {
			return err
		}
		if legacy {
			replacement := call.Setters[0].Name + " " + call.Resource
			if call.RequiresID {
				replacement += " ID"
			}
			a.deprecated("apply", replacement)
		}
		session, err := a.session(doc)
		if err != nil {
			return err
		}
		input := engine.CallInput{Resource: call.Resource, Call: call.Name, Params: map[string]any{}, Templates: map[string]engine.Text{}}
		if len(args) > 0 {
			input.ID = args[0]
		}
		if err := a.fillCall(cmd, session, call, options, &input); err != nil {
			return err
		}
		result, err := session.engine.Apply(cmd.Context(), input)
		// What was done before a failure is still reported.
		if shown := a.showCall(cmd, session, call, result, spec); shown != nil && err == nil {
			err = shown
		}
		return failed(err)
	}
	return command
}

// addParameterFlag adds the option of a parameter with the type of its
// value.
func addParameterFlag(flags *pflag.FlagSet, name string, kind definition.ParamType, help string) {
	// A word in backquotes would be taken for the name of the value.
	help = strings.ReplaceAll(help, "`", "'")
	switch kind {
	case definition.TypeInt:
		flags.Int64(name, 0, help)
	case definition.TypeFloat:
		flags.Float64(name, 0, help)
	case definition.TypeBool:
		flags.Bool(name, false, help)
	case definition.TypeList:
		flags.StringArray(name, nil, help)
		// Several values may follow the option at once.
		flags.Lookup(name).Annotations = map[string][]string{argv.AnnotationWords: {argv.WordsRepeat}}
	default:
		flags.String(name, "", help)
	}
}

// givenParameters returns the parameters that were given on the command
// line, each under the option it was given with and with the type of its
// parameter.
func givenParameters(flags *pflag.FlagSet, call *definition.Call, options map[string][]string) (map[string]any, error) {
	params := map[string]any{}
	for _, parameter := range slices.Concat(call.Parameters, call.StepParameters) {
		for _, name := range options[parameter.Name] {
			if !flags.Changed(name) {
				continue
			}
			var value any
			var err error
			switch parameter.Type {
			case definition.TypeInt:
				value, err = flags.GetInt64(name)
			case definition.TypeFloat:
				value, err = flags.GetFloat64(name)
			case definition.TypeBool:
				// A flag is given or not: --flag=false is not given.
				given, getErr := flags.GetBool(name)
				if err = getErr; !given {
					continue
				}
				value = true
			case definition.TypeList:
				value, err = flags.GetStringArray(name)
			default:
				value, err = flags.GetString(name)
			}
			if err != nil {
				return nil, failed(err)
			}
			params[name] = value
		}
	}
	return params, nil
}

// fillCall reads what was given on the command line into the input of a
// call.
func (a *App) fillCall(cmd *cobra.Command, session *session, call *definition.Call, options map[string][]string, input *engine.CallInput) error {
	flags := cmd.Flags()
	ctx := cmd.Context()
	params, err := givenParameters(flags, call, options)
	if err != nil {
		return err
	}
	input.Params = params
	for _, parameter := range call.Parameters {
		if !parameter.Template {
			continue
		}
		if option := parameter.Name + suffixTemplate; flags.Changed(option) {
			text, _ := flags.GetString(option)
			input.Templates[parameter.Name] = engine.Text{Value: text, Origin: "--" + option}
		}
		if option := parameter.Name + suffixTemplateFile; flags.Changed(option) {
			source, _ := flags.GetString(option)
			text, err := session.engine.ReadSource(ctx, source, "--"+option)
			if err != nil {
				return failed(err)
			}
			input.Templates[parameter.Name] = engine.Text{Value: text, Origin: "--" + option + " " + source}
		}
	}

	input.If, _ = flags.GetStringArray(flagIf)
	for option, target := range map[string]*[][]string{flagIfNone: &input.IfNone, flagIfAny: &input.IfAny} {
		checks, _ := flags.GetStringArray(option)
		for _, check := range checks {
			// Several words given after the option arrive joined; one
			// quoted word is split at its spaces.
			words := strings.Split(check, argv.Separator)
			if len(words) == 1 {
				words = strings.Fields(check)
			}
			*target = append(*target, words)
		}
	}
	if flags.Lookup(flagTemplate) == nil {
		return nil
	}

	input.DryRun, _ = flags.GetBool(flagDryRun)
	if source, _ := flags.GetString(flagTemplate); source != "" {
		file, err := session.engine.LoadCallTemplate(ctx, source)
		if err != nil {
			return failed(err)
		}
		input.File = file
	}
	variables, _ := flags.GetStringArray(flagTemplateVar)
	for _, variable := range variables {
		name, value, found := strings.Cut(variable, "=")
		if !found || name == "" {
			return apikit.Errorf(apikit.KindUsage, "--%s %s: expected NAME=VALUE", flagTemplateVar, variable)
		}
		input.Vars = append(input.Vars, engine.Var{Name: name, Value: value})
	}
	files, _ := flags.GetStringArray(flagTemplateVarFile)
	for _, variable := range files {
		name, source, found := strings.Cut(variable, "=")
		if !found || name == "" || source == "" {
			return apikit.Errorf(apikit.KindUsage, "--%s %s: expected NAME=PATH", flagTemplateVarFile, variable)
		}
		value, err := session.engine.ReadSource(ctx, source, "--"+flagTemplateVarFile+" "+name)
		if err != nil {
			return failed(err)
		}
		input.Vars = append(input.Vars, engine.Var{Name: name, Value: value, Origin: "--" + flagTemplateVarFile})
	}
	return nil
}

// showCall prints what a call did: the parameters of a dry run, a line for
// every request whose answer has nothing to show, or the answer itself.
func (a *App) showCall(cmd *cobra.Command, session *session, call *definition.Call, result *engine.CallResult, spec output.Spec) error {
	if result == nil {
		return nil
	}
	w := cmd.OutOrStdout()
	if result.DryRun != nil {
		for _, parameter := range result.DryRun {
			if _, err := fmt.Fprintln(w, dryRunLine(parameter)); err != nil {
				return failed(err)
			}
		}
		writeLine(a.opts.Streams.Err, "Dry run, "+call.Name+" was not sent")
		return nil
	}
	// A message names what the API returned. A program reading the output
	// gets it as it is; on a terminal its escape sequences and control
	// characters are left out, as in every format that writes lines.
	plain := term.IsTerminal(w)
	for _, message := range result.Messages {
		if plain {
			message = term.Plain(message)
		}
		if _, err := fmt.Fprintln(w, message); err != nil {
			return failed(err)
		}
	}
	if apikit.Falsy(result.Body) {
		return nil
	}
	return a.render(cmd, session, &engine.Result{Resource: result.Resource, Value: result.Body}, view{spec: spec})
}

// dryRunLine writes a parameter of a dry run as YAML: its name and the
// value the call would use, a text of several lines as a block. A value
// that was resolved from another, an ID from a name for example, is
// followed by what was given.
func dryRunLine(parameter engine.ResolvedParam) string {
	entry := yaml.MapSlice{{Key: parameter.Name, Value: yamlValue(parameter.Value)}}
	data, err := yaml.MarshalWithOptions(entry, yaml.UseLiteralStyleIfMultiline(true), yaml.IndentSequence(true))
	if err != nil {
		return parameter.Name + ": " + apikit.Text(parameter.Value)
	}
	text := strings.TrimRight(string(data), "\n")
	if !apikit.Equal(parameter.Given, parameter.Value) && parameter.Given != nil && !strings.Contains(text, "\n") {
		text += "  # given: " + apikit.Text(parameter.Given)
	}
	return text
}

// yamlValue turns a value of the engine into what the YAML encoder writes
// as it is meant: numbers as numbers, objects with their keys in order.
func yamlValue(v any) any {
	switch value := v.(type) {
	case json.Number:
		if whole, err := value.Int64(); err == nil {
			return whole
		}
		if number, err := value.Float64(); err == nil {
			return number
		}
		return value.String()
	case []any:
		list := make([]any, len(value))
		for i, item := range value {
			list[i] = yamlValue(item)
		}
		return list
	case *apikit.Object:
		entries := yaml.MapSlice{}
		for key, item := range value.All() {
			entries = append(entries, yaml.MapItem{Key: key, Value: yamlValue(item)})
		}
		return entries
	}
	return v
}

// allChoices returns every value a parameter can take, whatever the other
// parameters are.
func allChoices(parameter *definition.Parameter) []string {
	choices := slices.Clone(parameter.Choices)
	for _, set := range parameter.ChoicesBy {
		for _, value := range set.Values {
			if !slices.Contains(choices, value) {
				choices = append(choices, value)
			}
		}
	}
	return choices
}

// completeChoices offers the values a parameter can take. Where they depend
// on which other parameter is given, the ones for the parameters typed so
// far are offered.
func completeChoices(call *definition.Call, parameter *definition.Parameter) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
		choices := slices.Clone(parameter.Choices)
		for _, set := range parameter.ChoicesBy {
			other, ok := call.Parameter(set.Parameter)
			if !ok {
				continue
			}
			if slices.ContainsFunc(other.OptionNames(), func(name string) bool {
				flag := cmd.Flags().Lookup(name)
				return flag != nil && flag.Changed
			}) {
				choices = append(choices, set.Values...)
			}
		}
		if len(choices) == 0 {
			choices = allChoices(parameter)
		}
		return startingWith(choices, typed)
	}
}
