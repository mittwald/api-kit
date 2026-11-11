package definition

import "slices"

// Call is what a caller runs on a resource: all setters that share a name,
// or the part of them that an alias names. A call of several setters sends
// one request for each setter whose parameters were given.
type Call struct {
	// Resource is the resource the call belongs to.
	Resource string
	// Name is the name the call was looked up by: the name of the setters,
	// or an alias.
	Name string
	// Aliases are the aliases of the setters, without duplicates.
	Aliases []string
	// RequiresID is false for a call that works without the ID of an
	// existing item.
	RequiresID bool
	// Setters are the setters the call runs, in document order.
	Setters []*Setter
	// Parameters are the parameters of all setters, in document order.
	Parameters []*Parameter
	// StepParameters are the step parameters of all setters. Setters may
	// define one alike; it is listed once.
	StepParameters []*Parameter
	// Exclusive are the groups of parameters of which at most one may be
	// given, of all setters.
	Exclusive [][]string
	// OneOf are the groups of parameters of which exactly one must be
	// given, of all setters.
	OneOf [][]string
}

// Shared reports whether the call has several setters.
func (c *Call) Shared() bool { return len(c.Setters) > 1 }

// Parameter returns the parameter or step parameter with that name or
// alias.
func (c *Call) Parameter(option string) (*Parameter, bool) {
	for _, parameter := range slices.Concat(c.Parameters, c.StepParameters) {
		if slices.Contains(parameter.OptionNames(), option) {
			return parameter, true
		}
	}
	return nil, false
}

// TemplateParameters returns the names of the parameters whose value may
// be given as a template.
func (c *Call) TemplateParameters() []string {
	var names []string
	for _, parameter := range c.Parameters {
		if parameter.Template {
			names = append(names, parameter.Name)
		}
	}
	return names
}

// Calls returns the calls of a resource, one per setter name, in the order
// the names first appear.
func (d *Document) Calls(resource string) []*Call {
	var names []string
	for _, setter := range d.Resource(resource).Setters {
		if !slices.Contains(names, setter.Name) {
			names = append(names, setter.Name)
		}
	}
	calls := make([]*Call, len(names))
	for i, name := range names {
		calls[i], _ = d.Call(resource, name)
	}
	return calls
}

// Call returns the call of a resource with that name. An alias yields the
// call with only the setters that have the alias; setters of one name may
// share an alias, which then runs that part of their call.
func (d *Document) Call(resource, name string) (*Call, bool) {
	all := d.Resource(resource).Setters
	var setters []*Setter
	for _, setter := range all {
		if slices.Contains(setter.Aliases, name) {
			setters = append(setters, setter)
		}
	}
	if len(setters) == 0 {
		for _, setter := range all {
			if setter.Name == name {
				setters = append(setters, setter)
			}
		}
	}
	if len(setters) == 0 {
		return nil, false
	}

	call := &Call{Resource: resource, Name: name, RequiresID: setters[0].RequiresID, Setters: setters}
	for _, setter := range setters {
		for _, alias := range setter.Aliases {
			if !slices.Contains(call.Aliases, alias) {
				call.Aliases = append(call.Aliases, alias)
			}
		}
		call.Parameters = append(call.Parameters, setter.Parameters...)
		for _, parameter := range setter.StepParameters {
			if _, known := call.Parameter(parameter.Name); !known {
				call.StepParameters = append(call.StepParameters, parameter)
			}
		}
		call.Exclusive = append(call.Exclusive, setter.Exclusive...)
		call.OneOf = append(call.OneOf, setter.OneOf...)
	}
	return call, true
}

// CallUse names a call and the resources that have it.
type CallUse struct {
	// Name is the name of the call.
	Name string
	// Resources are the resources with a call of that name, in document
	// order.
	Resources []string
}

// CallNames returns the names of all calls with the resources that have
// them, in the order the names first appear. Aliases are not listed.
func (d *Document) CallNames() []CallUse {
	var uses []CallUse
	for _, resource := range d.resources {
		for _, call := range d.Calls(resource.Name) {
			index := slices.IndexFunc(uses, func(use CallUse) bool { return use.Name == call.Name })
			if index < 0 {
				uses = append(uses, CallUse{Name: call.Name})
				index = len(uses) - 1
			}
			uses[index].Resources = append(uses[index].Resources, resource.Name)
		}
	}
	return uses
}

// SubResources returns the resources that belong to items of a parent, in
// document order.
func (d *Document) SubResources(parent string) []*Resource {
	var subs []*Resource
	for _, resource := range d.resources {
		if resource.Parent == parent {
			subs = append(subs, resource)
		}
	}
	return subs
}
