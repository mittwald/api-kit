package definition

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/expr"
)

// optionName is what a parameter may be called: it becomes a command-line
// option and a template variable.
var optionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// reservedVariables are the names every call defines itself.
var reservedVariables = []string{VariableID, VariableResult, VariableChanges}

// validate checks what only shows across keys: references between
// resources, setters that form one call, and templates that use names.
func (l *loader) validate(doc *Document) {
	for _, resource := range doc.resources {
		path := join(keyResources, resource.Name)
		if resource.API != "" {
			l.knownAPI(join(path, "api"), doc, resource.API)
		}
		for i, condition := range resource.Conditions {
			if _, err := expr.ParseCondition(condition); err != nil {
				l.problemf(index(join(path, "conditions"), i), "must be a condition like enabled=false, not %q", condition)
			}
		}
		l.setters(doc, resource, path)
	}
}

// call collects what the setters of one name share.
type call struct {
	setters     int
	options     []string              // option names of all parameters
	parameters  map[string]*Parameter // parameters by name
	stepOptions map[string]*Parameter // step parameters by option name
	requiresID  *bool
	seenOptions []string // option names of the setters checked so far
	grouped     []string // parameters already in a group
}

func (l *loader) setters(doc *Document, resource *Resource, path string) {
	calls := map[string]*call{}
	var names []string
	for _, setter := range resource.Setters {
		c := calls[setter.Name]
		if c == nil {
			c = &call{parameters: map[string]*Parameter{}, stepOptions: map[string]*Parameter{}}
			calls[setter.Name] = c
			names = append(names, setter.Name)
		}
		c.setters++
		for _, parameter := range setter.Parameters {
			c.options = append(c.options, parameter.OptionNames()...)
			c.parameters[parameter.Name] = parameter
		}
	}

	aliasCalls := map[string]string{}
	for _, setter := range resource.Setters {
		c := calls[setter.Name]
		if slices.Contains(l.reserved.Commands, setter.Name) {
			l.problemf(join(setter.path, keyName), "%q is the name of a built-in command", setter.Name)
		}
		l.setterAliases(setter, names, aliasCalls)
		l.parameterNames(setter, c)
		if setter.API != "" {
			l.knownAPI(join(setter.path, "api"), doc, setter.API)
		}

		// Setters that share a name form one call, which runs those whose
		// parameters were given.
		if c.setters > 1 {
			if c.requiresID == nil {
				c.requiresID = &setter.RequiresID
			} else if *c.requiresID != setter.RequiresID {
				l.problemf(join(setter.path, "requiresId"), "setters named %q must agree on it", setter.Name)
			}
			if len(setter.Parameters) == 0 {
				l.problemf(setter.path, "a setter that shares its name needs parameters, which select it")
			}
			var own []string
			for _, parameter := range setter.Parameters {
				for _, option := range parameter.OptionNames() {
					if slices.Contains(c.seenOptions, option) {
						l.problemf(parameter.path, "name or alias %q is used by more than one %q setter", option, setter.Name)
					}
					own = append(own, option)
				}
			}
			c.seenOptions = append(c.seenOptions, own...)
		}

		parameterNames := map[string]bool{}
		for _, parameter := range setter.Parameters {
			parameterNames[parameter.Name] = true
		}
		for _, parameter := range setter.StepParameters {
			parameterNames[parameter.Name] = true
		}
		variableSources := l.parameterExtras(doc, setter, parameterNames)

		known := maps.Clone(parameterNames)
		for variable := range variableSources {
			known[variable] = true
		}
		if setter.RequiresID {
			known[VariableID] = true
		}
		stepNames := l.steps(doc, setter.Steps, known, false)
		for _, name := range stepNames {
			known[name] = true
		}
		thenKnown := maps.Clone(known)
		thenKnown[VariableResult] = true
		l.steps(doc, setter.Then, thenKnown, true)

		for _, variable := range variablesOf(setter.Token) {
			if !slices.Contains(stepNames, variable) {
				l.problemf(join(setter.path, "token"), "template variable %q is not a step; a token comes from a step", variable)
			}
		}
		l.setterTemplates(setter, known)
		l.parameterGroups(setter, c)
		l.sharedVariables(setter, variableSources)
	}

	for _, name := range names {
		l.callOptions(resource, name, calls[name], path)
	}
}

