package definition_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// environments is the smallest part every definition needs.
const environments = "environments:\n  production:\n    apis:\n      main: https://api.example.test/v1/\n"

// reserved are the names a command line would claim in these tests.
var reserved = definition.Reserved{
	Commands:               []string{"get", "apply", "config"},
	GlobalOptions:          []string{"output", "verbose"},
	CallOptions:            []string{"if", "if-none", "if-any"},
	TemplateCallOptions:    []string{"template", "template-var", "dry-run"},
	TemplateOptionSuffixes: []string{"-template", "-template-file"},
}

// load loads a definition with overlays merged additively.
func load(t *testing.T, base string, overlays ...string) (*definition.Document, []definition.Diagnostic, error) {
	t.Helper()
	return loadMode(t, definition.Additive, base, overlays...)
}

func loadMode(t *testing.T, mode definition.Mode, base string, overlays ...string) (*definition.Document, []definition.Diagnostic, error) {
	t.Helper()
	options := definition.LoadOptions{Mode: mode, Reserved: reserved}
	for _, overlay := range overlays {
		options.Overlays = append(options.Overlays, definition.Source{Name: "overlay.yaml", Data: []byte(overlay)})
	}
	return definition.Load(definition.Source{Name: "base.yaml", Data: []byte(base)}, options)
}

// mustLoad loads a definition that has to be valid.
func mustLoad(t *testing.T, base string, overlays ...string) *definition.Document {
	t.Helper()
	doc, warnings, err := load(t, base, overlays...)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("Load warned: %v", warnings)
	}
	return doc
}

// problemsOf returns the problems of a failed load, one string each.
func problemsOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		t.Fatal("Load succeeded, want problems")
	}
	if kind := apikit.KindOf(err); kind != apikit.KindDefinition {
		t.Errorf("KindOf = %v, want %v", kind, apikit.KindDefinition)
	}
	failure, ok := errors.AsType[*definition.Error](err)
	if !ok {
		t.Fatalf("the error does not wrap a *definition.Error: %v", err)
	}
	out := make([]string, len(failure.Problems))
	for i, problem := range failure.Problems {
		if problem.Severity != definition.Problem {
			t.Errorf("problem %q has severity %d", problem, problem.Severity)
		}
		out[i] = problem.String()
	}
	return out
}

// wantProblems checks that the load failed with exactly the wanted
// problems, each given as a part of its text.
func wantProblems(t *testing.T, err error, want ...string) {
	t.Helper()
	got := problemsOf(t, err)
	for _, part := range want {
		if !slices.ContainsFunc(got, func(problem string) bool { return strings.Contains(problem, part) }) {
			t.Errorf("no problem contains %q", part)
		}
	}
	if len(got) != len(want) || t.Failed() {
		t.Errorf("got %d problems, want %d:\n  %s", len(got), len(want), strings.Join(got, "\n  "))
	}
}

