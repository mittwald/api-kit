package engine

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// CallInput is everything a caller gives for one call.
type CallInput struct {
	// Resource is the resource the call belongs to.
	Resource string
	// ID is the ID of the item the call changes, or another value that
	// names it, such as an alias. It is empty for a call that takes none.
	ID string
	// Call is the name of the call, or an alias of it.
	Call string
	// Params are the parameters that were given, by name or alias, and
	// only those. The value of a parameter of type str is a string, of
	// type int an int64, of type float a float64, of type list a []string,
	// and of type bool true: a flag is given or not, so false counts as
	// not given.
	Params map[string]any
	// Templates are the parameters whose value was given as a template,
	// by name or alias. Only a parameter that the definition lets take a
	// template can be given so, and not as a value in Params as well.
	Templates map[string]Text
	// File is a call template that gives further parameters, from
	// [Engine.LoadCallTemplate]. What is given in Params and Templates
	// wins over it.
	File *CallTemplate
	// Vars are the template variables, in the order they were given.
	Vars []Var
	// If are conditions that must hold for the call to run, each on a
	// field of the item the call changes, as in "status!=closed", or of an
	// item a parameter names, as in "owner.city=Berlin".
	If []string
	// IfNone are listings that must have no item for the call to run.
	// Each is the name of a resource followed by conditions on its items.
	IfNone [][]string
	// IfAny are listings that must have at least one item for the call to
	// run, written like those of IfNone.
	IfAny [][]string
	// DryRun checks the call and resolves its parameters, and stops before
	// the first step or request that changes something.
	DryRun bool
}

// ResolvedParam is one parameter of a dry run: what would be used, and
// what was given for it. Both are values of the types [apikit.Decode]
// produces.
type ResolvedParam struct {
	// Name is the name of the parameter, or "id" for the ID of the call.
	Name string
	// Value is what the call would use: the ID a name was resolved to, the
	// text a template was rendered to.
	Value any
	// Given is the value as it was given. It equals Value where nothing
	// was resolved.
	Given any
}

// CallResult is what a call did.
type CallResult struct {
	// Resource is the resource whose definition describes Body: the one
	// the setter names for its response, else the resource of the call.
	Resource string
	// Body is the response of the API, when one setter ran and it answered
	// with something to show.
	Body any
	// Messages holds one line for every request that was sent and whose
	// response is not in Body, saying what it did.
	Messages []string
	// DryRun is set in place of everything else for a dry run: the ID and
	// the parameters of the call, in the order of the definition. It is
	// empty, but not nil, for a call that takes neither.
	DryRun []ResolvedParam
}

// callRun is one call under way.
type callRun struct {
	e *Engine
	// resource is the resource the call belongs to.
	resource string
	call     *definition.Call
	// id is the ID of the item the call changes, resolved. It is empty for
	// a call that takes none.
	id string
	// args are the arguments of the call: its parameters, and the
	// variables they set.
	args values
	// kept is what the steps that ran kept, by [callRun.stepsKey].
	kept map[string]values
	// payloads are the payload templates written as JSON text, read.
	payloads map[*definition.Setter]any
	// secrets are the tokens that steps supplied, and what they were made
	// from. They must not show in an error.
	secrets []string
}

// newCallRun returns the state of a call that has the given arguments.
func (e *Engine) newCallRun(resource string, call *definition.Call, args values) *callRun {
	return &callRun{
		e:        e,
		resource: resource,
		call:     call,
		args:     args,
		kept:     map[string]values{},
		payloads: map[*definition.Setter]any{},
	}
}

// Apply runs a call: one or more requests that change a resource.
//
// The call is checked as a whole before anything is sent: its parameters,
// the templates that fill them in, the ID and the names that parameters
// give in place of IDs, the conditions, and the payload of every request.
// What the caller got wrong is an error of kind [apikit.KindUsage], an ID
// or a name that leads to no item one of kind [apikit.KindNotFound], and a
// condition that does not hold one of kind [apikit.KindSkipped], whose
// message says what was skipped and why.
//
// A call of several setters sends one request for each setter of which a
// parameter was given, and stops at the first that fails. The result then
// comes back with the error and tells what was done before. A request the
// API refuses, and a step that fails, is an error of kind
// [apikit.KindFailed], with status 404 as with any other: what the caller
// named was found before.
func (e *Engine) Apply(ctx context.Context, in CallInput) (*CallResult, error) {
	call, ok := e.doc.Call(in.Resource, in.Call)
	if !ok {
		return nil, apikit.Errorf(apikit.KindUsage, "%s is not available for %s", in.Call, in.Resource)
	}
	args, err := givenParameters(call, in.Params)
	if err != nil {
		return nil, err
	}
	r := e.newCallRun(in.Resource, call, args)
	result, err := r.apply(ctx, in)
	return result, r.withoutSecrets(err)
}

