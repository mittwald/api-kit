package engine

import (
	"context"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// The options that make a call depend on a listing, as messages name them.
const (
	optionIf     = "--if"
	optionIfNone = "--if-none"
	optionIfAny  = "--if-any"
)

// callCondition is a condition a call depends on: a test of a field of the
// item the call changes, or of an item one of its parameters names.
type callCondition struct {
	expr.Condition
	// parameter names the item to test. Nil means the item of the call.
	parameter *definition.Parameter
	// field leads to the value to test in that item.
	field expr.Path
}

// parseConditions reads the conditions of a call. A path that starts with
// a parameter naming a resource, as in owner.city, tests the item that
// parameter names; any other path tests the item the call changes, which a
// call without an ID does not have.
func (r *callRun) parseConditions(sources []string) ([]callCondition, error) {
	var conditions []callCondition
	for _, source := range sources {
		parsed, err := expr.ParseCondition(source)
		if err != nil {
			return nil, apikit.Errorf(apikit.KindUsage, "%s: %w", optionIf, err)
		}
		condition := callCondition{Condition: parsed}
		path := parsed.Path
		name, field, dotted := strings.Cut(parsed.Path, ".")
		parameter, named := resourceParameter(r.call, name)
		switch {
		case dotted && named && !r.args.has(parameter.Name):
			return nil, apikit.Errorf(apikit.KindUsage, "%s %s needs --%s", optionIf, source, parameter.Name)
		case dotted && named:
			condition.parameter, path = parameter, field
		case !r.call.RequiresID:
			tests := "nothing"
			var examples []string
			for _, parameter := range r.call.Parameters {
				if parameter.Resource != nil {
					examples = append(examples, parameter.Name+".<field>")
				}
			}
			if len(examples) > 0 {
				tests = "a parameter: " + strings.Join(examples, ", ")
			}
			return nil, apikit.Errorf(apikit.KindUsage, "%s %s: %s takes no ID, so a condition tests %s",
				optionIf, source, r.call.Name, tests)
		}
		if condition.field, err = expr.ParsePath(path); err != nil {
			return nil, apikit.Errorf(apikit.KindUsage, "%s %s: %w", optionIf, source, err)
		}
		conditions = append(conditions, condition)
	}
	return conditions, nil
}

// checkConditions fetches the items the conditions test and reports the
// first condition that does not hold as an error of kind
// [apikit.KindSkipped]: the call is not run, on purpose.
func (r *callRun) checkConditions(ctx context.Context, conditions []callCondition) error {
	for _, condition := range conditions {
		var item *apikit.Object
		var err error
		if condition.parameter == nil {
			item, err = r.e.Item(ctx, r.resource, r.id)
		} else {
			item, err = r.item(ctx, condition.parameter)
		}
		if err != nil {
			return err
		}
		actual := condition.field.Get(item, definition.ExtraKey)
		if !expr.Holds(actual, condition.Op, condition.Value) {
			shown := apikit.Text(actual)
			if shown == "" {
				shown = "empty"
			}
			return apikit.Errorf(apikit.KindSkipped, "Skipped %s: %s does not hold, %s is %s",
				r.call.Name, condition.Source, condition.Path, shown)
		}
	}
	return nil
}

// listCheck is a listing a call depends on: its items that meet all
// conditions must be none, or at least one.
type listCheck struct {
	// option is the option the check was given with.
	option string
	// resource is the resource to list.
	resource string
	// text is the check as written, for messages.
	text string
	// conditions are what an item has to meet.
	conditions []listCondition
}

// listCondition is one condition of a [listCheck].
type listCondition struct {
	expr.Condition
	// field leads to the value to test in an item.
	field expr.Path
	// reference is what the value refers to in the call: "id", a
	// parameter, or a field of the item a parameter names. Empty means the
	// value is literal.
	reference string
	// filter is set for a condition the API applies as a filter.
	filter bool
}

// filterKeys returns the filters the API takes for a resource, as the
// definition lists them, with and without those that only order a listing.
func (e *Engine) filterKeys(resource string) (all, narrowing []string) {
	for _, filter := range slices.Concat(e.doc.Filters, e.doc.Resource(resource).Filters) {
		key, _, _ := strings.Cut(filter, "=")
		key = strings.TrimSpace(key)
		if slices.Contains(all, key) {
			continue
		}
		all = append(all, key)
		if !slices.Contains(e.doc.OrderFilters, key) {
			narrowing = append(narrowing, key)
		}
	}
	return all, narrowing
}

// parseListChecks reads the listings a call depends on. Each is a resource
// followed by conditions. A value @name refers to the call: @id, a
// parameter, or a field of the item a parameter names, as in
// @owner.number; @@ is a literal @. A condition on a filter of the
// resource is sent to the API, the others are tested on the items, so one
// narrowing filter is required: without it the whole listing would be
// read.
func (r *callRun) parseListChecks(option string, lists [][]string) ([]listCheck, error) {
	var checks []listCheck
	for _, words := range lists {
		text := strings.Join(words, " ")
		fail := func(format string, args ...any) ([]listCheck, error) {
			problem := apikit.Errorf(apikit.KindUsage, format, args...)
			problem.Msg = option + " " + text + ": " + problem.Msg
			return nil, problem
		}
		if len(words) == 0 {
			return fail("expected a resource and conditions")
		}
		resource := r.e.doc.ResourceName(words[0])
		if described := r.e.doc.Resource(resource); !described.Defined() || !described.SupportsListing {
			return fail("%s is no resource that can be listed", words[0])
		}
		filters, narrowing := r.e.filterKeys(resource)
		check := listCheck{option: option, resource: resource, text: text}
		narrowed := false
		for _, word := range words[1:] {
			parsed, err := expr.ParseCondition(word)
			if err != nil {
				return fail("expected conditions like status=open, ownerId=@owner or 'createdAt>=2026-01-31', got %s", word)
			}
			condition := listCondition{Condition: parsed}
			if condition.field, err = expr.ParsePath(parsed.Path); err != nil {
				return fail("%w", err)
			}
			condition.filter = parsed.Op == expr.OpEqual && slices.Contains(filters, parsed.Path)
			narrowed = narrowed || (condition.filter && slices.Contains(narrowing, parsed.Path))

			reference, refers := strings.CutPrefix(parsed.Value, "@")
			switch {
			case strings.HasPrefix(reference, "@"):
				condition.Value = reference
			case refers:
				name, _, dotted := strings.Cut(reference, ".")
				parameter, known := variableParameter(r.call, name)
				_, named := resourceParameter(r.call, name)
				switch {
				case variableKey(reference) == definition.VariableID:
					if !r.call.RequiresID {
						return fail("@%s needs a call with an ID", reference)
					}
				case !known || (dotted && !named):
					return fail("@%s names no parameter of the call, nor a field of an item one names", reference)
				case !r.args.has(parameter.Name):
					return fail("@%s needs --%s", reference, parameter.Name)
				}
				condition.reference = reference
			case condition.filter && parsed.Value == "":
				// A shell variable that is not set, say.
				return fail("filter %s has no value", parsed.Path)
			}
			check.conditions = append(check.conditions, condition)
		}
		if !narrowed {
			slices.Sort(narrowing)
			for i, key := range narrowing {
				narrowing[i] = key + "="
			}
			return fail("needs one filter of %s, e.g. %s", resource, strings.Join(narrowing, ", "))
		}
		checks = append(checks, check)
	}
	return checks, nil
}

// referenced returns the value a reference of a list check stands for: the
// ID of the call, a parameter as it was resolved, or a field of the item a
// parameter names.
func (r *callRun) referenced(ctx context.Context, reference string) (string, error) {
	if variableKey(reference) == definition.VariableID {
		return r.id, nil
	}
	name, field, dotted := strings.Cut(reference, ".")
	var value any
	if parameter, known := variableParameter(r.call, name); known {
		value, _ = r.args.get(parameter.Name)
	}
	if parameter, named := resourceParameter(r.call, name); dotted && named {
		item, err := r.item(ctx, parameter)
		if err != nil {
			return "", err
		}
		value = nil
		if path, err := expr.ParsePath(field); err == nil {
			value = path.Get(item, definition.ExtraKey)
		}
	}
	text := apikit.Text(value)
	if text == "" {
		return "", apikit.Errorf(apikit.KindFailed, "@%s has no value", reference)
	}
	return text, nil
}

// checkLists reads the listings a call depends on and reports the first
// that is not as required as an error of kind [apikit.KindSkipped]. A
// listing that cannot be read is an error, never an empty listing.
func (r *callRun) checkLists(ctx context.Context, checks []listCheck) error {
	for _, check := range checks {
		var query []Filter
		var tests []listCondition
		for _, condition := range check.conditions {
			if condition.reference != "" {
				value, err := r.referenced(ctx, condition.reference)
				if err != nil {
					return err
				}
				condition.Value = value
			}
			if condition.filter {
				query = append(query, Filter{Key: condition.Path, Value: condition.Value})
			} else {
				tests = append(tests, condition)
			}
		}
		query, err := r.e.filterIDs(ctx, query)
		if err != nil {
			return err
		}
		listing, err := r.e.getAll(ctx, Ref{Resource: check.resource}, query, r.e.doc.Pagination.PageSize)
		if err != nil {
			return err
		}
		var found []string
		for _, item := range itemObjects(listing) {
			if !slices.ContainsFunc(tests, func(test listCondition) bool {
				return !expr.Holds(test.field.Get(item, definition.ExtraKey), test.Op, test.Value)
			}) {
				found = append(found, r.e.shownID(check.resource, item))
			}
		}
		switch {
		case check.option == optionIfNone && len(found) > 0:
			return apikit.Errorf(apikit.KindSkipped, "Skipped %s: %s %s found %s",
				r.call.Name, check.option, check.text, strings.Join(found, ", "))
		case check.option == optionIfAny && len(found) == 0:
			return apikit.Errorf(apikit.KindSkipped, "Skipped %s: %s %s found nothing", r.call.Name, check.option, check.text)
		}
	}
	return nil
}

// shownID returns what names an item in a message: the first of the fields
// the definition lists for display that the item has a value for, such as
// a short ID, else its ID.
func (e *Engine) shownID(resource string, item *apikit.Object) string {
	for _, field := range e.doc.Identifiers.Display {
		if value, _ := item.Get(field); !apikit.Falsy(value) {
			return apikit.Text(value)
		}
	}
	id, _ := e.IDOf(resource, item)
	return id
}
