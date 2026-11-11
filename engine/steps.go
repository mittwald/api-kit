package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// stepFailed returns the error of a step that cannot do what it is for.
func stepFailed(step *definition.Step, format string, args ...any) error {
	return apikit.Errorf(apikit.KindFailed, "step %s %s", step.As, fmt.Sprintf(format, args...))
}

// stepError returns the error of a step whose request, or whose call,
// failed. A step that fails is a call that failed, whatever came of its
// request: an item the step did not find is not the one the caller asked
// for, and a mistake in the arguments of its call is none of the command
// line. The cause stays in the message.
func stepError(step *definition.Step, err error) error {
	kind := apikit.KindOf(err)
	if kind == apikit.KindNotFound || kind == apikit.KindUsage {
		kind = apikit.KindFailed
	}
	return &apikit.Error{Kind: kind, Msg: fmt.Sprintf("step %s failed: %s", step.As, causeText(err)), Err: err}
}

// causeText returns the message of an error as a part of another message.
// The body of an answer of the API may end in a line break, which would
// break the message in two where more follows.
func causeText(err error) string {
	return strings.TrimSpace(err.Error())
}

// stepsKey tells the steps of a setter apart from other steps, and from
// the same steps with other values of their step parameters.
func (r *callRun) stepsKey(setter *definition.Setter, vals values) string {
	var key strings.Builder
	fmt.Fprintf(&key, "%q", r.id)
	for _, step := range setter.Steps {
		fmt.Fprintf(&key, " %q %q %q %q %q %q %q %t %q %s", step.As, step.Get, step.Call, step.Resource,
			step.ID, step.Sub, step.Filters, step.First, step.Select, apikit.Text(step.Parameters))
	}
	for _, parameter := range setter.StepParameters {
		value, _ := vals.get(parameter.Name)
		fmt.Fprintf(&key, " %q", apikit.Text(value))
	}
	return key.String()
}

// runSteps runs the steps of a setter in order and adds to vals what each
// of them keeps, for the steps after it and the templates of the setter.
// Setters of one call that have the same steps run them once: a call that
// acts for another user asks for that once.
func (r *callRun) runSteps(ctx context.Context, setter *definition.Setter, vals values) error {
	if len(setter.Steps) == 0 {
		return nil
	}
	key := r.stepsKey(setter, vals)
	if kept, ok := r.kept[key]; ok {
		maps.Copy(vals, kept)
		return nil
	}
	// What ends up as the token must not show in an error.
	var secret []string
	for _, placeholder := range expr.ParseTemplate(setter.Token).Placeholders() {
		secret = append(secret, variableKey(placeholder.Name))
	}
	kept := values{}
	for _, step := range setter.Steps {
		var item any
		var err error
		if step.Get != "" {
			item, err = r.getStep(ctx, step, vals)
		} else {
			item, err = r.callStep(ctx, step, vals)
		}
		if err != nil {
			return err
		}
		object, _ := item.(*apikit.Object)
		value := fieldOf(object, step.Select)
		if value == nil || value == "" {
			return stepFailed(step, "found no %s", step.Select)
		}
		if slices.Contains(secret, variableKey(step.As)) {
			r.secrets = append(r.secrets, apikit.Text(value))
		}
		vals.set(step.As, value)
		kept.set(step.As, value)
	}
	r.kept[key] = kept
	return nil
}

// getStep fetches what a step names and returns the one item it picks. A
// listing is read to its end, so that the filters and the pick see every
// item.
func (r *callRun) getStep(ctx context.Context, step *definition.Step, vals values) (*apikit.Object, error) {
	query, excluded := splitFilters(step.Filters, vals.get)
	ref := Ref{Resource: step.Get, ID: renderWith(step.ID, vals.get), Sub: step.Sub}
	var found any
	var err error
	if ref.Sub != "" || ref.ID == "" {
		found, err = r.e.fetchAll(ctx, ref, query, r.e.doc.Pagination.PageSize)
	} else {
		found, err = r.e.fetch(ctx, ref, query)
	}
	if err != nil {
		return nil, stepError(step, err)
	}
	items := withoutExcluded(itemObjects(found), excluded)
	switch {
	case len(items) == 0:
		return nil, stepFailed(step, "found nothing")
	case len(items) > 1 && !step.First:
		return nil, stepFailed(step, "found %d results, expected one", len(items))
	}
	return items[0], nil
}

