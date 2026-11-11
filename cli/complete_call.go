package cli

import (
	"context"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mittwald/api-kit/definition"
)

// lookupTimeout bounds how long a completion waits for the API.
const lookupTimeout = 5 * time.Second

// files is the directive of a completion that leaves the word to the
// shell, which offers file names.
const files = cobra.ShellCompDirectiveDefault

// completeCall registers what the shell offers for the options and words
// of a call.
func (a *App) completeCall(doc *definition.Document, command *cobra.Command, call *definition.Call, options map[string][]string) {
	register := func(name string, complete cobra.CompletionFunc) {
		if command.Flags().Lookup(name) != nil {
			_ = command.RegisterFlagCompletionFunc(name, complete)
		}
	}
	register(flagIf, completeIf(doc, call))
	register(flagIfNone, completeListCheck(doc, call, nil))
	register(flagIfAny, completeListCheck(doc, call, nil))
	register(flagTemplate, a.completeTemplate)
	register(flagTemplateVarFile, func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return nil, files
	})
	for _, parameter := range call.Parameters {
		if parameter.Template {
			register(parameter.Name+suffixTemplateFile, func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
				return nil, files
			})
		}
		if parameter.Lookup != nil && len(allChoices(parameter)) == 0 {
			for _, name := range options[parameter.Name] {
				register(name, a.completeLookup(doc, call, parameter, options))
			}
		}
	}
	// A word after the ID belongs to a list check whose words are still
	// being typed; anything else here is the ID, which nothing is offered
	// for.
	command.ValidArgsFunction = func(cmd *cobra.Command, args []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if pending := a.pending; pending != nil && (pending.Flag == flagIfNone || pending.Flag == flagIfAny) {
			return completeListCheck(doc, call, pending.Words)(cmd, args, typed)
		}
		return nil, noFiles
	}
}

// completeIf offers conditions for --if: those the definition lists for
// the resource of the call and, with the parameter as prefix, for the
// resources its parameters name.
func completeIf(doc *definition.Document, call *definition.Call) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var offers []string
		if call.RequiresID {
			offers = append(offers, doc.Resource(call.Resource).Conditions...)
		}
		for _, parameter := range call.Parameters {
			target := parameterResource(cmd, call, parameter)
			if target == "" {
				continue
			}
			for _, condition := range doc.Resource(target).Conditions {
				offers = append(offers, parameter.Name+"."+condition)
			}
		}
		return startingWith(unique(offers), typed)
	}
}

// parameterResource returns the resource a parameter names: the one the
// definition gives it, or the one that goes with the value of another
// parameter given so far.
func parameterResource(cmd *cobra.Command, call *definition.Call, parameter *definition.Parameter) string {
	target := parameter.Resource
	if target == nil {
		return ""
	}
	if target.By == "" {
		return target.Name
	}
	other, ok := call.Parameter(target.By)
	if !ok {
		return ""
	}
	for _, name := range other.OptionNames() {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || !flag.Changed {
			continue
		}
		for _, pair := range target.Map {
			if pair.Key == flag.Value.String() {
				return pair.Value
			}
		}
	}
	return ""
}

// completeListCheck offers the words of --if-none and --if-any. The first
// word is a resource that can be listed. The words after it, given here as
// the words so far, are filters and conditions of that resource; a filter
// without a value is also offered with what the call can put there: its ID
// and the parameters that name a resource.
func completeListCheck(doc *definition.Document, call *definition.Call, words []string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(words) == 0 {
			var resources []string
			for _, resource := range doc.Resources() {
				if resource.Parent == "" && resource.SupportsListing {
					resources = append(resources, resource.Name)
				}
			}
			return startingWith(resources, typed)
		}
		resource := doc.Resource(doc.ResourceName(words[0]))
		var references []string
		if call.RequiresID {
			references = append(references, "@"+definition.VariableID)
		}
		for _, parameter := range call.Parameters {
			if parameter.Resource != nil {
				references = append(references, "@"+parameter.Name)
			}
		}
		var offers []string
		for _, filter := range slices.Concat(doc.Filters, resource.Filters) {
			offers = append(offers, filter)
			if strings.HasSuffix(filter, "=") {
				for _, reference := range references {
					offers = append(offers, filter+reference)
				}
			}
		}
		offers = append(offers, resource.Conditions...)
		return startingWith(unique(offers), typed)
	}
}

// completeTemplate offers the call templates the tool ships. A word that
// is none of them is left to the shell, since a template can be a file.
func (a *App) completeTemplate(_ *cobra.Command, _ []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var names []string
	if a.opts.Templates != nil {
		entries, _ := fs.ReadDir(a.opts.Templates, ".")
		for _, entry := range entries {
			if extension := path.Ext(entry.Name()); !entry.IsDir() && (extension == ".yaml" || extension == ".yml") {
				names = append(names, strings.TrimSuffix(entry.Name(), extension))
			}
		}
	}
	matches, _ := startingWith(names, typed)
	return matches, files
}

// completeLookup offers what the listing of a parameter's lookup holds, an
// email for each member of a project for example. It asks the API with the
// token at hand and never for a login; any trouble offers nothing.
func (a *App) completeLookup(doc *definition.Document, call *definition.Call, parameter *definition.Parameter, options map[string][]string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
		params, err := givenParameters(cmd.Flags(), call, options)
		if err != nil {
			return nil, noFiles
		}
		session, err := a.session(doc)
		if err != nil {
			return nil, noFiles
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), lookupTimeout)
		defer cancel()
		offers, err := session.engine.CompleteLookup(ctx, call.Resource, call.Name, parameter.Name, params, typed)
		if err != nil {
			return nil, noFiles
		}
		return offers, noFiles
	}
}

// unique returns the words without repetitions, in their order.
func unique(words []string) []string {
	var out []string
	for _, word := range words {
		if !slices.Contains(out, word) {
			out = append(out, word)
		}
	}
	return out
}