// setterAliases checks the aliases of a setter: none may be a setter name,
// only setters of one name may share one, and none is listed twice.
func (l *loader) setterAliases(setter *Setter, names []string, aliasCalls map[string]string) {
	path := join(setter.path, keyAliases)
	for i, alias := range setter.Aliases {
		switch {
		case slices.Contains(names, alias):
			l.problemf(index(path, i), "%q is the name of a setter", alias)
		case slices.Index(setter.Aliases, alias) != i:
			l.problemf(index(path, i), "%q is listed more than once", alias)
		default:
			owner, taken := aliasCalls[alias]
			if taken && owner != setter.Name {
				l.problemf(index(path, i), "%q is an alias of %q setters as well; only setters of one name may share an alias", alias, owner)
			}
			aliasCalls[alias] = setter.Name
		}
	}
}

// parameterNames checks the names and aliases of the parameters and step
// parameters of a setter.
func (l *loader) parameterNames(setter *Setter, c *call) {
	var seen []string
	check := func(parameter *Parameter, what string) {
		for _, option := range parameter.OptionNames() {
			switch {
			case option == "":
				// Reported as a missing name already.
			case !optionName.MatchString(option):
				l.problemf(parameter.path, "%s %q must start with a letter and hold only letters, digits, - and _", what, option)
			case slices.ContainsFunc(seen, func(other string) bool { return sameOption(other, option) }):
				l.problemf(parameter.path, "%s %q is used more than once", what, option)
			}
			seen = append(seen, option)
		}
	}
	for _, parameter := range setter.Parameters {
		check(parameter, "parameter name or alias")
	}

	// Setters of one call may define a step parameter alike; it is one
	// option of the call then.
	seen = nil
	for _, parameter := range setter.StepParameters {
		check(parameter, "step parameter name or alias")
		for _, option := range parameter.OptionNames() {
			if slices.Contains(c.options, option) {
				l.problemf(parameter.path, "step parameter name or alias %q is taken by a parameter", option)
				continue
			}
			if other, ok := c.stepOptions[option]; ok && !apikit.Equal(other.raw, parameter.raw) {
				l.problemf(parameter.path, "step parameter %q is defined differently by another %q setter", option, setter.Name)
			}
			c.stepOptions[option] = parameter
		}
	}
}

// sameOption reports whether two names are one option or one template
// variable: dashes and underscores are interchangeable in templates.
func sameOption(a, b string) bool {
	return strings.ReplaceAll(a, "_", "-") == strings.ReplaceAll(b, "_", "-")
}

