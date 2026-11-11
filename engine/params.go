package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// parameterNames returns the names of the parameters and step parameters of
// a call, for a message that lists what the call takes.
func parameterNames(call *definition.Call) []string {
	var names []string
	for _, parameter := range slices.Concat(call.Parameters, call.StepParameters) {
		names = append(names, parameter.Name)
	}
	return names
}

// unknownParameter returns the error for an option a call does not have.
// origin says where the option was given and may be empty.
func unknownParameter(call *definition.Call, origin, option string) error {
	if origin != "" {
		origin += ": "
	}
	return apikit.Errorf(apikit.KindUsage, "%s%s has no parameter %s; it has %s",
		origin, call.Name, option, strings.Join(parameterNames(call), ", "))
}

// variableParameter returns the parameter or step parameter a template
// variable names, by its name or an alias.
func variableParameter(call *definition.Call, variable string) (*definition.Parameter, bool) {
	for _, parameter := range slices.Concat(call.Parameters, call.StepParameters) {
		for _, option := range parameter.OptionNames() {
			if variableKey(option) == variableKey(variable) {
				return parameter, true
			}
		}
	}
	return nil, false
}

// resourceParameter returns the parameter of that name if its value names
// an item of a resource.
func resourceParameter(call *definition.Call, name string) (*definition.Parameter, bool) {
	for _, parameter := range call.Parameters {
		if parameter.Resource != nil && variableKey(parameter.Name) == variableKey(name) {
			return parameter, true
		}
	}
	return nil, false
}

// parameterResource returns the resource whose items a parameter names,
// which may depend on the value of another parameter. It is empty for a
// parameter that names none.
func parameterResource(parameter *definition.Parameter, args values) string {
	named := parameter.Resource
	switch {
	case named == nil:
		return ""
	case named.By == "":
		return named.Name
	}
	selector, _ := args.get(named.By)
	for _, pair := range named.Map {
		if pair.Key == apikit.Text(selector) {
			return pair.Value
		}
	}
	return ""
}

// givenParameters files the parameters a caller gave under their names, as
// the values the call works with.
func givenParameters(call *definition.Call, params map[string]any) (values, error) {
	args := values{}
	for _, option := range slices.Sorted(maps.Keys(params)) {
		parameter, ok := call.Parameter(option)
		if !ok {
			return nil, unknownParameter(call, "", option)
		}
		if params[option] == nil {
			continue
		}
		// A NaN, which JSON cannot hold, is no value of any type.
		value, err := apikit.Normalize(params[option])
		if err != nil {
			err = fmt.Errorf("must be of type %s", parameter.Type)
		} else {
			value, err = parameterValue(parameter, value)
		}
		switch {
		case err != nil:
			return nil, apikit.Errorf(apikit.KindUsage, "--%s %w", option, err)
		case args.has(parameter.Name):
			return nil, apikit.Errorf(apikit.KindUsage, "--%s is given more than once", parameter.Name)
		case value != nil:
			args.set(parameter.Name, value)
		}
	}
	return args, nil
}

// parameterValue returns what a caller or a call template gives a parameter
// as the value of its type, the way a command line reads it: a number may
// be written as text, one value is a list of one. A flag that is false was
// not given, which is nil.
func parameterValue(parameter *definition.Parameter, value any) (any, error) {
	if parameter.Type == definition.TypeBool {
		flag, ok := value.(bool)
		switch {
		case !ok:
			return nil, errors.New("must be true or false")
		case !flag:
			return nil, nil
		}
		return true, nil
	}

	mistyped := fmt.Errorf("must be of type %s", parameter.Type)
	items, isList := value.([]any)
	if parameter.Type == definition.TypeList {
		mistyped = errors.New("must be a value or a list of values")
	}
	if parameter.Type != definition.TypeList || !isList {
		items = []any{value}
	}
	if len(items) == 0 {
		return nil, mistyped
	}
	texts := make([]any, len(items))
	for i, item := range items {
		switch item.(type) {
		case string, json.Number:
			texts[i] = apikit.Text(item)
		default:
			return nil, mistyped
		}
	}

	text := apikit.Text(texts[0])
	switch parameter.Type {
	case definition.TypeList:
		return texts, nil
	case definition.TypeInt, definition.TypeFloat:
		number, ok := parameterNumber(parameter.Type, text)
		if !ok {
			return nil, mistyped
		}
		return number, nil
	}
	return text, nil
}

