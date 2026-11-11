package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// The options that give templates, as messages name them.
const (
	optionTemplate    = "--template"
	optionTemplateVar = "--template-var"
)

// templateOptionSuffixes are appended to the name of a parameter for the
// options that give its value as a template.
var templateOptionSuffixes = []string{"-template", "-template-file"}

// templateExtensions are the file extensions of the call templates a tool
// ships.
var templateExtensions = []string{".yaml", ".yml"}

// The keys of a call template.
const (
	templateKeyResource   = "resource"
	templateKeyCall       = "call"
	templateKeyVariables  = "variables"
	templateKeyParameters = "parameters"
)

var (
	// sourceURL matches a source that is fetched, not read from a file.
	sourceURL = regexp.MustCompile(`(?i)^https?://`)
	// variableName is what a template variable may be called.
	variableName = regexp.MustCompile(`^[_a-zA-Z][_a-zA-Z0-9-]*$`)
)

// Text is a value that came from somewhere worth naming in a message.
type Text struct {
	// Value is the text.
	Value string
	// Origin says where the text came from, the way a message names it:
	// the option, and the source if it names one, as in
	// "--message-template-file body.txt".
	Origin string
}

// Var is a template variable given for one call.
type Var struct {
	// Name is what templates call the variable.
	Name string
	// Value is its value.
	Value string
	// Origin is the option that gave the variable, for messages. Empty
	// means "--template-var".
	Origin string
}

// CallTemplate is a call-template document: values for the parameters of a
// call, kept in a file so that a call that is made often need not be
// spelled out each time.
type CallTemplate struct {
	// Resource is the resource the template is for. Empty means any.
	Resource string
	// Call is the call the template is for, by its name or an alias. Empty
	// means any.
	Call string
	// Variables are the template variables the document declares, each
	// with its default: a string or a number, or nil for a variable that
	// has to be given.
	Variables *apikit.Object
	// Parameters are the values of parameters, by name or alias. The value
	// of a parameter that takes a template is one.
	Parameters *apikit.Object
	// Origin says where the document came from, the way a message names
	// it, as in "--template welcome.yaml".
	Origin string
}

// stdinOnce is the standard input of an Engine after a source has read it.
// It can be read once, so it remembers what did.
type stdinOnce struct {
	io.Reader
	// reader names what read the input.
	reader string
}

// ReadSource returns the text a source holds: a file, a URL, or "-" for
// [Options.Stdin]. The line breaks at its end are dropped, as a shell does
// for $(cat file). A URL is fetched without the credential, since it may
// lead anywhere. what names the text in messages, best by the option that
// gave the source, as in "--message-template-file".
//
// Standard input can be read once: a second source that asks for it is an
// error of kind [apikit.KindUsage]. A source that cannot be read, or that
// is no UTF-8 text, is one of kind [apikit.KindFailed].
func (e *Engine) ReadSource(ctx context.Context, source, what string) (string, error) {
	text, err := e.readSource(ctx, source, what)
	return strings.TrimRight(text, "\n"), err
}

// readSource returns the text a source holds as it is.
func (e *Engine) readSource(ctx context.Context, source, what string) (string, error) {
	var data []byte
	var err error
	switch {
	case source == "-":
		if data, err = e.readStdin(what); apikit.KindOf(err) == apikit.KindUsage {
			return "", err
		}
	case sourceURL.MatchString(source):
		data, err = e.download(ctx, source)
	default:
		data, err = os.ReadFile(expandHome(source))
	}
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case err == nil && !utf8.Valid(data):
		err = errors.New("it is no UTF-8 text")
	}
	if err != nil {
		return "", apikit.Errorf(apikit.KindFailed, "unable to read %s %s: %w", what, source, err)
	}
	return string(data), nil
}

// readStdin reads the standard input for what, which is the only one that
// may: whoever asks next would get nothing and not know why.
func (e *Engine) readStdin(what string) ([]byte, error) {
	e.mu.Lock()
	once, read := e.stdin.(*stdinOnce)
	if !read {
		once = &stdinOnce{Reader: e.stdin, reader: what}
		e.stdin = once
	}
	e.mu.Unlock()
	switch {
	case read && once.reader == what:
		return nil, apikit.Errorf(apikit.KindUsage, "only one %s can read stdin (-)", what)
	case read:
		return nil, apikit.Errorf(apikit.KindUsage, "only one of %s and %s can read stdin (-)", once.reader, what)
	case once.Reader == nil:
		return nil, nil
	}
	return io.ReadAll(once.Reader)
}

