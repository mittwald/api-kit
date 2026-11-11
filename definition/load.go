package definition

import (
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// Reserved are the names the command line of a tool uses itself. A
// definition cannot take them for its calls and parameters.
type Reserved struct {
	// Commands are the names of the built-in commands. Every call is a
	// command too.
	Commands []string
	// GlobalOptions are the options every command has.
	GlobalOptions []string
	// CallOptions are the options every call has.
	CallOptions []string
	// TemplateCallOptions are the options a call gets when one of its
	// parameters takes a template.
	TemplateCallOptions []string
	// TemplateOptionSuffixes are appended to the name of a parameter that
	// takes a template, for the options that give the template.
	TemplateOptionSuffixes []string
}

// LoadOptions tell [Load] what to merge into the base definition and which
// names are taken.
type LoadOptions struct {
	// Overlays are merged into the base definition, in order.
	Overlays []Source
	// Mode says whether overlays may only add or also replace.
	Mode Mode
	// Reserved are the names the command line uses itself.
	Reserved Reserved
}

// Load reads the base definition, merges the overlays into it and checks
// the result. It returns the definition, and warnings about parts of
// overlays that were ignored.
//
// A definition with mistakes does not load: the error is an [*apikit.Error]
// of kind [apikit.KindDefinition] that wraps an [*Error] listing all of
// them. The base definition is checked on its own first, so mistakes
// reported after that come from the overlays.
func Load(base Source, o LoadOptions) (*Document, []Diagnostic, error) {
	root, err := parse(base)
	if err != nil {
		return nil, nil, err
	}
	doc, err := check(root, base.Name, o.Reserved)
	if err != nil || len(o.Overlays) == 0 {
		return doc, nil, err
	}

	var warnings []Diagnostic
	names := make([]string, len(o.Overlays))
	for i, overlay := range o.Overlays {
		names[i] = overlay.Name
		tree, err := parse(overlay)
		if err != nil {
			return nil, warnings, err
		}
		merged, found := Merge(root, tree, o.Mode, overlay.Name)
		root = merged
		warnings = append(warnings, found...)
	}
	doc, err = check(root, strings.Join(names, ", "), o.Reserved)
	return doc, warnings, err
}

// check decodes and validates a merged document.
func check(root *apikit.Object, source string, reserved Reserved) (*Document, error) {
	l := &loader{source: source, reserved: reserved}
	doc := l.document(root)
	l.validate(doc)
	if len(l.problems) > 0 {
		return nil, newError(source, l.problems)
	}
	return doc, nil
}