// callStep runs the call a step names and returns what it answered. The
// call is checked like one from the command line, and before its ID is
// resolved, so that a step that cannot run sends nothing.
func (r *callRun) callStep(ctx context.Context, step *definition.Step, vals values) (any, error) {
	call, ok := r.e.doc.Call(step.Resource, step.Call)
	if !ok || call.Shared() {
		return nil, stepFailed(step, "needs exactly one %s setter of %s", step.Call, step.Resource)
	}
	id := renderWith(step.ID, vals.get)
	switch {
	case call.RequiresID && id == "":
		return nil, stepFailed(step, "needs the ID of a %s", r.e.doc.Resource(step.Resource).Singular)
	case !call.RequiresID && id != "":
		return nil, stepFailed(step, "must not give an ID, %s takes none", step.Call)
	}
	args, err := stepArguments(step, call, vals)
	if err != nil {
		return nil, err
	}
	if err := checkArguments(call, call.Setters, args); err != nil {
		return nil, stepError(step, err)
	}

	called := r.e.newCallRun(step.Resource, call, args)
	// The secrets of the call are those of the step, whatever comes of it.
	defer func() { r.secrets = append(r.secrets, called.secrets...) }()
	if id != "" {
		if called.id, err = r.e.ResolveID(ctx, step.Resource, id); err != nil {
			return nil, stepError(step, err)
		}
	}
	if err := called.prepare(ctx, call.Setters); err != nil {
		return nil, stepError(step, err)
	}
	did, err := called.runSetter(ctx, call.Setters[0])
	if err != nil {
		return nil, stepError(step, err)
	}
	return did.body, nil
}

// stepArguments returns the arguments a call step gives its call. A step
// names a parameter by its name or an alias. A value that is one
// placeholder and nothing else passes on what the placeholder stands for
// as it is, a flag for one, and leaves the parameter out when that was not
// given.
func stepArguments(step *definition.Step, call *definition.Call, vals values) (values, error) {
	args := values{}
	for name, value := range step.Parameters.All() {
		parameter, ok := call.Parameter(name)
		if !ok {
			return nil, stepFailed(step, "gives unknown parameter %s", name)
		}
		// What a placeholder stood for is no template: a text may hold
		// ${...} as text.
		render := true
		if template, isText := value.(string); isText {
			if placeholder, sole := expr.ParseTemplate(template).Sole(); sole {
				if value, ok = vals.get(placeholder); !ok {
					continue
				}
				render = false
			}
		}
		converted, err := stepArgument(step, parameter, value, vals, render)
		if err != nil {
			return nil, err
		}
		args.set(parameter.Name, converted)
	}
	var missing []string
	for _, parameter := range call.Setters[0].Parameters {
		if parameter.Required && !args.has(parameter.Name) {
			missing = append(missing, parameter.Name)
		}
	}
	if len(missing) > 0 {
		return nil, stepFailed(step, "misses %s", optionList(missing))
	}
	return args, nil
}