// download fetches a source from a URL. The request carries no credential.
func (e *Engine) download(ctx context.Context, source string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	switch {
	case err != nil:
		return nil, err
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("the server answered with status %d", resp.StatusCode)
	}
	return data, nil
}

// expandHome returns path with a leading ~ replaced by the home directory
// of the user. A shell does that for a path that stands alone, but not for
// one after the equals sign of an option.
func expandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok || (rest != "" && !os.IsPathSeparator(rest[0])) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, rest)
}

// BuiltinTemplates returns the names of the call templates the tool ships,
// sorted: the files of [Options.Templates] without their extension.
func (e *Engine) BuiltinTemplates() []string {
	if e.templates == nil {
		return nil
	}
	entries, err := fs.ReadDir(e.templates, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		extension := path.Ext(entry.Name())
		if entry.Type().IsRegular() && slices.Contains(templateExtensions, extension) {
			names = append(names, strings.TrimSuffix(entry.Name(), extension))
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// templateText returns the text of the call template a source names: a
// file, a URL or standard input as [Engine.ReadSource] reads them, else the
// built-in template of that name.
func (e *Engine) templateText(ctx context.Context, source string) (string, error) {
	_, err := os.Stat(expandHome(source))
	if source == "-" || sourceURL.MatchString(source) || err == nil {
		return e.readSource(ctx, source, optionTemplate)
	}
	// A path that leads nowhere is no name.
	if e.templates != nil && !strings.ContainsAny(source, `/\`) {
		for _, name := range []string{source, source + templateExtensions[0], source + templateExtensions[1]} {
			info, err := fs.Stat(e.templates, name)
			if err != nil || !info.Mode().IsRegular() || !slices.Contains(templateExtensions, path.Ext(name)) {
				continue
			}
			data, err := fs.ReadFile(e.templates, name)
			if err != nil {
				return "", apikit.Errorf(apikit.KindFailed, "unable to read the built-in template %s: %w", source, err)
			}
			return string(data), nil
		}
	}
	builtin := "none"
	if names := e.BuiltinTemplates(); len(names) > 0 {
		builtin = strings.Join(names, ", ")
	}
	return "", apikit.Errorf(apikit.KindUsage, "%s %s is no file, URL or built-in template (built-in: %s)",
		optionTemplate, source, builtin)
}

// LoadCallTemplate reads the call template a source names: a file, a URL
// or "-" as [Engine.ReadSource] reads them, else the template of that name
// among those the tool ships.
//
// The document is YAML: a mapping with the optional keys "resource" and
// "call", which say what the template is for, "variables" with the default
// of each template variable, and "parameters". A source that names
// nothing, and a document of another shape, is an error of kind
// [apikit.KindUsage]. Whether the parameters fit a call shows when
// [Engine.Apply] gets the template.
func (e *Engine) LoadCallTemplate(ctx context.Context, source string) (*CallTemplate, error) {
	text, err := e.templateText(ctx, source)
	if err != nil {
		return nil, err
	}
	origin := optionTemplate + " " + source
	var decoded any
	if err := yaml.UnmarshalWithOptions([]byte(text), &decoded, yaml.UseOrderedMap()); err != nil {
		// The line and column, without the lines of the document that the
		// decoder quotes: the message stays one line.
		return nil, &apikit.Error{
			Kind: apikit.KindUsage,
			Msg:  origin + " is no valid YAML: " + yaml.FormatError(err, false, false),
			Err:  err,
		}
	}
	document, ok := fromYAML(decoded).(*apikit.Object)
	if !ok {
		return nil, apikit.Errorf(apikit.KindUsage, "%s must be a mapping with '%s'", origin, templateKeyParameters)
	}
	known := []string{templateKeyResource, templateKeyCall, templateKeyVariables, templateKeyParameters}
	unknown := slices.DeleteFunc(document.Keys(), func(key string) bool { return slices.Contains(known, key) })
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, apikit.Errorf(apikit.KindUsage, "%s has unknown keys %s; known are %s",
			origin, strings.Join(unknown, ", "), strings.Join(known, ", "))
	}

	template := &CallTemplate{Origin: origin, Variables: apikit.NewObject(), Parameters: apikit.NewObject()}
	resource, _ := document.Get(templateKeyResource)
	call, _ := document.Get(templateKeyCall)
	template.Resource, template.Call = apikit.Text(resource), apikit.Text(call)

	if parameters, _ := document.Get(templateKeyParameters); parameters != nil {
		if template.Parameters, ok = parameters.(*apikit.Object); !ok {
			return nil, apikit.Errorf(apikit.KindUsage, "%s: '%s' must map parameter names to values",
				origin, templateKeyParameters)
		}
	}
	if variables, _ := document.Get(templateKeyVariables); variables != nil {
		template.Variables, ok = variables.(*apikit.Object)
		for name, value := range template.Variables.All() {
			switch value.(type) {
			case nil, string, json.Number:
				ok = ok && variableName.MatchString(name)
			default:
				ok = false
			}
		}
		if !ok {
			return nil, apikit.Errorf(apikit.KindUsage,
				"%s: '%s' must map variable names to a default value, or to nothing for a variable without one",
				origin, templateKeyVariables)
		}
	}
	return template, nil
}

// fromYAML turns what the YAML decoder produced into the values of package
// apikit. A number JSON cannot hold, a NaN or an infinity, becomes the
// text it was written as: it is no value any parameter takes, and the
// message about that names it.
func fromYAML(v any) any {
	switch value := v.(type) {
	case yaml.MapSlice:
		object := apikit.NewObject()
		for _, item := range value {
			object.Set(fmt.Sprint(item.Key), fromYAML(item.Value))
		}
		return object
	case []any:
		list := make([]any, len(value))
		for i, item := range value {
			list[i] = fromYAML(item)
		}
		return list
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Sprint(value)
		}
		return json.Number(strconv.FormatFloat(value, 'g', -1, 64))
	case nil, bool, string:
		return value
	}
	// The whole numbers, of whatever size.
	return json.Number(fmt.Sprint(v))
}

// userTemplate is the value of a parameter given as a template.
type userTemplate struct {
	parameter *definition.Parameter
	// text is the template.
	text string
	// origin says where the template came from, for messages.
	origin string
}

// fill completes the arguments of a call from its templates: the
// parameters given as a template, and a call template that gives further
// parameters. What was given directly wins over the call template, also
// over a parameter it gives of a group that already has one.
//
// A template is written by whoever runs the call, and what it yields is
// sent to other people. So every placeholder must have a value, the plain
// $name form included, and a variable that no template uses is a mistake
// too. A template sees the parameters as they were given, not as they are
// resolved later, the variables, and the fields of the items that
// parameters name, as in ${owner.name}.
//
// A call of one setter needs its required parameters here, since a
// template may supply them. A shared call checks them when its setters are
// selected.
func (r *callRun) fill(ctx context.Context, in CallInput) error {
	call := r.call
	templates, err := r.givenTemplates(in.Templates)
	if err != nil {
		return err
	}
	templated := func(name string) bool {
		return slices.ContainsFunc(templates, func(template userTemplate) bool { return template.parameter.Name == name })
	}
	if in.File != nil {
		if templates, err = r.fromFile(in.File, templates, templated); err != nil {
			return err
		}
	}

	if !call.Shared() {
		setter := call.Setters[0]
		var missing []string
		for _, parameter := range setter.Parameters {
			if !parameter.Required || r.args.has(parameter.Name) || templated(parameter.Name) {
				continue
			}
			options := []string{"--" + parameter.Name}
			for _, suffix := range templateOptionSuffixes {
				if parameter.Template {
					options = append(options, "--"+parameter.Name+suffix)
				}
			}
			missing = append(missing, strings.Join(options, "/"))
		}
		if len(missing) > 0 {
			return apikit.Errorf(apikit.KindUsage, "%s needs %s", call.Name, strings.Join(missing, ", "))
		}
		for _, group := range setter.OneOf {
			if !slices.ContainsFunc(group, func(name string) bool { return r.args.has(name) || templated(name) }) {
				return apikit.Errorf(apikit.KindUsage, "%s needs exactly one of %s", call.Name, optionList(group))
			}
		}
	}

	// What the templates see by name: the defaults of the call template,
	// the variables, and the parameters that are no templates themselves.
	seen := values{}
	if in.File != nil {
		for name, value := range in.File.Variables.All() {
			if value != nil {
				seen.set(name, value)
			}
		}
	}
	variables := values{}
	var order []string
	for _, variable := range in.Vars {
		origin := variable.Origin
		if origin == "" {
			origin = optionTemplateVar
		}
		parameter, isParameter := variableParameter(call, variable.Name)
		switch {
		case !variableName.MatchString(variable.Name):
			return apikit.Errorf(apikit.KindUsage, "%s %s: a variable is named with letters, digits, - and _", origin, variable.Name)
		case variableKey(variable.Name) == definition.VariableID:
			return apikit.Errorf(apikit.KindUsage, "%s %s: %s is the ID of the call", origin, variable.Name, variable.Name)
		case isParameter:
			return apikit.Errorf(apikit.KindUsage, "%s %s: %s is a parameter, give --%s", origin, variable.Name, variable.Name, parameter.Name)
		case variables.has(variable.Name):
			return apikit.Errorf(apikit.KindUsage, "template variable %s is given more than once", variable.Name)
		}
		variables.set(variable.Name, variable.Value)
		order = append(order, variable.Name)
	}
	maps.Copy(seen, variables)
	for _, parameter := range slices.Concat(call.Parameters, call.StepParameters) {
		if value, ok := r.args.scalar(parameter.Name); ok && !parameter.Template {
			seen.set(parameter.Name, value)
		}
	}
	if in.ID != "" {
		seen.set(definition.VariableID, in.ID)
	}

	var failure error
	lookup := func(name string) (any, bool) {
		if value, ok := seen.get(name); ok {
			return value, true
		}
		head, field, dotted := strings.Cut(name, ".")
		parameter, named := resourceParameter(call, head)
		if !dotted || !named || !r.args.has(parameter.Name) || failure != nil {
			return nil, false
		}
		// Fetched when a template asks for it; the Engine remembers the
		// answer for the next placeholder.
		item, err := r.item(ctx, parameter)
		if err != nil {
			failure = err
			return nil, false
		}
		selector, err := expr.ParsePath(field)
		if err != nil {
			return nil, false
		}
		value := selector.Get(item, definition.ExtraKey)
		return value, value != nil
	}
	used := values{}
	for _, template := range templates {
		parsed := expr.ParseTemplate(template.text)
		for _, placeholder := range parsed.Placeholders() {
			used.set(placeholder.Name, true)
		}
		text, missing := parsed.Render(lookup, expr.RenderOptions{Missing: expr.Report, Format: templateValueText})
		switch {
		case failure != nil:
			return failure
		case len(missing) > 0:
			return r.missingValues(template.origin, missing)
		}
		r.args.set(template.parameter.Name, text)
	}

	order = slices.DeleteFunc(order, used.has)
	switch len(order) {
	case 0:
		return nil
	case 1:
		return apikit.Errorf(apikit.KindUsage, "template variable %s: no template uses it", order[0])
	}
	return apikit.Errorf(apikit.KindUsage, "template variable %s: no template uses them", strings.Join(order, ", "))
}

// givenTemplates returns the parameters that were given as a template, in
// the order of the definition.
func (r *callRun) givenTemplates(given map[string]Text) ([]userTemplate, error) {
	byName := map[string]userTemplate{}
	for _, option := range slices.Sorted(maps.Keys(given)) {
		parameter, ok := r.call.Parameter(option)
		if !ok {
			return nil, unknownParameter(r.call, "", option)
		}
		origin := given[option].Origin
		if origin == "" {
			origin = "--" + parameter.Name + templateOptionSuffixes[0]
		}
		_, twice := byName[parameter.Name]
		switch {
		case !parameter.Template:
			return nil, apikit.Errorf(apikit.KindUsage, "%s: --%s takes no template", origin, parameter.Name)
		case r.args.has(parameter.Name):
			return nil, apikit.Errorf(apikit.KindUsage, "%s cannot be given together with --%s", origin, parameter.Name)
		case twice:
			return nil, apikit.Errorf(apikit.KindUsage, "%s: --%s has a template already", origin, parameter.Name)
		}
		byName[parameter.Name] = userTemplate{parameter: parameter, text: given[option].Value, origin: origin}
	}
	var templates []userTemplate
	for _, parameter := range r.call.Parameters {
		if template, ok := byName[parameter.Name]; ok {
			templates = append(templates, template)
		}
	}
	return templates, nil
}

// fromFile adds the parameters of a call template to the arguments, and
// those that are templates themselves to templates, which it returns.
func (r *callRun) fromFile(file *CallTemplate, templates []userTemplate, templated func(string) bool) ([]userTemplate, error) {
	call, origin := r.call, file.Origin
	if origin == "" {
		origin = optionTemplate
	}
	names := slices.Concat([]string{call.Name}, call.Aliases)
	for _, setter := range call.Setters {
		names = append(names, setter.Name)
	}
	switch {
	case file.Resource != "" && r.e.doc.ResourceName(file.Resource) != r.resource:
		return nil, apikit.Errorf(apikit.KindUsage, "%s is for %s, not %s", origin, file.Resource, r.resource)
	case file.Call != "" && !slices.Contains(names, file.Call):
		return nil, apikit.Errorf(apikit.KindUsage, "%s is for %s, not %s", origin, file.Call, call.Name)
	}

	var given []*definition.Parameter
	valueOf := map[string]any{}
	for option, value := range file.Parameters.All() {
		parameter, ok := call.Parameter(option)
		if !ok {
			return nil, unknownParameter(call, origin, option)
		}
		if _, twice := valueOf[parameter.Name]; twice {
			return nil, apikit.Errorf(apikit.KindUsage, "%s gives %s twice", origin, parameter.Name)
		}
		valueOf[parameter.Name] = value
		given = append(given, parameter)
	}

	// The command line wins: over the same parameter, and over one of a
	// group of which it gives another.
	direct := func(name string) bool { return r.args.has(name) || templated(name) }
	groups := slices.Concat(call.Exclusive, call.OneOf)
	given = slices.DeleteFunc(given, func(parameter *definition.Parameter) bool {
		return direct(parameter.Name) || slices.ContainsFunc(groups, func(group []string) bool {
			return slices.Contains(group, parameter.Name) && slices.ContainsFunc(group, direct)
		})
	})
	for _, group := range groups {
		both := slices.DeleteFunc(slices.Clone(group), func(name string) bool {
			return !slices.ContainsFunc(given, func(parameter *definition.Parameter) bool { return parameter.Name == name })
		})
		if len(both) > 1 {
			return nil, apikit.Errorf(apikit.KindUsage, "%s gives %s, which exclude each other", origin, optionList(both))
		}
	}

	for _, parameter := range given {
		value := valueOf[parameter.Name]
		if parameter.Template {
			text, ok := value.(string)
			if !ok {
				return nil, apikit.Errorf(apikit.KindUsage, "%s: %s must be a text", origin, parameter.Name)
			}
			templates = append(templates, userTemplate{parameter: parameter, text: text, origin: parameter.Name + " of " + origin})
			continue
		}
		converted, err := parameterValue(parameter, value)
		switch {
		case err != nil:
			return nil, apikit.Errorf(apikit.KindUsage, "%s: %s %w", origin, parameter.Name, err)
		case converted != nil:
			r.args.set(parameter.Name, converted)
		}
	}
	return templates, nil
}

// templateValueText returns a value as a template of a user shows it. It
// differs from [apikit.Text] for a list of plain values, which reads as
// its values with commas between them.
func templateValueText(value any) string {
	list, ok := value.([]any)
	if !ok {
		return apikit.Text(value)
	}
	texts := make([]string, len(list))
	for i, item := range list {
		switch item.(type) {
		case string, json.Number, bool:
			texts[i] = apikit.Text(item)
		default:
			return apikit.Text(value)
		}
	}
	return strings.Join(texts, ", ")
}

// missingValues returns the error for a template whose placeholders have no
// value, with a hint at how to give each one.
func (r *callRun) missingValues(origin string, missing []expr.Placeholder) error {
	// The braced ones first, then the plain ones.
	slices.SortStableFunc(missing, func(a, b expr.Placeholder) int {
		switch {
		case a.Braced == b.Braced:
			return 0
		case a.Braced:
			return -1
		}
		return 1
	})
	var raw, hints []string
	plain := false
	for _, placeholder := range missing {
		if slices.Contains(raw, placeholder.Raw) {
			continue
		}
		raw = append(raw, placeholder.Raw)
		plain = plain || !placeholder.Braced
		if hint := r.howToGive(placeholder.Name); !slices.Contains(hints, hint) {
			hints = append(hints, hint)
		}
	}
	message := fmt.Sprintf("%s uses %s without a value; %s", origin, strings.Join(raw, ", "), strings.Join(hints, ", "))
	if plain {
		message += "; $$ is a literal $"
	}
	return &apikit.Error{Kind: apikit.KindUsage, Msg: message}
}

// howToGive says how a caller gives the value of a template variable.
func (r *callRun) howToGive(variable string) string {
	head, field, dotted := strings.Cut(variable, ".")
	parameter, isParameter := variableParameter(r.call, variable)
	if named, ok := resourceParameter(r.call, head); dotted && ok {
		if r.args.has(named.Name) {
			return "the " + head + " has no " + field
		}
		return "give --" + named.Name
	}
	switch {
	case isParameter && parameter.Template:
		return "no template can use " + variable
	case variableKey(variable) == definition.VariableID && r.call.RequiresID:
		return "give the ID of the " + r.e.doc.Resource(r.resource).Singular
	case variableKey(variable) == definition.VariableID:
		return r.call.Name + " takes no ID"
	case isParameter:
		return "give --" + parameter.Name
	}
	return "give " + optionTemplateVar + " " + variable + "=VALUE"
}
