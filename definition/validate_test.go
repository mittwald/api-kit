package definition_test

import (
	"strings"
	"testing"
)

// withSetters returns a definition whose resource "pets" has the given
// setters. Resource "owners" has a call "login" for steps to refer to.
func withSetters(setters string) string {
	var b strings.Builder
	b.WriteString(environments)
	b.WriteString("resources:\n  owners:\n    setters:\n      - {name: login}\n  pets:\n    setters:\n")
	for line := range strings.SplitSeq(strings.Trim(setters, "\n"), "\n") {
		b.WriteString("      ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestValidSetters(t *testing.T) {
	tests := map[string]string{
		"setters that share a name and a step parameter": `
- name: update
  parameters: [{name: food}]
  stepParameters: [{name: role, default: keeper}]
- name: update
  parameters: [{name: toy}]
  stepParameters: [{name: role, default: keeper}]`,
		"setters of one name share an alias": `
- {name: update, aliases: [feed], parameters: [{name: food}]}
- {name: update, aliases: [feed], parameters: [{name: toy}]}`,
		"a group spans the setters of a call": `
- name: update
  parameters: [{name: food, required: false}]
  exclusiveParameters: [[food, toy]]
- name: update
  parameters: [{name: toy, required: false}]`,
		"parameters that set one variable share a group": `
- name: move
  requiresId: false
  pathTemplate: "${place}"
  parameters:
    - {name: home, required: false, variables: {place: "${home}"}}
    - {name: clinic, required: false, variables: {place: "${clinic}"}}
  oneOfParameters: [[home, clinic]]`,
		"steps build on each other and on the result": `
- name: adopt
  pathTemplate: pets/${id}/adopt/${keeper}
  token: "${session}"
  message: Adopted ${id} as ${note}
  parameters: [{name: owner}]
  steps:
    - {as: keeper, get: owners, id: "${owner}", select: id}
    - {as: session, call: login, resource: owners, id: "${keeper}", select: token}
  then:
    - {as: note, call: login, resource: owners, id: "${result}", select: id, parameters: {who: "${keeper}"}}`,
		"a plain dollar name is text, not a variable": `
- {name: price, pathTemplate: "pets/${id}", message: "Costs $amount or $$5"}`,
		"a lookup uses parameters and variables": `
- name: assign
  parameters:
    - {name: owner, variables: {kind: owners}}
    - {name: vet, lookup: {get: "${kind}", id: "${owner}", match: [email], select: id}}`,
	}
	for name, setters := range tests {
		t.Run(name, func(t *testing.T) {
			mustLoad(t, withSetters(setters))
		})
	}
}

func TestSetterProblems(t *testing.T) {
	const at = "resources.pets.setters"
	tests := []struct {
		name    string
		setters string
		want    []string
	}{
		{"name of a built-in command", `- {name: get}`,
			[]string{at + `[0].name: "get" is the name of a built-in command`}},
		{"missing name", `- {help: Nameless}`,
			[]string{at + "[0].name: is required"}},
		{"alias that is a setter name", "- {name: feed, aliases: [groom]}\n- {name: groom}",
			[]string{at + `[0].aliases[0]: "groom" is the name of a setter`}},
		{"alias of setters of different names", "- {name: feed, aliases: [care]}\n- {name: groom, aliases: [care]}",
			[]string{at + `[1].aliases[0]: "care" is an alias of "feed" setters as well`}},
		{"alias listed twice", `- {name: feed, aliases: [care, care]}`,
			[]string{at + `[0].aliases[1]: "care" is listed more than once`}},
		{"unknown API", `- {name: feed, api: other}`,
			[]string{at + `[0].api: unknown API "other"`}},

		{"parameter name used twice", `- {name: feed, parameters: [{name: food}, {name: treat, aliases: [food]}]}`,
			[]string{at + `[0].parameters[1]: parameter name or alias "food" is used more than once`}},
		{"parameter names that differ by a dash only", `- {name: feed, parameters: [{name: pet-food}, {name: pet_food}]}`,
			[]string{at + `[0].parameters[1]: parameter name or alias "pet_food" is used more than once`}},
		{"parameter name that is no option", `- {name: feed, parameters: [{name: 2fast}]}`,
			[]string{at + `[0].parameters[0]: parameter name or alias "2fast" must start with a letter`}},
		{"parameter without a name", `- {name: feed, parameters: [{help: Nameless}]}`,
			[]string{at + "[0].parameters[0].name: is required"}},
		{"unknown parameter type", `- {name: feed, parameters: [{name: food, type: date}]}`,
			[]string{at + `[0].parameters[0].type: must be str, int, float, bool or list, not "date"`}},
		{"required flag", `- {name: feed, parameters: [{name: quiet, type: bool}]}`,
			[]string{at + "[0].parameters[0].required: must be false for a parameter of type bool"}},
		{"template on a number", `- {name: feed, parameters: [{name: grams, type: int, template: true}]}`,
			[]string{at + "[0].parameters[0].template: only a parameter of type str can take a template"}},
		{"length of a number", `- {name: feed, parameters: [{name: grams, type: int, maxLength: 3}]}`,
			[]string{at + "[0].parameters[0].maxLength: only a parameter of type str has a length"}},
		{"length of zero", `- {name: feed, parameters: [{name: food, maxLength: 0}]}`,
			[]string{at + "[0].parameters[0].maxLength: must be a whole number of at least 1"}},
		{"choices of a number", `- {name: feed, parameters: [{name: grams, type: int, choices: ["1", "2"]}]}`,
			[]string{at + "[0].parameters[0].choices: only a parameter of type str can have choices"}},
		{"empty choices", `- {name: feed, parameters: [{name: food, choices: []}]}`,
			[]string{at + "[0].parameters[0].choices: needs at least one value"}},
		{"choices that are no strings", `- {name: feed, parameters: [{name: food, choices: [1]}]}`,
			[]string{at + "[0].parameters[0].choices[0]: must be a string, not a number"}},
		{"choices by an unknown parameter", `- {name: feed, parameters: [{name: food, choices: {bowl: [dry]}}]}`,
			[]string{at + `[0].parameters[0].choices.bowl: "bowl" is not a parameter of the setter`}},

		{"unknown resource of a parameter", `- {name: feed, parameters: [{name: owner, resource: keepers}]}`,
			[]string{at + `[0].parameters[0].resource: unknown resource "keepers"`}},
		{"resource by an unknown parameter", `- {name: feed, parameters: [{name: target, resource: {parameter: kind, resources: {a: owners}}}]}`,
			[]string{at + `[0].parameters[0].resource.parameter: "kind" is not a parameter of the setter`}},
		{"resource map with an unknown resource", `- {name: feed, parameters: [{name: kind}, {name: target, resource: {parameter: kind, resources: {a: keepers}}}]}`,
			[]string{at + `[0].parameters[1].resource.resources.a: unknown resource "keepers"`}},
		{"resource map without resources", `- {name: feed, parameters: [{name: kind}, {name: target, resource: {parameter: kind}}]}`,
			[]string{at + "[0].parameters[1].resource.resources: needs the resource for each value"}},

		{"variable named like a parameter", `- {name: feed, parameters: [{name: food, variables: {food: x}}]}`,
			[]string{at + "[0].parameters[0].variables.food: the name is taken"}},
		{"variable named like the ID", `- {name: feed, parameters: [{name: food, variables: {id: x}}]}`,
			[]string{at + "[0].parameters[0].variables.id: the name is taken"}},
		{"variable that uses an unknown name", `- {name: feed, parameters: [{name: food, variables: {kind: "${bowl}"}}]}`,
			[]string{at + `[0].parameters[0].variables.kind: template variable "bowl" is not a parameter`}},
		{"variable set twice without a group", `
- name: feed
  parameters:
    - {name: dry, required: false, variables: {kind: dry}}
    - {name: wet, required: false, variables: {kind: wet}}`,
			[]string{at + `[0]: parameters dry, wet all set variable "kind" and must share`}},

		{"lookup without its keys", `- {name: feed, parameters: [{name: vet, lookup: {id: x}}]}`,
			[]string{
				at + "[0].parameters[0].lookup.get: is required",
				at + "[0].parameters[0].lookup.select: is required",
				at + "[0].parameters[0].lookup.match: needs at least one field",
			}},
		{"lookup in an unknown resource", `- {name: feed, parameters: [{name: vet, lookup: {get: keepers, match: [id], select: id}}]}`,
			[]string{at + `[0].parameters[0].lookup.get: unknown resource "keepers"`}},
		{"lookup with an unknown variable", `- {name: feed, parameters: [{name: vet, lookup: {get: owners, id: "${bowl}", match: [id], select: id}}]}`,
			[]string{at + `[0].parameters[0].lookup: template variable "bowl" is not a parameter or a variable`}},

		{"step parameter without a default", `- {name: feed, stepParameters: [{name: role}]}`,
			[]string{at + "[0].stepParameters[0].default: a step parameter needs a default value"}},
		{"step parameter with a boolean default", `- {name: feed, stepParameters: [{name: role, default: true}]}`,
			[]string{at + "[0].stepParameters[0].default: a step parameter needs a default value"}},
		{"step parameter of type bool", `- {name: feed, stepParameters: [{name: role, type: bool, default: x}]}`,
			[]string{at + `[0].stepParameters[0].type: a step parameter must be of type str, int or float, not "bool"`}},
		{"step parameter named like a parameter", `- {name: feed, parameters: [{name: role}], stepParameters: [{name: as, aliases: [role], default: x}]}`,
			[]string{at + `[0].stepParameters[0]: step parameter name or alias "role" is taken by a parameter`}},
		{"step parameter named like a parameter of the call", `
- {name: feed, parameters: [{name: role}]}
- {name: feed, parameters: [{name: food}], stepParameters: [{name: role, default: x}]}`,
			[]string{at + `[1].stepParameters[0]: step parameter name or alias "role" is taken by a parameter`}},
		{"step parameter defined differently", `
- {name: feed, parameters: [{name: food}], stepParameters: [{name: role, default: keeper}]}
- {name: feed, parameters: [{name: toy}], stepParameters: [{name: role, default: owner}]}`,
			[]string{at + `[1].stepParameters[0]: step parameter "role" is defined differently by another "feed" setter`}},
		{"step parameter that knows no parameter keys", `- {name: feed, stepParameters: [{name: role, default: x, required: false}]}`,
			[]string{at + "[0].stepParameters[0].required: unknown key"}},

		{"shared name without agreement on the ID", `
- {name: feed, parameters: [{name: food}]}
- {name: feed, requiresId: false, parameters: [{name: toy}]}`,
			[]string{at + `[1].requiresId: setters named "feed" must agree on it`}},
		{"shared name without parameters", "- {name: feed, parameters: [{name: food}]}\n- {name: feed}",
			[]string{at + "[1]: a setter that shares its name needs parameters"}},
		{"shared name with a shared parameter", `
- {name: feed, parameters: [{name: food}]}
- {name: feed, parameters: [{name: bowl, aliases: [food]}]}`,
			[]string{at + `[1].parameters[0]: name or alias "food" is used by more than one "feed" setter`}},

		{"step without a name", `- {name: feed, steps: [{get: owners, select: id}]}`,
			[]string{at + "[0].steps[0].as: is required"}},
		{"step without a field to keep", `- {name: feed, steps: [{as: keeper, get: owners}]}`,
			[]string{at + "[0].steps[0].select: is required"}},
		{"step named like a parameter", `- {name: feed, parameters: [{name: keeper}], steps: [{as: keeper, get: owners, select: id}]}`,
			[]string{at + `[0].steps[0].as: "keeper" is taken`}},
		{"step named like the result", `- {name: feed, steps: [{as: result, get: owners, select: id}]}`,
			[]string{at + `[0].steps[0].as: "result" is taken`}},
		{"two steps of one name", `- {name: feed, steps: [{as: keeper, get: owners, select: id}, {as: keeper, get: owners, select: id}]}`,
			[]string{at + `[0].steps[1].as: "keeper" is taken`}},
		{"step that reads and calls", `- {name: feed, steps: [{as: keeper, get: owners, call: login, resource: owners, select: id}]}`,
			[]string{at + "[0].steps[0]: needs either 'get' or 'call'"}},
		{"step that does neither", `- {name: feed, steps: [{as: keeper, select: id}]}`,
			[]string{at + "[0].steps[0]: needs either 'get' or 'call'"}},
		{"step that reads an unknown resource", `- {name: feed, steps: [{as: keeper, get: keepers, select: id}]}`,
			[]string{at + `[0].steps[0].get: unknown resource "keepers"`}},
		{"call step without a resource", `- {name: feed, steps: [{as: keeper, call: login, select: id}]}`,
			[]string{at + "[0].steps[0].resource: a call step needs the resource"}},
		{"call step on an unknown resource", `- {name: feed, steps: [{as: keeper, call: login, resource: keepers, select: id}]}`,
			[]string{at + `[0].steps[0].resource: unknown resource "keepers"`}},
		{"call step with an unknown call", `- {name: feed, steps: [{as: keeper, call: logout, resource: owners, select: id}]}`,
			[]string{at + `[0].steps[0].call: resource "owners" has no call "logout"`}},
		{"step with an unknown way to pick", `- {name: feed, steps: [{as: keeper, get: owners, select: id, pick: last}]}`,
			[]string{at + `[0].steps[0].pick: must be one or first, not "last"`}},
		{"step that uses a later step", `
- name: feed
  steps:
    - {as: keeper, get: owners, id: "${session}", select: id}
    - {as: session, get: owners, select: id}`,
			[]string{at + `[0].steps[0]: template variable "session" is not a parameter or an earlier step`}},
		{"step that uses the ID of a call without one", `- {name: feed, requiresId: false, steps: [{as: keeper, get: owners, id: "${id}", select: id}]}`,
			[]string{at + `[0].steps[0]: template variable "id" is not a parameter or an earlier step`}},
		{"reading step after the request", `- {name: feed, then: [{as: note, get: owners, select: id}]}`,
			[]string{at + "[0].then[0]: only call steps can run after the request"}},
		{"result before the request", `- {name: feed, steps: [{as: keeper, get: owners, id: "${result}", select: id}]}`,
			[]string{at + `[0].steps[0]: template variable "result" is not a parameter or an earlier step`}},

		{"token from something that is no step", `- {name: feed, parameters: [{name: secret}], token: "${secret}"}`,
			[]string{at + `[0].token: template variable "secret" is not a step`}},
		{"path with an unknown variable", `- {name: feed, pathTemplate: "pets/${id}/${bowl}"}`,
			[]string{at + `[0].pathTemplate: template variable "bowl" is not a parameter or a step`}},
		{"path with the ID of a call without one", `- {name: feed, requiresId: false, pathTemplate: "pets/${id}"}`,
			[]string{at + `[0].pathTemplate: template variable "id" needs 'requiresId' to be true`}},
		{"structured payload with an unknown variable", `- {name: feed, payloadTemplate: {meal: {"${kind}": ["${food}"]}}, parameters: [{name: food}]}`,
			[]string{at + `[0].payloadTemplate: template variable "kind" is not a parameter or a step`}},
		{"string payload with an unknown variable", `- {name: feed, payloadTemplate: '{"food": "${food}"}'}`,
			[]string{at + `[0].payloadTemplate: template variable "food" is not a parameter or a step`}},
		{"payload that is a number", `- {name: feed, payloadTemplate: 5}`,
			[]string{at + "[0].payloadTemplate: must be a string, a mapping or a list, not a number"}},
		{"message with the ID of a call without one", `- {name: feed, requiresId: false, message: "Fed ${id}"}`,
			[]string{at + `[0].message: template variable "id" needs 'requiresId' to be true`}},

		{"group with an unknown parameter", `- {name: feed, parameters: [{name: food, required: false}], exclusiveParameters: [[food, bowl]]}`,
			[]string{at + `[0].exclusiveParameters[0]: unknown parameter "bowl"`}},
		{"group with a required parameter", `- {name: feed, parameters: [{name: food}, {name: toy, required: false}], exclusiveParameters: [[food, toy]]}`,
			[]string{at + `[0].exclusiveParameters[0]: parameter "food" is required and cannot be in a group`}},
		{"parameter in two groups", `
- name: feed
  parameters: [{name: food, required: false}, {name: toy, required: false}, {name: bowl, required: false}]
  exclusiveParameters: [[food, toy]]
  oneOfParameters: [[food, bowl]]`,
			[]string{at + `[0].oneOfParameters[0]: parameter "food" is in more than one group`}},
		{"one-of group of one", `- {name: feed, parameters: [{name: food, required: false}], oneOfParameters: [[food]]}`,
			[]string{at + "[0].oneOfParameters[0]: needs 2 parameters or more"}},
		{"group that is no list", `- {name: feed, exclusiveParameters: [food]}`,
			[]string{at + "[0].exclusiveParameters[0]: must be a list of parameter names, not a string"}},

		{"parameter named like an option of every command", `- {name: feed, parameters: [{name: output}]}`,
			[]string{at + `: option "output" of call "feed" is taken by an option every command has`}},
		{"parameter named like an option of every call", `- {name: feed, parameters: [{name: food, aliases: [if]}]}`,
			[]string{at + `: option "if" of call "feed" is taken by an option every call has`}},
		{"step parameter named like an option of every call", `- {name: feed, stepParameters: [{name: if-none, default: x}]}`,
			[]string{at + `: option "if-none" of call "feed" is taken by an option every call has`}},
		{"parameter named like a template option", `- {name: feed, parameters: [{name: note, template: true}, {name: note-template-file}]}`,
			[]string{at + `: option "note-template-file" of call "feed" is taken by the options its templates add`}},
		{"parameter named like the template option of the call", `
- {name: feed, parameters: [{name: note, template: true}]}
- {name: feed, parameters: [{name: dry-run, type: bool, required: false}]}`,
			[]string{at + `: option "dry-run" of call "feed" is taken by the options its templates add`}},

		{"setter that is no mapping", "- feed",
			[]string{at + "[0]: must be a mapping, not a string"}},
		{"unknown key of a setter", `- {name: feed, verb: POST}`,
			[]string{at + "[0].verb: unknown key"}},
		{"parameters that are no list", `- {name: feed, parameters: {name: food}}`,
			[]string{at + "[0].parameters: must be a list, not a mapping"}},
		{"flag that is no boolean", `- {name: feed, requiresId: "no"}`,
			[]string{at + "[0].requiresId: must be true or false, not a string"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := load(t, withSetters(tt.setters))
			wantProblems(t, err, tt.want...)
		})
	}
}

func TestDocumentProblems(t *testing.T) {
	const two = "environments:\n  production:\n    apis: {main: https://api.example.test, admin: https://admin.example.test}\n"
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{"no environments", "resources: {}\n",
			[]string{"environments: at least one environment"}},
		{"empty environments", "environments: {}\n",
			[]string{"environments: at least one environment"}},
		{"environments that are a list", "environments: [production]\n",
			[]string{"environments: must be a mapping, not a list"}},
		{"environment without APIs", "environments:\n  production: {linkBase: https://app.example.test}\n",
			[]string{"environments.production.apis: needs the base URL of at least one API"}},
		{"API URL that is no string", "environments:\n  production:\n    apis: {main: 8080}\n",
			[]string{"environments.production.apis.main: must be a string, not a number", "environments.production.apis: needs the base URL"}},
		{"environments with different APIs", two + "  staging:\n    apis: {main: https://api.staging.example.test}\ndefaults: {api: main}\n",
			[]string{`environments.staging.apis: must define the same APIs as environment "production": main, admin`}},
		{"several APIs without a default", two,
			[]string{"defaults.api: must name the API for requests that name none: main, admin"}},
		{"unknown default API", two + "defaults: {api: other}\n",
			[]string{`defaults.api: unknown API "other", the environments define: main, admin`}},
		{"unknown default API for items", two + "defaults: {api: main, itemApi: other}\n",
			[]string{`defaults.itemApi: unknown API "other"`}},
		{"unknown API of a resource", environments + "resources:\n  pets: {api: other}\n",
			[]string{`resources.pets.api: unknown API "other"`}},

		{"timeout below a second", environments + "http: {timeoutSeconds: 0.5}\n",
			[]string{"http.timeoutSeconds: must be a number of at least 1"}},
		{"workers that are no whole number", environments + "http: {joinMaxWorkers: 2.5}\n",
			[]string{"http.joinMaxWorkers: must be a whole number of at least 1"}},
		{"retry statuses that are no numbers", environments + "http: {retryStatuses: [bad]}\n",
			[]string{"http.retryStatuses[0]: must be a whole number"}},
		{"number given as text", environments + "http: {retries: \"2\"}\n",
			[]string{"http.retries: must be a whole number, not a string"}},
		{"page size of zero", environments + "pagination: {pageSize: 0}\n",
			[]string{"pagination.pageSize: must be a whole number of at least 1"}},
		{"unknown top-level key", environments + "resource: {}\n",
			[]string{"resource: unknown key"}},
		{"unknown key of a section", environments + "output: {maxChars: 10}\n",
			[]string{"output.maxChars: unknown key"}},
		{"filters that are no strings", environments + "filters: [1]\n",
			[]string{"filters[0]: must be a string, not a number"}},

		{"pattern that does not compile", environments + "identifiers: {native: \"(\"}\n",
			[]string{`identifiers.native: "(" is not a regular expression`}},
		{"pattern with look-ahead", environments + "output: {keepWhole: [\"^(?!x)\"]}\n",
			[]string{"output.keepWhole[0]: "}},
		{"filter rule for an unknown resource", environments + "filterIds:\n  ownerId: [{resource: owners}]\n",
			[]string{`filterIds.ownerId[0].resource: unknown resource "owners"`}},
		{"filter rule with a bad pattern", environments + "resources: {owners: {}}\nfilterIds:\n  ownerId: [{match: \"[\", resource: owners}]\n",
			[]string{`filterIds.ownerId[0].match: "[" is not a regular expression`}},
		{"filter rules that are no list", environments + "filterIds: {ownerId: owners}\n",
			[]string{"filterIds.ownerId: must be a list of rules, not a string"}},

		{"setters that are no list", environments + "resources:\n  pets: {setters: {name: feed}}\n",
			[]string{"resources.pets.setters: must be a list, not a mapping"}},
		{"resource that is no mapping", environments + "resources: {pets: all}\n",
			[]string{"resources.pets: must be a mapping, not a string"}},
		{"condition that is none", environments + "resources:\n  pets: {conditions: [vaccinated]}\n",
			[]string{`resources.pets.conditions[0]: must be a condition like enabled=false, not "vaccinated"`}},
		{"column without paths", environments + "resources:\n  pets: {columns: [{label: owner}]}\n",
			[]string{"resources.pets.columns[0].paths: needs at least one path"}},
		{"column that is a number", environments + "resources:\n  pets: {columns: [1]}\n",
			[]string{"resources.pets.columns[0]: must be a mapping, not a number"}},
		{"color rule of an unknown kind", environments + "resources:\n  pets:\n    colors: [{labels: [name], rules: [{match: a, matchType: glob}]}]\n",
			[]string{`resources.pets.colors[0].rules[0].matchType: must be literal or regex, not "glob"`}},
		{"color rule with a bad pattern", environments + "resources:\n  pets:\n    colors: [{labels: [name], rules: [{match: \"(\", matchType: regex}]}]\n",
			[]string{"resources.pets.colors[0].rules[0].match: "}},
		{"join without its keys", environments + "resources:\n  pets: {joins: [{column: ownerId}]}\n",
			[]string{"resources.pets.joins[0].name: is required", "resources.pets.joins[0].resource: is required"}},
		{"join of something that is no field", environments + "resources:\n  pets: {joins: [{name: owner, column: ownerId, resource: owners, join: 1}]}\n",
			[]string{"resources.pets.joins[0].join: must be a field or a list of fields, not a number"}},
		{"join over relation fields without conditions", environments + "resources:\n  pets: {joins: [{name: owner, column: [links], resource: owners, join: name}]}\n",
			[]string{"resources.pets.joins[0].column: a list of fields needs 'conditions'"}},
		{"join sorted in an unknown direction", environments + "resources:\n  pets: {joins: [{name: owner, column: ownerId, resource: owners, join: name, sort: [{column: name, direction: up}]}]}\n",
			[]string{`resources.pets.joins[0].sort[0].direction: must be asc or desc, not "up"`}},
		{"nested join with a mistake", environments + "resources:\n  pets: {joins: [{name: owner, column: ownerId, resource: owners, join: name, nested: [{name: region, column: regionId, join: id}]}]}\n",
			[]string{"resources.pets.joins[0].nested[0].resource: is required"}},
		{"link template without a URL", environments + "resources:\n  pets: {urlTemplate: {query: {id: \"${id}\"}}}\n",
			[]string{"resources.pets.urlTemplate.url: is required"}},
		{"header from a resource that is no sub-resource", environments + "resources:\n  owners: {}\n  pets: {headers: [{name: X-Owner, column: id, resource: owners}]}\n",
			[]string{`resources.pets.headers[0].resource: "owners" is not a sub-resource of "pets"`}},
		{"header from a resource the definition does not describe", environments + "resources:\n  pets: {headers: [{name: X-Key, column: key, resource: keys}]}\n",
			[]string{`resources.pets.headers[0].resource: "keys" is not a sub-resource of "pets"`}},
		{"header without its keys", environments + "resources:\n  pets: {headers: [{name: X-Token}]}\n",
			[]string{"resources.pets.headers[0].column: is required", "resources.pets.headers[0].resource: is required"}},
		{"ID alias without a query", environments + "resources:\n  pets: {idAliases: [{match: \"^p-\"}]}\n",
			[]string{"resources.pets.idAliases[0].query: is required"}},
		{"ID search without a column", environments + "resources:\n  pets: {idSearch: [{match: \"^p-\"}]}\n",
			[]string{"resources.pets.idSearch[0].column: is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := load(t, tt.doc)
			wantProblems(t, err, tt.want...)
		})
	}
}