// stepArgument returns the value a call step gives a parameter, as the
// type of the parameter: "true" or true for a flag, "2" or 2 for a number,
// one value or a list for a list. render fills the placeholders of a
// string in first.
func stepArgument(step *definition.Step, parameter *definition.Parameter, value any, vals values, render bool) (any, error) {
	fill := func(value any) any {
		if template, isText := value.(string); isText && render {
			return renderWith(template, vals.get)
		}
		return value
	}
	value = fill(value)
	list, isList := value.([]any)
	if isList {
		list = slices.Clone(list)
		for i, item := range list {
			list[i] = fill(item)
		}
		value = list
	}
	text := apikit.Text(value)
	_, isObject := value.(*apikit.Object)
	switch {
	case parameter.Type == definition.TypeList && isList:
		return list, nil
	case parameter.Type == definition.TypeList:
		return []any{value}, nil
	case isList:
		return nil, stepFailed(step, "gives parameter %s a list", parameter.Name)
	case isObject:
		return nil, stepFailed(step, "gives parameter %s a mapping", parameter.Name)
	}
	switch parameter.Type {
	case definition.TypeBool:
		if flag, isFlag := value.(bool); isFlag {
			return flag, nil
		}
		if strings.EqualFold(text, "true") || strings.EqualFold(text, "false") {
			return strings.EqualFold(text, "true"), nil
		}
	case definition.TypeInt, definition.TypeFloat:
		switch value.(type) {
		case string, json.Number:
			if number, ok := parameterNumber(parameter.Type, text); ok {
				return number, nil
			}
		}
	default:
		return text, nil
	}
	return nil, stepFailed(step, "gives %s parameter %s the value %s", parameter.Type, parameter.Name, text)
}

// thenSteps runs the call steps that follow the request of a setter. They
// see the ID in the answer as ${result}, and what the steps before them
// kept. By now the setter has changed something, so a step that fails says
// what was done.
func (r *callRun) thenSteps(ctx context.Context, setter *definition.Setter, vals values, resource string, answer any) error {
	object, _ := answer.(*apikit.Object)
	found, _ := r.e.idValue(resource, object)
	result := idText(found)
	if result == "" {
		return apikit.Errorf(apikit.KindFailed, "%s succeeded, but its response has no ID for the steps after it", r.call.Name)
	}
	vals = maps.Clone(vals)
	vals.set(definition.VariableResult, result)
	var earlier []string
	for _, step := range setter.Then {
		if err := r.thenStep(ctx, step, vals, earlier); err != nil {
			return &apikit.Error{
				Kind: apikit.KindOf(err),
				Msg:  fmt.Sprintf("%s succeeded with %s, but not the steps after it: %s", r.call.Name, result, causeText(err)),
				Err:  err,
			}
		}
		earlier = append(earlier, step.As)
	}
	return nil
}

// thenStep runs one of the steps after the request and keeps what it
// selects, if its answer has that. A step that needs what an earlier one
// did not keep cannot run: the placeholder would be sent as it is written.
func (r *callRun) thenStep(ctx context.Context, step *definition.Step, vals values, earlier []string) error {
	for _, name := range earlier {
		if !vals.has(name) && stepUses(step, name) {
			return stepFailed(step, "needs %s, which that step's response lacked", name)
		}
	}
	answer, err := r.callStep(ctx, step, vals)
	if err != nil {
		return err
	}
	object, _ := answer.(*apikit.Object)
	if selected := fieldOf(object, step.Select); selected != nil && selected != "" {
		vals.set(step.As, selected)
	}
	return nil
}

// stepUses reports whether a call step has a placeholder of that name.
func stepUses(step *definition.Step, name string) bool {
	var uses func(value any) bool
	uses = func(value any) bool {
		switch value := value.(type) {
		case string:
			return slices.ContainsFunc(expr.ParseTemplate(value).Placeholders(), func(placeholder expr.Placeholder) bool {
				return placeholder.Braced && variableKey(placeholder.Name) == variableKey(name)
			})
		case []any:
			return slices.ContainsFunc(value, uses)
		case *apikit.Object:
			for key, item := range value.All() {
				if uses(key) || uses(item) {
					return true
				}
			}
		}
		return false
	}
	return uses(step.ID) || uses(step.Parameters)
}