// parameterExtras checks what parameters refer to: resources, other
// parameters, and the variables they set. It returns, for each variable,
// the parameters that set it.
func (l *loader) parameterExtras(doc *Document, setter *Setter, parameterNames map[string]bool) map[string][]string {
	own := map[string]bool{}
	for _, parameter := range setter.Parameters {
		own[parameter.Name] = true
	}
	variableSources := map[string][]string{}
	for _, parameter := range setter.Parameters {
		if target := parameter.Resource; target != nil {
			path := join(parameter.path, "resource")
			if target.By == "" {
				l.knownResource(path, doc, target.Name)
			} else {
				if !own[target.By] {
					l.problemf(join(path, "parameter"), "%q is not a parameter of the setter", target.By)
				}
				for _, pair := range target.Map {
					l.knownResource(join(join(path, "resources"), pair.Key), doc, pair.Value)
				}
			}
		}
		for _, variable := range parameter.Variables {
			path := join(join(parameter.path, "variables"), variable.Key)
			if parameterNames[variable.Key] || slices.Contains(reservedVariables, variable.Key) {
				l.problemf(path, "the name is taken by a parameter or by the call itself")
			}
			for _, used := range variablesOf(variable.Value) {
				if !parameterNames[used] {
					l.problemf(path, "template variable %q is not a parameter", used)
				}
			}
			variableSources[variable.Key] = append(variableSources[variable.Key], parameter.Name)
		}
		for _, choices := range parameter.ChoicesBy {
			if !own[choices.Parameter] {
				l.problemf(join(join(parameter.path, "choices"), choices.Parameter), "%q is not a parameter of the setter", choices.Parameter)
			}
		}
	}

	// A lookup runs before the steps, so it sees parameters and variables.
	for _, parameter := range setter.Parameters {
		lookup := parameter.Lookup
		if lookup == nil {
			continue
		}
		path := join(parameter.path, "lookup")
		if lookup.Get != "" && len(variablesOf(lookup.Get)) == 0 {
			l.knownResource(join(path, "get"), doc, lookup.Get)
		}
		for _, used := range variablesOf(lookup.raw) {
			if _, isVariable := variableSources[used]; !parameterNames[used] && !isVariable {
				l.problemf(path, "template variable %q is not a parameter or a variable", used)
			}
		}
	}
	return variableSources
}

// knownResource reports a reference to a resource the definition does not
// describe.
func (l *loader) knownResource(path string, doc *Document, name string) {
	if _, ok := doc.byName[name]; !ok && name != "" {
		l.problemf(path, "unknown resource %q", name)
	}
}

// steps checks the steps of a setter and returns the names they add. known
// are the names a step may use besides those of earlier steps.
func (l *loader) steps(doc *Document, steps []*Step, known map[string]bool, then bool) []string {
	var names []string
	for _, step := range steps {
		if known[step.As] || slices.Contains(names, step.As) || slices.Contains(reservedVariables, step.As) {
			l.problemf(join(step.path, "as"), "%q is taken by a parameter, another step or the call itself", step.As)
		}
		switch {
		case step.Get != "" && then:
			l.problemf(step.path, "only call steps can run after the request")
		case step.Get != "":
			l.knownResource(join(step.path, "get"), doc, step.Get)
		case step.Call != "" && step.Resource != "":
			target, ok := doc.byName[step.Resource]
			if !ok {
				l.problemf(join(step.path, "resource"), "unknown resource %q", step.Resource)
			} else if !hasCall(target, step.Call) {
				l.problemf(join(step.path, "call"), "resource %q has no call %q", step.Resource, step.Call)
			}
		}
		// A step sees the parameters, the ID and the steps before it.
		for _, variable := range variablesOf(step.raw) {
			if !known[variable] && !slices.Contains(names, variable) {
				l.problemf(step.path, "template variable %q is not a parameter or an earlier step", variable)
			}
		}
		names = append(names, step.As)
	}
	return names
}

// hasCall reports whether a resource has a setter of that name or alias.
func hasCall(resource *Resource, name string) bool {
	return slices.ContainsFunc(resource.Setters, func(setter *Setter) bool {
		return setter.Name == name || slices.Contains(setter.Aliases, name)
	})
}

// setterTemplates checks the variables of the path, the payload and the
// message. A variable without a value would be sent as it is written.
func (l *loader) setterTemplates(setter *Setter, known map[string]bool) {
	check := func(key string, template any) {
		for _, variable := range variablesOf(template) {
			switch {
			case known[variable]:
			case variable == VariableID:
				l.problemf(join(setter.path, key), "template variable %q needs 'requiresId' to be true", variable)
			default:
				l.problemf(join(setter.path, key), "template variable %q is not a parameter or a step", variable)
			}
		}
	}
	check("pathTemplate", setter.PathTemplate)
	check("payloadTemplate", setter.PayloadTemplate)
	if !setter.RequiresID && slices.Contains(variablesOf(setter.Message), VariableID) {
		l.problemf(join(setter.path, "message"), "template variable %q needs 'requiresId' to be true", VariableID)
	}
}