// parameterNumber reads text as the value of a parameter of type int or
// float: a whole number that fits into 64 bits, or a number JSON can hold,
// which a NaN and an infinity are not.
func parameterNumber(kind definition.ParamType, text string) (json.Number, bool) {
	text = strings.TrimSpace(text)
	if kind == definition.TypeInt {
		whole, err := strconv.ParseInt(text, 10, 64)
		return json.Number(strconv.FormatInt(whole, 10)), err == nil
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return "", false
	}
	return json.Number(strconv.FormatFloat(number, 'g', -1, 64)), true
}

// parameterChoices returns the values a parameter takes, given the other
// parameters. Before the parameter they depend on is given, every value is
// possible.
func parameterChoices(parameter *definition.Parameter, args values) []string {
	if len(parameter.ChoicesBy) == 0 {
		return parameter.Choices
	}
	var all []string
	for _, choices := range parameter.ChoicesBy {
		if args.has(choices.Parameter) {
			return choices.Values
		}
		for _, value := range choices.Values {
			if !slices.Contains(all, value) {
				all = append(all, value)
			}
		}
	}
	return all
}

// selectSetters returns the setters of a call that run. A call of one
// setter runs it. A shared call runs the setters of which a parameter was
// given; such a setter needs all its required parameters, and a step
// parameter needs a setter that uses it.
func selectSetters(call *definition.Call, args values) ([]*definition.Setter, error) {
	if !call.Shared() {
		return call.Setters, nil
	}
	var selected []*definition.Setter
	var all []string
	for _, setter := range call.Setters {
		var given, missing []string
		for _, parameter := range setter.Parameters {
			all = append(all, parameter.Name)
			switch {
			case args.has(parameter.Name):
				given = append(given, parameter.Name)
			case parameter.Required:
				missing = append(missing, parameter.Name)
			}
		}
		switch {
		case len(given) == 0:
			continue
		case len(missing) > 0:
			return nil, apikit.Errorf(apikit.KindUsage, "%s also needs %s", optionList(given), optionList(missing))
		}
		selected = append(selected, setter)
	}
	if len(selected) == 0 {
		return nil, apikit.Errorf(apikit.KindUsage, "%s needs at least one of %s", call.Name, optionList(all))
	}

	// A step parameter does nothing without a setter that uses it.
	for _, stepParameter := range call.StepParameters {
		if !args.has(stepParameter.Name) {
			continue
		}
		used := false
		var users []string
		for _, setter := range call.Setters {
			if !slices.ContainsFunc(setter.StepParameters, func(own *definition.Parameter) bool {
				return own.Name == stepParameter.Name
			}) {
				continue
			}
			used = used || slices.Contains(selected, setter)
			for _, parameter := range setter.Parameters {
				users = append(users, parameter.Name)
			}
		}
		if !used {
			return nil, apikit.Errorf(apikit.KindUsage, "--%s needs one of %s", stepParameter.Name, optionList(users))
		}
	}
	return selected, nil
}

// checkArguments reports the first rule of a call that its arguments break:
// a group of parameters that exclude each other, a group of which one must
// be given, the choices of a parameter, or the length of its value. The
// groups of every setter are checked, since they may name parameters of
// the other setters of the call; only a setter that runs needs one of each
// of its groups.
func checkArguments(call *definition.Call, selected []*definition.Setter, args values) error {
	given := func(group []string) []string {
		return slices.DeleteFunc(slices.Clone(group), func(name string) bool { return !args.has(name) })
	}
	for _, setter := range call.Setters {
		runs := slices.Contains(selected, setter)
		for _, group := range setter.Exclusive {
			if names := given(group); len(names) > 1 {
				return apikit.Errorf(apikit.KindUsage, "%s takes only one of %s", call.Name, optionList(names))
			}
		}
		for _, group := range setter.OneOf {
			if count := len(given(group)); count > 1 || (runs && count != 1) {
				return apikit.Errorf(apikit.KindUsage, "%s needs exactly one of %s", call.Name, optionList(group))
			}
		}
		if !runs {
			continue
		}
		for _, parameter := range setter.Parameters {
			value, ok := args.get(parameter.Name)
			if !ok {
				continue
			}
			text := apikit.Text(value)
			if choices := parameterChoices(parameter, args); len(choices) > 0 && !slices.Contains(choices, text) {
				return apikit.Errorf(apikit.KindUsage, "--%s %s is not possible here, choose from %s",
					parameter.Name, text, strings.Join(choices, ", "))
			}
			length := utf8.RuneCountInString(text)
			if _, isText := value.(string); isText && parameter.MaxLength > 0 && length > parameter.MaxLength {
				return apikit.Errorf(apikit.KindUsage, "--%s has %d characters, at most %d are possible",
					parameter.Name, length, parameter.MaxLength)
			}
		}
	}
	return nil
}