func TestLoadMinimalDefinitionFillsDefaults(t *testing.T) {
	doc := mustLoad(t, environments)

	if len(doc.Environments) != 1 || doc.Environments[0].Name != "production" {
		t.Fatalf("Environments = %+v", doc.Environments)
	}
	if url, ok := doc.API("", "main"); !ok || url != "https://api.example.test/v1" {
		t.Errorf(`API("", "main") = %q, %v; want the URL without its trailing slash`, url, ok)
	}
	if url, ok := doc.API("production", "main"); !ok || url != "https://api.example.test/v1" {
		t.Errorf(`API("production", "main") = %q, %v`, url, ok)
	}
	if _, ok := doc.API("staging", "main"); ok {
		t.Error("an unknown environment has an API")
	}
	if _, ok := doc.API("", "other"); ok {
		t.Error("an unknown API has a URL")
	}
	if got := doc.Defaults; got.API != "main" || got.ItemAPI != "main" {
		t.Errorf("Defaults = %+v, want the only API for both", got)
	}

	http := doc.HTTP
	if http.TimeoutSeconds != 30 || http.Retries != 2 || http.RetryDelaySeconds != 1 ||
		http.JoinMaxWorkers != 50 || http.PaginationMaxWorkers != 8 || !slices.Equal(http.RetryStatuses, []int{502, 503, 504}) {
		t.Errorf("HTTP = %+v", http)
	}
	if got, want := doc.Pagination, (definition.Pagination{LimitParam: "limit", SkipParam: "skip", PageParam: "page", PageSize: 100}); got != want {
		t.Errorf("Pagination = %+v, want %+v", got, want)
	}
	if doc.Identifiers.Native != nil || !slices.Equal(doc.Identifiers.Fields, []string{"id", "${singular}Id"}) {
		t.Errorf("Identifiers = %+v", doc.Identifiers)
	}
	if doc.Columns.ID != "id" || doc.Output.MaxCharacters != 50 || !slices.Equal(doc.Output.NameFields, []string{"name", "title", "description"}) {
		t.Errorf("Columns = %+v, Output = %+v", doc.Columns, doc.Output)
	}
	if len(doc.Resources()) != 0 {
		t.Errorf("Resources() = %v, want none", doc.Resources())
	}
}

func TestUndefinedResourceGetsDefaults(t *testing.T) {
	doc := mustLoad(t, environments+"resources:\n  toys: {}\n  staff: {}\n")

	resource := doc.Resource("collars")
	if resource.Defined() || resource.Name != "collars" || resource.APIResource != "collars" ||
		resource.Singular != "collar" || !resource.SupportsListing {
		t.Errorf("Resource of an undescribed name = %+v", resource)
	}
	if !doc.Resource("toys").Defined() {
		t.Error("a described resource is not Defined")
	}

	names := map[string]string{"toys": "toys", "toy": "toys", "collar": "collars", "staff": "staff", "glass": "glass"}
	for arg, want := range names {
		if got := doc.ResourceName(arg); got != want {
			t.Errorf("ResourceName(%q) = %q, want %q", arg, got, want)
		}
	}
}