// parameterGroups checks the groups of parameters that exclude each other.
// A group may name parameters of every setter that shares the name, and a
// parameter may be in one group only.
func (l *loader) parameterGroups(setter *Setter, c *call) {
	check := func(key string, groups [][]string, minimum int) {
		for i, group := range groups {
			path := index(join(setter.path, key), i)
			if len(group) < minimum {
				l.problemf(path, "needs %d parameters or more", minimum)
			}
			for _, name := range group {
				parameter, ok := c.parameters[name]
				switch {
				case !ok:
					l.problemf(path, "unknown parameter %q", name)
					continue
				case parameter.Required:
					l.problemf(path, "parameter %q is required and cannot be in a group", name)
				}
				if slices.Contains(c.grouped, name) {
					l.problemf(path, "parameter %q is in more than one group", name)
				}
				c.grouped = append(c.grouped, name)
			}
		}
	}
	check("exclusiveParameters", setter.Exclusive, 1)
	check("oneOfParameters", setter.OneOf, 2)
}

// sharedVariables checks that parameters that set the same variable exclude
// each other.
func (l *loader) sharedVariables(setter *Setter, variableSources map[string][]string) {
	groups := slices.Concat(setter.Exclusive, setter.OneOf)
	for _, variable := range slices.Sorted(maps.Keys(variableSources)) {
		sources := variableSources[variable]
		if len(sources) < 2 {
			continue
		}
		together := slices.ContainsFunc(groups, func(group []string) bool {
			return !slices.ContainsFunc(sources, func(source string) bool { return !slices.Contains(group, source) })
		})
		if !together {
			l.problemf(setter.path, "parameters %s all set variable %q and must share an 'exclusiveParameters' or 'oneOfParameters' group",
				strings.Join(slices.Sorted(slices.Values(sources)), ", "), variable)
		}
	}
}

// callOptions checks the options of a call against those the command line
// adds itself.
func (l *loader) callOptions(resource *Resource, name string, c *call, resourcePath string) {
	taken := slices.Concat(c.options, slices.Collect(maps.Keys(c.stepOptions)))
	slices.Sort(taken)
	taken = slices.Compact(taken)

	var templateOptions []string
	for _, setter := range resource.Setters {
		if setter.Name != name {
			continue
		}
		for _, parameter := range setter.Parameters {
			if !parameter.Template {
				continue
			}
			for _, suffix := range l.reserved.TemplateOptionSuffixes {
				templateOptions = append(templateOptions, parameter.Name+suffix)
			}
		}
	}
	if len(templateOptions) > 0 {
		templateOptions = append(templateOptions, l.reserved.TemplateCallOptions...)
	}

	path := join(resourcePath, keySetters)
	for _, option := range taken {
		switch {
		case slices.Contains(l.reserved.GlobalOptions, option):
			l.problemf(path, "option %q of call %q is taken by an option every command has", option, name)
		case slices.Contains(l.reserved.CallOptions, option):
			l.problemf(path, "option %q of call %q is taken by an option every call has", option, name)
		case slices.Contains(templateOptions, option):
			l.problemf(path, "option %q of call %q is taken by the options its templates add", option, name)
		}
	}
}

// variablesOf returns the names of the ${...} placeholders in a template,
// or anywhere in a structured one, sorted and without duplicates. A plain
// $name is not counted: without a value it stays text.
func variablesOf(template any) []string {
	seen := map[string]bool{}
	collectVariables(template, seen)
	return slices.Sorted(maps.Keys(seen))
}

func collectVariables(v any, seen map[string]bool) {
	switch value := v.(type) {
	case string:
		for _, placeholder := range expr.ParseTemplate(value).Placeholders() {
			if placeholder.Braced {
				seen[placeholder.Name] = true
			}
		}
	case []any:
		for _, item := range value {
			collectVariables(item, seen)
		}
	case *apikit.Object:
		for key, item := range value.All() {
			collectVariables(key, seen)
			collectVariables(item, seen)
		}
	}
}