// prepare turns the arguments of a call into what its setters work with.
// Values that name an item become its ID first; then the parameters set
// their variables, and the lookups, which may use both, are resolved.
func (r *callRun) prepare(ctx context.Context, setters []*definition.Setter) error {
	if err := r.resolveIDs(ctx, setters); err != nil {
		return err
	}
	r.setVariables(setters)
	return r.resolveLookups(ctx, setters)
}

// resolveIDs replaces the value of every parameter that names an item of a
// resource by the ID of that item, so that a parameter takes whatever the
// ID of a call takes: an alias or a name.
func (r *callRun) resolveIDs(ctx context.Context, setters []*definition.Setter) error {
	asGiven := maps.Clone(r.args)
	for _, setter := range setters {
		for _, parameter := range setter.Parameters {
			value, _ := asGiven.get(parameter.Name)
			text, _ := value.(string)
			resource := parameterResource(parameter, asGiven)
			if resource == "" || text == "" {
				continue
			}
			id, err := r.e.ResolveID(ctx, resource, text)
			if err != nil {
				return err
			}
			r.args.set(parameter.Name, id)
		}
	}
	return nil
}

// setVariables adds the variables of every parameter that was given. The
// variables of one parameter see the arguments as they were before any of
// them was set.
func (r *callRun) setVariables(setters []*definition.Setter) {
	for _, setter := range setters {
		for _, parameter := range setter.Parameters {
			if !r.args.has(parameter.Name) {
				continue
			}
			rendered := make([]string, len(parameter.Variables))
			for i, variable := range parameter.Variables {
				rendered[i] = renderWith(variable.Value, r.args.scalar)
			}
			for i, variable := range parameter.Variables {
				r.args.set(variable.Key, rendered[i])
			}
		}
	}
}

// item fetches the item a parameter names. The value may be an alias still
// or the ID it was resolved to.
func (r *callRun) item(ctx context.Context, parameter *definition.Parameter) (*apikit.Object, error) {
	resource := parameterResource(parameter, r.args)
	value, _ := r.args.get(parameter.Name)
	if resource == "" || apikit.Text(value) == "" {
		return nil, apikit.Errorf(apikit.KindUsage, "--%s names no item of a resource here", parameter.Name)
	}
	id, err := r.e.ResolveID(ctx, resource, apikit.Text(value))
	if err != nil {
		return nil, err
	}
	return r.e.Item(ctx, resource, id)
}

// resolved lists what a dry run shows: the ID and every parameter of the
// setters that would run, as it would be used and as it was given.
func (r *callRun) resolved(selected []*definition.Setter, givenID string, asGiven values) []ResolvedParam {
	params := []ResolvedParam{}
	if r.id != "" {
		params = append(params, ResolvedParam{Name: definition.VariableID, Value: r.id, Given: givenID})
	}
	listed := map[string]bool{}
	for _, setter := range selected {
		for _, parameter := range slices.Concat(setter.Parameters, setter.StepParameters) {
			value, ok := r.args.get(parameter.Name)
			if !ok || listed[parameter.Name] {
				continue
			}
			listed[parameter.Name] = true
			given, _ := asGiven.get(parameter.Name)
			params = append(params, ResolvedParam{Name: parameter.Name, Value: value, Given: given})
		}
	}
	return params
}