// apply runs the call of r. The order matters: every check comes before
// the first request it can do without.
func (r *callRun) apply(ctx context.Context, in CallInput) (*CallResult, error) {
	call := r.call
	if err := r.fill(ctx, in); err != nil {
		return nil, err
	}
	conditions, err := r.parseConditions(in.If)
	if err != nil {
		return nil, err
	}
	checks, err := r.parseListChecks(optionIfNone, in.IfNone)
	if err != nil {
		return nil, err
	}
	more, err := r.parseListChecks(optionIfAny, in.IfAny)
	if err != nil {
		return nil, err
	}
	checks = append(checks, more...)

	switch {
	case call.RequiresID && in.ID == "":
		return nil, apikit.Errorf(apikit.KindUsage, "%s needs the ID of a %s", call.Name, r.e.doc.Resource(r.resource).Singular)
	case !call.RequiresID && in.ID != "":
		return nil, apikit.Errorf(apikit.KindUsage, "%s takes no ID, got %s", call.Name, in.ID)
	}
	selected, err := selectSetters(call, r.args)
	if err != nil {
		return nil, err
	}
	if err := checkArguments(call, selected, r.args); err != nil {
		return nil, err
	}

	asGiven := maps.Clone(r.args)
	if in.ID != "" {
		if r.id, err = r.e.ResolveID(ctx, r.resource, in.ID); err != nil {
			return nil, err
		}
	}
	if err := r.prepare(ctx, selected); err != nil {
		return nil, err
	}
	if err := r.checkConditions(ctx, conditions); err != nil {
		return nil, err
	}
	if err := r.checkLists(ctx, checks); err != nil {
		return nil, err
	}

	// A call of several requests sends none unless each has a payload, so
	// that no part of it can fail for that after another changed the item.
	if in.DryRun || len(selected) > 1 {
		for _, setter := range selected {
			if err := r.checkPayload(setter, r.setterValues(setter)); err != nil {
				return nil, err
			}
		}
	}
	result := &CallResult{Resource: r.resource}
	if in.DryRun {
		result.DryRun = r.resolved(selected, in.ID, asGiven)
		return result, nil
	}
	for i, setter := range selected {
		did, err := r.runSetter(ctx, setter)
		if err != nil {
			return result, r.skipping(err, selected[i+1:])
		}
		result.Resource = did.resource
		// An answer that says nothing, such as an empty object, is not
		// shown; the message is.
		if len(selected) == 1 && !apikit.Falsy(did.body) {
			result.Body = did.body
		} else {
			result.Messages = append(result.Messages, did.message)
		}
	}
	return result, nil
}

// skipping adds to the error of a setter the parameters of the setters
// after it, which were given and are not sent now.
func (r *callRun) skipping(err error, rest []*definition.Setter) error {
	var skipped []string
	for _, setter := range rest {
		for _, parameter := range setter.Parameters {
			if r.args.has(parameter.Name) {
				skipped = append(skipped, parameter.Name)
			}
		}
	}
	if len(skipped) == 0 {
		return err
	}
	return &apikit.Error{
		Kind: apikit.KindOf(err),
		Msg:  fmt.Sprintf("%s; skipped %s after that", causeText(err), optionList(skipped)),
		Err:  err,
	}
}

// withoutSecrets returns err with the tokens of the steps blotted out of
// its message, should the API or a step have repeated one.
func (r *callRun) withoutSecrets(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range r.secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "***")
		}
	}
	if message == err.Error() {
		return err
	}
	// The error is not wrapped: what it wraps holds the token still.
	return &apikit.Error{Kind: apikit.KindOf(err), Msg: message}
}

