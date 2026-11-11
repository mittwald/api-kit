package cli

import (
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/output"
)

// noFiles is the directive of a completion that offers words of its own
// and no file names.
const noFiles = cobra.ShellCompDirectiveNoFileComp

// startingWith returns the candidates that start with what was typed. When
// each of them still needs something appended, a filter key that wants its
// value for example, the shell is told not to add a space.
func startingWith(candidates []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var matches []cobra.Completion
	open := true
	for _, candidate := range candidates {
		word, _, _ := strings.Cut(candidate, "\t")
		if !strings.HasPrefix(word, typed) {
			continue
		}
		matches = append(matches, candidate)
		open = open && strings.HasSuffix(word, "=")
	}
	if open && len(matches) > 0 {
		return matches, noFiles | cobra.ShellCompDirectiveNoSpace
	}
	return matches, noFiles
}

// completeGetArgs offers the arguments of get: a resource that can be
// listed, nothing for the ID, and the sub-resources of the resource.
func completeGetArgs(doc *definition.Document) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var names []string
		switch len(args) {
		case 0:
			for _, resource := range doc.Resources() {
				if resource.Parent == "" && resource.SupportsListing {
					names = append(names, resource.Name)
				}
			}
		case 2:
			for _, resource := range doc.SubResources(doc.ResourceName(args[0])) {
				names = append(names, resource.Name)
			}
		}
		return startingWith(names, typed)
	}
}

// completeFilter offers the filters of the resource that get is about to
// list: the ones every resource has and its own. A filter is "key=" or
// "key=value"; several can be typed with commas between them, and the one
// being typed is completed.
func completeFilter(doc *definition.Document) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
		filters := slices.Clone(doc.Filters)
		switch {
		case len(args) >= 3:
			filters = append(filters, doc.Resource(doc.ResourceName(args[2])).Filters...)
		case len(args) >= 1:
			filters = append(filters, doc.Resource(doc.ResourceName(args[0])).Filters...)
		}
		done := ""
		if comma := strings.LastIndex(typed, ","); comma >= 0 {
			done = typed[:comma+1]
		}
		// A filter of the resource may be one that every resource has.
		var offers []string
		for _, filter := range filters {
			if !slices.Contains(offers, done+filter) {
				offers = append(offers, done+filter)
			}
		}
		return startingWith(offers, typed)
	}
}

// formatHelp says in a few words what each output format is.
var formatHelp = map[output.Format]string{
	output.Text:     "table of the usual columns",
	output.Wide:     "table of every field",
	output.JSON:     "JSON, with related resources",
	output.JSONRaw:  "JSON as the API returned it",
	output.YAML:     "YAML, with related resources",
	output.JSONPath: "what a JSONPath expression selects",
	output.Columns:  "chosen fields, separated by tabs",
	output.ID:       "the ID of each item",
	output.Name:     "the name of each item",
	output.Link:     "the link to each item",
	output.CSV:      "comma-separated values",
	output.TSV:      "tab-separated values",
	output.Markdown: "Markdown table",
}

// completeOutput offers the output formats. The ones that take an argument
// end in an equals sign.
func completeOutput(_ *cobra.Command, _ []string, typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var formats []string
	for _, name := range output.Formats() {
		format := output.Format(name)
		if format.TakesArgument() {
			name += "="
		}
		formats = append(formats, name+"\t"+formatHelp[format])
	}
	return startingWith(formats, typed)
}