// full exercises every key of the schema once.
const full = `
environments:
  production:
    apis:
      main: https://api.example.test/v1
      admin: https://admin.example.test/v2
    linkBase: https://app.example.test/
  staging:
    apis:
      admin: https://admin.staging.example.test/v2
      main: https://api.staging.example.test/v1
defaults:
  api: admin
  itemApi: main
http:
  timeoutSeconds: 12.5
  retries: 0
  retryStatuses: [503]
  retryDelaySeconds: 0.5
  joinMaxWorkers: 4
  paginationMaxWorkers: 2
pagination: {limitParam: per_page, skipParam: offset, pageParam: p, pageSize: 25}
identifiers:
  native: "^[0-9a-f]{8}$"
  fields: [id, "${singular}Id", uid]
  display: [code]
columns: {id: ref}
filters: ["search="]
orderFilters: [sort, order]
filterIds:
  ownerId:
    - {match: "^o-", resource: owners}
    - {resource: pets}
output:
  maxCharacters: 40
  columnOrder: [id, code, name]
  nameFields: [label]
  keepWhole: ["^[0-9a-f]{8}$"]
auth:
  header: {name: X-Token, value: "${token}"}
resources:
  owners:
    api: admin
    singular: owner
    idField: ownerId
  visits:
    parent: pets
    supportsListing: false
    apiResource: appointments
  pets:
    filters: ["species=", "ownerId="]
    requiredFilters: [species]
    conditions: ["vaccinated=false", "tags[kind=diet].value!=none"]
    headers:
      - {name: X-Visit, column: token, resource: visits}
    urlTemplate:
      url: pets/view
      parameter: "${id}"
      encodeParameter: true
      query: {owner: "${ownerId}", name: "${name}"}
    columns:
      - id
      - {paths: [owner.name, ownerId], label: owner}
      - {paths: [species]}
    colors:
      - labels: [species]
        color: "245"
        env: EXAMPLE_COLOR_FADED
        rules:
          - {match: "cat.", color: "red"}
          - {match: "^dog", matchType: regex, caseSensitive: false, color: "blue", env: EXAMPLE_COLOR_DOG}
    joins:
      - {name: owner, column: ownerId, resource: owners, join: name}
      - name: clinic
        column: [relations, links]
        conditions: {kind: clinic, role: primary}
        resource: owners
        join: [id, name]
        many: true
        sort: [{column: name, direction: desc}, {column: id}]
        nested: {name: region, column: regionId, resource: owners, join: [id]}
      - name: toys
        column: toyIds
        resource: owners
        join: name
        nested:
          - {name: maker, column: makerId, resource: owners, join: name}
          - {name: shop, column: shopId, resource: owners, join: name}
    idAliases:
      - {match: "^[A-Z]{3}$", query: 'search="${id}"', field: code}
    idSearch:
      - {match: "^[0-9a-f]{8}$", not: true, column: name, idColumn: uid}
    setters:
      - name: rename
        aliases: [name]
        help: Rename a pet
        method: patch
        api: admin
        resource: owners
        pathTemplate: pets/${id}
        payloadTemplate: {name: "${name}", owner: {id: "${owner}"}, tags: ["${tag}"]}
        message: Renamed ${id}
        token: "${session}"
        parameters:
          - {name: name, aliases: [new-name], help: New name, template: true, maxLength: 80}
          - {name: owner, required: false, resource: owners, variables: {kind: owner, ref: "${owner}"}}
          - {name: tag, required: false, choices: [diet, care]}
          - name: level
            required: false
            choices: {owner: [low, high], tag: [none]}
          - {name: target, required: false, resource: {parameter: tag, resources: {diet: owners, care: pets}}}
          - name: vet
            required: false
            lookup: {get: "${kind}s", id: "${ref}", sub: staff, filters: ["role!=guest"], match: [id, email], select: id, complete: email}
          - {name: weight, type: float, required: false}
          - {name: age, type: int, required: false}
          - {name: fed, type: bool, required: false}
          - {name: toys, type: list, required: false}
        stepParameters:
          - {name: role, aliases: [as-role], default: keeper, help: Role to act as}
          - {name: tries, type: int, default: 3}
        exclusiveParameters: [[weight, age]]
        oneOfParameters: [[fed, toys]]
        steps:
          - {as: keeper, get: owners, id: "${id}", sub: staff, filters: ["role=${role}"], pick: first, select: id}
          - {as: session, call: login, resource: owners, id: "${keeper}", select: token, parameters: {reason: "${name}", urgent: true}}
        then:
          - {as: note, call: login, resource: owners, id: "${result}", select: id}
      - name: remove
        requiresId: true
        method: DELETE
        payloadTemplate: '{"force": ${force}}'
        parameters:
          - {name: force, type: bool, required: false}
  staff:
    parent: owners
`