// setterValues returns the values the templates of a setter see before its
// steps ran: the arguments of the call, the ID, and the default of every
// step parameter that was not given.
func (r *callRun) setterValues(setter *definition.Setter) values {
	vals := maps.Clone(r.args)
	if r.id != "" {
		vals.set(definition.VariableID, r.id)
	}
	for _, parameter := range setter.StepParameters {
		if !vals.has(parameter.Name) {
			vals.set(parameter.Name, parameter.Default)
		}
	}
	return vals
}

// sent is what the request of a setter came to.
type sent struct {
	// resource is the resource whose definition describes body.
	resource string
	// body is the response of the API.
	body any
	// message says what the request did.
	message string
}

// runSetter sends the request of one setter, after its steps, and runs the
// steps that follow it.
func (r *callRun) runSetter(ctx context.Context, setter *definition.Setter) (sent, error) {
	vals := r.setterValues(setter)
	// Before the steps, which may change something themselves.
	if err := r.checkPayload(setter, vals); err != nil {
		return sent{}, err
	}
	if err := r.runSteps(ctx, setter, vals); err != nil {
		return sent{}, err
	}
	token := renderWith(setter.Token, vals.scalar)
	if token != "" {
		r.secrets = append(r.secrets, token)
	}
	target, err := r.target(setter, vals)
	if err != nil {
		return sent{}, err
	}
	body, used, err := r.payload(setter, vals)
	if err != nil {
		return sent{}, err
	}

	answer, err := r.e.request(ctx, call{
		method: setter.Method, url: target, body: body, token: token,
		verb: "apply", resource: r.resource, id: r.id,
	})
	if err != nil {
		// What a call changes was found before; a 404 now is a refusal.
		if apikit.KindOf(err) == apikit.KindNotFound {
			err = &apikit.Error{Kind: apikit.KindFailed, Msg: err.Error(), Err: err}
		}
		return sent{}, err
	}
	did := sent{resource: setter.Resource, body: answer, message: r.message(setter, vals, used, body)}
	if did.resource == "" {
		did.resource = r.resource
	}
	if len(setter.Then) > 0 {
		if err := r.thenSteps(ctx, setter, vals, did.resource, answer); err != nil {
			return sent{}, err
		}
	}
	return did, nil
}

// target returns the URL of the request of a setter: the path of its
// template below the base URL of its API, or the item of the call. A value
// that a template puts into the path is escaped as one segment, so that
// it cannot lead to another path or add to the query.
func (r *callRun) target(setter *definition.Setter, vals values) (string, error) {
	ref := Ref{Resource: r.resource, ID: r.id}
	if setter.PathTemplate == "" {
		return r.e.url(setter.API, ref, nil)
	}
	base, err := r.e.base(setter.API, ref)
	if err != nil {
		return "", err
	}
	segment := func(value any) string {
		escaped := url.PathEscape(apikit.Text(value))
		if escaped == "." || escaped == ".." {
			// Dots alone are a path of their own, also when escaped by
			// the rules.
			escaped = strings.ReplaceAll(escaped, ".", "%2E")
		}
		return escaped
	}
	path, _ := expr.ParseTemplate(setter.PathTemplate).Render(vals.scalar, expr.RenderOptions{Format: segment})
	return base + "/" + path, nil
}

// message returns the line that says what the request of a setter did:
// its message template, else a line that names the payload and the item.
// The template sees the ID, the strings and numbers among the values of
// the setter, whatever the payload asked for, and as ${changes} the
// fields of a payload that is an object.
func (r *callRun) message(setter *definition.Setter, vals, used values, body any) string {
	if setter.Message == "" {
		var message strings.Builder
		message.WriteString("Successfully applied ")
		if !apikit.Falsy(body) {
			message.WriteString("'" + apikit.Text(body) + "' to ")
		}
		message.WriteString(r.e.doc.Resource(r.resource).Singular)
		if r.id != "" {
			message.WriteString(" " + r.id)
		}
		return message.String()
	}
	return renderWith(setter.Message, func(name string) (any, bool) {
		payload, isObject := body.(*apikit.Object)
		if variableKey(name) == definition.VariableChanges && isObject {
			var changes []string
			for key, value := range payload.All() {
				changes = append(changes, key+"="+apikit.Text(value))
			}
			return strings.Join(changes, ", "), true
		}
		if value, ok := used.get(name); ok {
			return value, true
		}
		return vals.scalar(name)
	})
}
