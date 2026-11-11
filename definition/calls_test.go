package definition_test

import (
	"slices"
	"testing"
)

// clinic has calls of one and of several setters, with aliases.
const clinic = environments + `
resources:
  owners:
    setters:
      - {name: rename, parameters: [{name: name}]}
  visits: {parent: pets}
  notes: {parent: pets}
  bills: {parent: owners}
  pets:
    setters:
      - name: update
        aliases: [diet, feed]
        parameters:
          - {name: food, aliases: [meal], required: false}
          - {name: treats, type: int, required: false}
        stepParameters: [{name: role, aliases: [as], default: keeper}]
        exclusiveParameters: [[food, toy]]
      - name: rename
        requiresId: false
        parameters: [{name: name, template: true}]
      - name: update
        aliases: [feed, play]
        parameters:
          - {name: toy, required: false}
          - {name: note, required: false, template: true}
          - {name: memo, required: false}
        stepParameters:
          - {name: role, aliases: [as], default: keeper}
          - {name: tries, type: int, default: 2}
        oneOfParameters: [[note, memo]]
`

func TestCallsGroupSettersByName(t *testing.T) {
	doc := mustLoad(t, clinic)

	calls := doc.Calls("pets")
	if len(calls) != 2 || calls[0].Name != "update" || calls[1].Name != "rename" {
		t.Fatalf("Calls(pets) = %+v, want update and rename, in the order they first appear", calls)
	}
	update, rename := calls[0], calls[1]

	if !update.Shared() || len(update.Setters) != 2 || update.Resource != "pets" || !update.RequiresID {
		t.Errorf("call update = %+v", update)
	}
	var parameters []string
	for _, parameter := range update.Parameters {
		parameters = append(parameters, parameter.Name)
	}
	if !slices.Equal(parameters, []string{"food", "treats", "toy", "note", "memo"}) {
		t.Errorf("parameters of update = %v", parameters)
	}
	// A step parameter that both setters define is one option of the call.
	var steps []string
	for _, parameter := range update.StepParameters {
		steps = append(steps, parameter.Name)
	}
	if !slices.Equal(steps, []string{"role", "tries"}) {
		t.Errorf("step parameters of update = %v", steps)
	}
	if !slices.Equal(update.Aliases, []string{"diet", "feed", "play"}) {
		t.Errorf("aliases of update = %v", update.Aliases)
	}
	if len(update.Exclusive) != 1 || len(update.OneOf) != 1 || !slices.Equal(update.OneOf[0], []string{"note", "memo"}) {
		t.Errorf("groups of update = %v, %v", update.Exclusive, update.OneOf)
	}
	if got := update.TemplateParameters(); !slices.Equal(got, []string{"note"}) {
		t.Errorf("TemplateParameters() = %v", got)
	}

	if rename.Shared() || rename.RequiresID || len(rename.Aliases) != 0 {
		t.Errorf("call rename = %+v", rename)
	}
	if len(doc.Calls("visits")) != 0 || len(doc.Calls("unknown")) != 0 {
		t.Error("a resource without setters has calls")
	}
}

func TestCallByAliasRunsPartOfTheCall(t *testing.T) {
	doc := mustLoad(t, clinic)
	tests := map[string][]string{ // call name: first parameter of each setter it runs
		"update": {"food", "toy"},
		"feed":   {"food", "toy"},
		"diet":   {"food"},
		"play":   {"toy"},
		"rename": {"name"},
	}
	for name, want := range tests {
		call, ok := doc.Call("pets", name)
		if !ok {
			t.Errorf("Call(pets, %s) not found", name)
			continue
		}
		var got []string
		for _, setter := range call.Setters {
			got = append(got, setter.Parameters[0].Name)
		}
		if !slices.Equal(got, want) || call.Name != name {
			t.Errorf("Call(pets, %s) runs %v under the name %q, want %v", name, got, call.Name, want)
		}
	}
	for _, name := range []string{"missing", "", "food"} {
		if call, ok := doc.Call("pets", name); ok {
			t.Errorf("Call(pets, %q) = %+v, want none", name, call)
		}
	}
	if _, ok := doc.Call("unknown", "update"); ok {
		t.Error("a resource the definition does not describe has a call")
	}
}

func TestCallParameterByNameOrAlias(t *testing.T) {
	doc := mustLoad(t, clinic)
	call, _ := doc.Call("pets", "update")
	for option, want := range map[string]string{"food": "food", "meal": "food", "toy": "toy", "role": "role", "as": "role", "tries": "tries"} {
		parameter, ok := call.Parameter(option)
		if !ok || parameter.Name != want {
			t.Errorf("Parameter(%q) = %v, %v; want %s", option, parameter, ok, want)
		}
	}
	if _, ok := call.Parameter("name"); ok {
		t.Error("the call has a parameter of another call")
	}
}

func TestCallNamesListResources(t *testing.T) {
	doc := mustLoad(t, clinic)
	uses := doc.CallNames()
	if len(uses) != 2 ||
		uses[0].Name != "rename" || !slices.Equal(uses[0].Resources, []string{"owners", "pets"}) ||
		uses[1].Name != "update" || !slices.Equal(uses[1].Resources, []string{"pets"}) {
		t.Errorf("CallNames() = %+v", uses)
	}
}

func TestSubResources(t *testing.T) {
	doc := mustLoad(t, clinic)
	var names []string
	for _, resource := range doc.SubResources("pets") {
		names = append(names, resource.Name)
	}
	if !slices.Equal(names, []string{"visits", "notes"}) {
		t.Errorf("SubResources(pets) = %v", names)
	}
	if len(doc.SubResources("visits")) != 0 {
		t.Error("a resource without sub-resources has some")
	}
}