func TestLoadDecodesEveryKey(t *testing.T) {
	// The owners resource needs the call the steps refer to.
	source := strings.Replace(full, "    idField: ownerId\n", "    idField: ownerId\n    setters:\n      - {name: login}\n", 1)
	doc := mustLoad(t, source)

	if got := doc.APINames(); !slices.Equal(got, []string{"main", "admin"}) {
		t.Errorf("APINames() = %v", got)
	}
	if url, _ := doc.API("staging", "main"); url != "https://api.staging.example.test/v1" {
		t.Errorf("staging main URL = %q", url)
	}
	if doc.Environments[0].LinkBase != "https://app.example.test" {
		t.Errorf("LinkBase = %q, want it without the trailing slash", doc.Environments[0].LinkBase)
	}
	if doc.Defaults.API != "admin" || doc.Defaults.ItemAPI != "main" {
		t.Errorf("Defaults = %+v", doc.Defaults)
	}
	if doc.HTTP.TimeoutSeconds != 12.5 || doc.HTTP.Retries != 0 || doc.HTTP.RetryDelaySeconds != 0.5 || !slices.Equal(doc.HTTP.RetryStatuses, []int{503}) {
		t.Errorf("HTTP = %+v", doc.HTTP)
	}
	if doc.Pagination.LimitParam != "per_page" || doc.Pagination.PageSize != 25 {
		t.Errorf("Pagination = %+v", doc.Pagination)
	}
	if !doc.Identifiers.Native.MatchString("0123abcd") || !slices.Equal(doc.Identifiers.Display, []string{"code"}) {
		t.Errorf("Identifiers = %+v", doc.Identifiers)
	}
	if doc.Columns.ID != "ref" || !slices.Equal(doc.OrderFilters, []string{"sort", "order"}) || !slices.Equal(doc.Filters, []string{"search="}) {
		t.Errorf("Columns = %+v, OrderFilters = %v, Filters = %v", doc.Columns, doc.OrderFilters, doc.Filters)
	}
	if len(doc.FilterIDs) != 1 || doc.FilterIDs[0].Filter != "ownerId" || len(doc.FilterIDs[0].Rules) != 2 ||
		!doc.FilterIDs[0].Rules[0].Match.MatchString("o-1") || doc.FilterIDs[0].Rules[1].Match != nil {
		t.Errorf("FilterIDs = %+v", doc.FilterIDs)
	}
	if doc.Output.MaxCharacters != 40 || len(doc.Output.KeepWhole) != 1 || !slices.Equal(doc.Output.ColumnOrder, []string{"id", "code", "name"}) {
		t.Errorf("Output = %+v", doc.Output)
	}
	if !doc.Auth.Has("header") {
		t.Error("the auth section was not kept")
	}

	var names []string
	for _, resource := range doc.Resources() {
		names = append(names, resource.Name)
	}
	if !slices.Equal(names, []string{"owners", "visits", "pets", "staff"}) {
		t.Errorf("resources in order = %v", names)
	}
	visits := doc.Resource("visits")
	if visits.Parent != "pets" || visits.SupportsListing || visits.APIResource != "appointments" || visits.Singular != "visit" {
		t.Errorf("visits = %+v", visits)
	}

	pets := doc.Resource("pets")
	if got := pets.Headers; len(got) != 1 || got[0] != (definition.Header{Name: "X-Visit", Column: "token", Resource: "visits"}) {
		t.Errorf("Headers = %+v", got)
	}
	link := pets.URLTemplate
	if link.URL != "pets/view" || link.Parameter != "${id}" || !link.EncodeParameter ||
		!slices.Equal(link.Query, []definition.QueryParameter{{Name: "owner", Template: "${ownerId}"}, {Name: "name", Template: "${name}"}}) {
		t.Errorf("URLTemplate = %+v", link)
	}
	if got := pets.Columns; len(got) != 3 || got[0].Label != "id" || got[1].Label != "owner" ||
		!slices.Equal(got[1].Paths, []string{"owner.name", "ownerId"}) || got[2].Label != "species" {
		t.Errorf("Columns = %+v", got)
	}
	colors := pets.Colors[0]
	if colors.Color != "245" || colors.Env != "EXAMPLE_COLOR_FADED" || len(colors.Rules) != 2 {
		t.Fatalf("Colors = %+v", colors)
	}
	if literal := colors.Rules[0].Pattern; !literal.MatchString("a cat.") || literal.MatchString("cats") || literal.MatchString("CAT.") {
		t.Errorf("literal rule pattern %q matches the wrong text", literal)
	}
	if regex := colors.Rules[1].Pattern; !regex.MatchString("DOG food") || regex.MatchString("a dog") {
		t.Errorf("regex rule pattern %q matches the wrong text", regex)
	}

	owner, clinic, toys := pets.Joins[0], pets.Joins[1], pets.Joins[2]
	if owner.Column != "ownerId" || !slices.Equal(owner.Fields, []string{"name"}) || owner.Many {
		t.Errorf("join owner = %+v", owner)
	}
	if clinic.Column != "" || !slices.Equal(clinic.RelationFields, []string{"relations", "links"}) || !slices.Equal(clinic.Fields, []string{"id", "name"}) || !clinic.Many ||
		!slices.Equal(clinic.Conditions, []definition.Pair{{Key: "kind", Value: "clinic"}, {Key: "role", Value: "primary"}}) ||
		!slices.Equal(clinic.Sort, []definition.Sort{{Column: "name", Descending: true}, {Column: "id"}}) ||
		len(clinic.Nested) != 1 || clinic.Nested[0].Name != "region" {
		t.Errorf("join clinic = %+v", clinic)
	}
	if len(toys.Nested) != 2 || toys.Nested[1].Name != "shop" {
		t.Errorf("join toys = %+v", toys)
	}
	if alias := pets.IDAliases[0]; !alias.Match.MatchString("ABC") || alias.Not || alias.Query != `search="${id}"` || alias.Field != "code" {
		t.Errorf("IDAliases = %+v", alias)
	}
	if search := pets.IDSearch[0]; !search.Not || search.Column != "name" || search.IDColumn != "uid" {
		t.Errorf("IDSearch = %+v", search)
	}

	rename, remove := pets.Setters[0], pets.Setters[1]
	if rename.Method != "PATCH" || rename.API != "admin" || rename.Resource != "owners" || !rename.RequiresID ||
		rename.PathTemplate != "pets/${id}" || rename.Message != "Renamed ${id}" || rename.Token != "${session}" ||
		!slices.Equal(rename.Aliases, []string{"name"}) {
		t.Errorf("setter rename = %+v", rename)
	}
	if payload, ok := rename.PayloadTemplate.(*apikit.Object); !ok || !slices.Equal(payload.Keys(), []string{"name", "owner", "tags"}) {
		t.Errorf("structured payload template = %#v", rename.PayloadTemplate)
	}
	if remove.Method != "DELETE" || remove.PayloadTemplate != `{"force": ${force}}` {
		t.Errorf("setter remove = %+v", remove)
	}

	byName := map[string]*definition.Parameter{}
	for _, parameter := range rename.Parameters {
		byName[parameter.Name] = parameter
	}
	if p := byName["name"]; !p.Required || !p.Template || p.MaxLength != 80 || p.Type != definition.TypeString ||
		!slices.Equal(p.OptionNames(), []string{"name", "new-name"}) {
		t.Errorf("parameter name = %+v", p)
	}
	if p := byName["owner"]; p.Required || p.Resource.Name != "owners" ||
		!slices.Equal(p.Variables, []definition.Pair{{Key: "kind", Value: "owner"}, {Key: "ref", Value: "${owner}"}}) {
		t.Errorf("parameter owner = %+v", p)
	}
	if p := byName["tag"]; !slices.Equal(p.Choices, []string{"diet", "care"}) {
		t.Errorf("parameter tag = %+v", p)
	}
	if p := byName["level"]; len(p.ChoicesBy) != 2 || p.ChoicesBy[0].Parameter != "owner" || !slices.Equal(p.ChoicesBy[1].Values, []string{"none"}) {
		t.Errorf("parameter level = %+v", p)
	}
	if p := byName["target"]; p.Resource.By != "tag" || !slices.Equal(p.Resource.Map, []definition.Pair{{Key: "diet", Value: "owners"}, {Key: "care", Value: "pets"}}) {
		t.Errorf("parameter target = %+v", p.Resource)
	}
	if lookup := byName["vet"].Lookup; lookup.Get != "${kind}s" || lookup.ID != "${ref}" || lookup.Sub != "staff" ||
		!slices.Equal(lookup.Match, []string{"id", "email"}) || lookup.Select != "id" || lookup.Complete != "email" ||
		!slices.Equal(lookup.Filters, []string{"role!=guest"}) {
		t.Errorf("lookup = %+v", lookup)
	}
	types := map[string]definition.ParamType{"weight": definition.TypeFloat, "age": definition.TypeInt, "fed": definition.TypeBool, "toys": definition.TypeList}
	for name, want := range types {
		if got := byName[name].Type; got != want {
			t.Errorf("type of %s = %q, want %q", name, got, want)
		}
	}
	if role, tries := rename.StepParameters[0], rename.StepParameters[1]; role.Default != "keeper" || role.Required ||
		apikit.Text(tries.Default) != "3" || tries.Type != definition.TypeInt {
		t.Errorf("step parameters = %+v, %+v", role, tries)
	}
	if !slices.Equal(rename.Exclusive[0], []string{"weight", "age"}) || !slices.Equal(rename.OneOf[0], []string{"fed", "toys"}) {
		t.Errorf("groups = %v, %v", rename.Exclusive, rename.OneOf)
	}
	keeper, session := rename.Steps[0], rename.Steps[1]
	if keeper.Get != "owners" || keeper.Sub != "staff" || !keeper.First || keeper.Select != "id" || !slices.Equal(keeper.Filters, []string{"role=${role}"}) {
		t.Errorf("step keeper = %+v", keeper)
	}
	if session.Call != "login" || session.Resource != "owners" || session.First || !slices.Equal(session.Parameters.Keys(), []string{"reason", "urgent"}) {
		t.Errorf("step session = %+v", session)
	}
	if note := rename.Then[0]; note.ID != "${result}" || note.Call != "login" {
		t.Errorf("then step = %+v", note)
	}
	if doc.Raw() == nil || !doc.Raw().Has("resources") {
		t.Error("Raw() does not return the document")
	}
}

func TestLoadRejectsUnreadableDocuments(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"syntax error", "environments: [", "cannot read definition base.yaml"},
		{"list at the root", "- a\n- b\n", "the document must be a mapping, not a list"},
		{"scalar at the root", "just text", "the document must be a mapping, not a string"},
		{"duplicate key", environments + "filters: []\nfilters: []\n", "cannot read definition base.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := load(t, tt.doc)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want it to contain %q", err, tt.want)
			}
			if kind := apikit.KindOf(err); kind != apikit.KindDefinition {
				t.Errorf("KindOf = %v, want %v", kind, apikit.KindDefinition)
			}
		})
	}
}

func TestLoadReadsJSONAndYAMLAnchors(t *testing.T) {
	doc := mustLoad(t, `{"environments": {"production": {"apis": {"main": "https://api.example.test"}}}, "http": {"retries": 5}}`)
	if doc.HTTP.Retries != 5 {
		t.Errorf("Retries = %d, want 5 from a JSON document", doc.HTTP.Retries)
	}

	doc = mustLoad(t, environments+`
resources:
  pets:
    filters: &common ["search=", "sort="]
  toys:
    filters: *common
`)
	if got := doc.Resource("toys").Filters; !slices.Equal(got, []string{"search=", "sort="}) {
		t.Errorf("filters through an alias = %v", got)
	}
}

func TestErrorListsEveryProblemWithItsPath(t *testing.T) {
	_, _, err := load(t, "http: {retries: -1}\nresources:\n  pets: {shade: red}\n")
	want := "invalid definition in base.yaml:\n" +
		"  - environments: at least one environment with its API base URLs is needed\n" +
		"  - http.retries: must be a whole number of at least 0\n" +
		"  - resources.pets.shade: unknown key"
	if err == nil || err.Error() != want {
		t.Errorf("Load error:\n%v\nwant:\n%s", err, want)
	}
}
