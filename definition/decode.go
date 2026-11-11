package definition

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// Defaults of the tuning sections.
const (
	defaultTimeoutSeconds       = 30
	defaultRetries              = 2
	defaultRetryDelaySeconds    = 1
	defaultJoinMaxWorkers       = 50
	defaultPaginationMaxWorkers = 8
	defaultPageSize             = 100
	defaultMaxCharacters        = 50
	defaultMethod               = "POST"
	defaultIDField              = "id"
)

// document decodes the root of a definition. Mistakes are recorded in the
// loader; the returned document is complete enough to keep checking.
func (l *loader) document(root *apikit.Object) *Document {
	m, _ := l.mapping("", root)
	doc := &Document{byName: map[string]*Resource{}, raw: root}

	l.environments(m, doc)
	l.defaults(m, doc)
	l.http(m, doc)
	l.pagination(m, doc)
	l.identifiers(m, doc)
	l.output(m, doc)

	doc.Columns = Columns{ID: defaultIDField}
	if columns, ok := m.child("columns"); ok {
		doc.Columns.ID = columns.text("id", defaultIDField)
		columns.finish()
	}
	doc.Filters = m.texts("filters")
	doc.OrderFilters = m.texts("orderFilters")
	l.auth(m, doc)

	// Resources come before the filter rules, which refer to them.
	if resources, ok := m.child(keyResources); ok {
		for name, value := range resources.object.All() {
			resources.used[name] = true
			resource := l.resource(resources.at(name), name, value)
			if resource == nil {
				continue
			}
			doc.resources = append(doc.resources, resource)
			doc.byName[name] = resource
		}
	}
	l.filterIDs(m, doc)
	m.finish()
	return doc
}

func (l *loader) environments(m mapping, doc *Document) {
	environments, ok := m.child("environments")
	if !ok || environments.object.Len() == 0 {
		if !m.has("environments") || ok {
			l.problemf("environments", "at least one environment with its API base URLs is needed")
		}
		return
	}
	for name, value := range environments.object.All() {
		environments.used[name] = true
		env, ok := l.mapping(environments.at(name), value)
		if !ok {
			continue
		}
		environment := Environment{Name: name, LinkBase: strings.TrimRight(env.text("linkBase", ""), "/")}
		for _, api := range env.pairs("apis") {
			environment.APIs = append(environment.APIs, API{Name: api.Key, URL: strings.TrimRight(api.Value, "/")})
		}
		if len(environment.APIs) == 0 {
			l.problemf(env.at("apis"), "needs the base URL of at least one API")
		}
		env.finish()
		doc.Environments = append(doc.Environments, environment)
	}
	// A resource names an API without knowing the environment, so every
	// environment has to offer the same ones.
	if len(doc.Environments) == 0 {
		return
	}
	want := doc.APINames()
	for _, environment := range doc.Environments[1:] {
		var have []string
		for _, api := range environment.APIs {
			have = append(have, api.Name)
		}
		if len(have) > 0 && !sameSet(want, have) {
			l.problemf(join(join("environments", environment.Name), "apis"),
				"must define the same APIs as environment %q: %s", doc.Environments[0].Name, strings.Join(want, ", "))
		}
	}
}

func (l *loader) defaults(m mapping, doc *Document) {
	names := doc.APINames()
	defaults, ok := m.child("defaults")
	if ok {
		doc.Defaults.API = defaults.text("api", "")
		doc.Defaults.ItemAPI = defaults.text("itemApi", "")
		defaults.finish()
	}
	switch {
	case doc.Defaults.API != "":
		l.knownAPI("defaults.api", doc, doc.Defaults.API)
	case len(names) == 1:
		doc.Defaults.API = names[0]
	case len(names) > 1:
		l.problemf("defaults.api", "must name the API for requests that name none: %s", strings.Join(names, ", "))
	}
	if doc.Defaults.ItemAPI == "" {
		doc.Defaults.ItemAPI = doc.Defaults.API
	} else {
		l.knownAPI("defaults.itemApi", doc, doc.Defaults.ItemAPI)
	}
}

// knownAPI reports an API name no environment defines.
func (l *loader) knownAPI(path string, doc *Document, name string) {
	names := doc.APINames()
	if len(names) > 0 && !slices.Contains(names, name) {
		l.problemf(path, "unknown API %q, the environments define: %s", name, strings.Join(names, ", "))
	}
}

func (l *loader) http(m mapping, doc *Document) {
	doc.HTTP = HTTP{
		TimeoutSeconds:       defaultTimeoutSeconds,
		Retries:              defaultRetries,
		RetryStatuses:        []int{502, 503, 504},
		RetryDelaySeconds:    defaultRetryDelaySeconds,
		JoinMaxWorkers:       defaultJoinMaxWorkers,
		PaginationMaxWorkers: defaultPaginationMaxWorkers,
	}
	http, ok := m.child("http")
	if !ok {
		return
	}
	doc.HTTP.TimeoutSeconds = http.number("timeoutSeconds", doc.HTTP.TimeoutSeconds, 1)
	doc.HTTP.Retries = http.integer("retries", doc.HTTP.Retries, 0)
	doc.HTTP.RetryStatuses = http.integers("retryStatuses", doc.HTTP.RetryStatuses)
	doc.HTTP.RetryDelaySeconds = http.number("retryDelaySeconds", doc.HTTP.RetryDelaySeconds, 0)
	doc.HTTP.JoinMaxWorkers = http.integer("joinMaxWorkers", doc.HTTP.JoinMaxWorkers, 1)
	doc.HTTP.PaginationMaxWorkers = http.integer("paginationMaxWorkers", doc.HTTP.PaginationMaxWorkers, 1)
	http.finish()
}

func (l *loader) pagination(m mapping, doc *Document) {
	doc.Pagination = Pagination{LimitParam: "limit", SkipParam: "skip", PageParam: "page", PageSize: defaultPageSize}
	pagination, ok := m.child("pagination")
	if !ok {
		return
	}
	doc.Pagination.LimitParam = pagination.text("limitParam", doc.Pagination.LimitParam)
	doc.Pagination.SkipParam = pagination.text("skipParam", doc.Pagination.SkipParam)
	doc.Pagination.PageParam = pagination.text("pageParam", doc.Pagination.PageParam)
	doc.Pagination.PageSize = pagination.integer("pageSize", doc.Pagination.PageSize, 1)
	pagination.finish()
}

func (l *loader) identifiers(m mapping, doc *Document) {
	doc.Identifiers = Identifiers{Fields: []string{defaultIDField, "${singular}Id"}}
	identifiers, ok := m.child("identifiers")
	if !ok {
		return
	}
	doc.Identifiers.Native = identifiers.pattern("native")
	if identifiers.has("fields") {
		doc.Identifiers.Fields = identifiers.texts("fields")
	}
	doc.Identifiers.Display = identifiers.texts("display")
	identifiers.finish()
}

func (l *loader) output(m mapping, doc *Document) {
	doc.Output = Output{MaxCharacters: defaultMaxCharacters, NameFields: []string{"name", "title", "description"}}
	output, ok := m.child("output")
	if !ok {
		return
	}
	doc.Output.MaxCharacters = output.integer("maxCharacters", doc.Output.MaxCharacters, 1)
	doc.Output.ColumnOrder = output.texts("columnOrder")
	if output.has("nameFields") {
		doc.Output.NameFields = output.texts("nameFields")
	}
	for i, source := range output.texts("keepWhole") {
		if pattern := l.pattern(index(output.at("keepWhole"), i), source); pattern != nil {
			doc.Output.KeepWhole = append(doc.Output.KeepWhole, pattern)
		}
	}
	output.finish()
}

func (l *loader) filterIDs(m mapping, doc *Document) {
	filterIDs, ok := m.child("filterIds")
	if !ok {
		return
	}
	for filter, value := range filterIDs.object.All() {
		filterIDs.used[filter] = true
		path := filterIDs.at(filter)
		rules, ok := value.([]any)
		if !ok {
			l.problemf(path, "must be a list of rules, not %s", kindOf(value))
			continue
		}
		entry := FilterID{Filter: filter}
		for i, item := range rules {
			rule, ok := l.mapping(index(path, i), item)
			if !ok {
				continue
			}
			resource := rule.text("resource", "")
			if _, known := doc.byName[resource]; !known {
				l.problemf(rule.at("resource"), "unknown resource %q", resource)
			}
			entry.Rules = append(entry.Rules, FilterIDRule{Match: rule.pattern("match"), Resource: resource})
			rule.finish()
		}
		doc.FilterIDs = append(doc.FilterIDs, entry)
	}
}

func (l *loader) resource(path, name string, value any) *Resource {
	m, ok := l.mapping(path, value)
	if !ok {
		return nil
	}
	resource := &Resource{
		Name:            name,
		API:             m.text("api", ""),
		APIResource:     m.text("apiResource", name),
		Parent:          m.text("parent", ""),
		SupportsListing: m.flag("supportsListing", true),
		Singular:        m.text("singular", singular(name)),
		IDField:         m.text("idField", ""),
		Filters:         m.texts("filters"),
		RequiredFilters: m.texts("requiredFilters"),
		Conditions:      m.texts("conditions"),
		defined:         true,
	}
	for i, item := range m.items("headers") {
		header, ok := l.mapping(index(m.at("headers"), i), item)
		if !ok {
			continue
		}
		resource.Headers = append(resource.Headers, Header{
			Name:     header.required("name"),
			Column:   header.required("column"),
			Resource: header.required("resource"),
		})
		header.finish()
	}
	if template, ok := m.child("urlTemplate"); ok {
		resource.URLTemplate = &URLTemplate{
			URL:             template.required("url"),
			Parameter:       template.text("parameter", ""),
			EncodeParameter: template.flag("encodeParameter", false),
		}
		for _, pair := range template.pairs("query") {
			resource.URLTemplate.Query = append(resource.URLTemplate.Query, QueryParameter{Name: pair.Key, Template: pair.Value})
		}
		template.finish()
	}
	for i, item := range m.items("columns") {
		if column, ok := l.column(index(m.at("columns"), i), item); ok {
			resource.Columns = append(resource.Columns, column)
		}
	}
	for i, item := range m.items("colors") {
		if colors, ok := l.colors(index(m.at("colors"), i), item); ok {
			resource.Colors = append(resource.Colors, colors)
		}
	}
	for i, item := range m.items("joins") {
		if join := l.join(index(m.at("joins"), i), item); join != nil {
			resource.Joins = append(resource.Joins, join)
		}
	}
	for i, item := range m.items("idAliases") {
		alias, ok := l.mapping(index(m.at("idAliases"), i), item)
		if !ok {
			continue
		}
		resource.IDAliases = append(resource.IDAliases, IDAlias{
			Match: alias.pattern("match"),
			Not:   alias.flag("not", false),
			Query: alias.required("query"),
			Field: alias.text("field", ""),
		})
		alias.finish()
	}
	for i, item := range m.items("idSearch") {
		search, ok := l.mapping(index(m.at("idSearch"), i), item)
		if !ok {
			continue
		}
		resource.IDSearch = append(resource.IDSearch, IDSearch{
			Match:    search.pattern("match"),
			Not:      search.flag("not", false),
			Column:   search.required("column"),
			IDColumn: search.text("idColumn", defaultIDField),
		})
		search.finish()
	}
	for i, item := range m.items(keySetters) {
		if setter := l.setter(index(m.at(keySetters), i), item); setter != nil {
			resource.Setters = append(resource.Setters, setter)
		}
	}
	m.finish()
	return resource
}

// required returns the string under key and reports a missing or empty one.
func (m mapping) required(key string) string {
	text := m.text(key, "")
	if text == "" && (!m.has(key) || isString(m.object, key)) {
		m.l.problemf(m.at(key), "is required")
	}
	return text
}

func isString(object *apikit.Object, key string) bool {
	value, _ := object.Get(key)
	_, ok := value.(string)
	return ok
}

// column decodes a table column: a path, or a mapping with several paths
// and a label.
func (l *loader) column(path string, value any) (Column, bool) {
	if text, ok := value.(string); ok {
		return Column{Label: text, Paths: []string{text}}, true
	}
	m, ok := l.mapping(path, value)
	if !ok {
		return Column{}, false
	}
	column := Column{Paths: m.texts("paths")}
	if len(column.Paths) == 0 {
		l.problemf(m.at("paths"), "needs at least one path")
		m.used["label"] = true
		m.finish()
		return Column{}, false
	}
	column.Label = m.text("label", column.Paths[0])
	m.finish()
	return column, true
}

func (l *loader) colors(path string, value any) (ColorSet, bool) {
	m, ok := l.mapping(path, value)
	if !ok {
		return ColorSet{}, false
	}
	set := ColorSet{Labels: m.texts("labels"), Color: m.text("color", ""), Env: m.text("env", "")}
	for i, item := range m.items("rules") {
		rule, ok := l.mapping(index(m.at("rules"), i), item)
		if !ok {
			continue
		}
		source := rule.required("match")
		matchType := rule.text("matchType", "literal")
		caseSensitive := rule.flag("caseSensitive", true)
		switch matchType {
		case "literal":
			source = regexp.QuoteMeta(source)
		case "regex":
		default:
			l.problemf(rule.at("matchType"), "must be literal or regex, not %q", matchType)
		}
		if !caseSensitive {
			source = "(?i)" + source
		}
		entry := ColorRule{Pattern: l.pattern(rule.at("match"), source), Color: rule.text("color", ""), Env: rule.text("env", "")}
		rule.finish()
		if entry.Pattern != nil {
			set.Rules = append(set.Rules, entry)
		}
	}
	m.finish()
	return set, true
}

func (l *loader) join(path string, value any) *Join {
	m, ok := l.mapping(path, value)
	if !ok {
		return nil
	}
	join := &Join{
		Name:       m.required("name"),
		Resource:   m.required("resource"),
		Many:       m.flag("many", false),
		Conditions: m.pairs("conditions"),
	}
	// "column" is the path to the related ID, or the fields that hold
	// relation entries when conditions pick the entry.
	switch column, _ := m.get("column"); column := column.(type) {
	case string:
		join.Column = column
	case []any:
		join.RelationFields = l.texts(m.at("column"), column)
	default:
		l.problemf(m.at("column"), "must be a path or a list of fields, not %s", kindOf(column))
	}
	if len(join.RelationFields) > 0 && len(join.Conditions) == 0 {
		l.problemf(m.at("column"), "a list of fields needs 'conditions' that pick the relation entry")
	}
	// "join" is the field to show, or the fields of the joined object.
	switch fields, present := m.get("join"); fields := fields.(type) {
	case string:
		join.Fields = []string{fields}
	case []any:
		join.Fields = l.texts(m.at("join"), fields)
	default:
		if present {
			l.problemf(m.at("join"), "must be a field or a list of fields, not %s", kindOf(fields))
		}
	}
	for i, item := range m.items("sort") {
		sort, ok := l.mapping(index(m.at("sort"), i), item)
		if !ok {
			continue
		}
		direction := sort.text("direction", "asc")
		if direction != "asc" && direction != "desc" {
			l.problemf(sort.at("direction"), "must be asc or desc, not %q", direction)
		}
		join.Sort = append(join.Sort, Sort{Column: sort.required("column"), Descending: direction == "desc"})
		sort.finish()
	}
	// "nested" is one join or a list of joins.
	switch nested, present := m.get("nested"); nested := nested.(type) {
	case []any:
		for i, item := range nested {
			if child := l.join(index(m.at("nested"), i), item); child != nil {
				join.Nested = append(join.Nested, child)
			}
		}
	default:
		if present {
			if child := l.join(m.at("nested"), nested); child != nil {
				join.Nested = append(join.Nested, child)
			}
		}
	}
	m.finish()
	return join
}

func (l *loader) setter(path string, value any) *Setter {
	m, ok := l.mapping(path, value)
	if !ok {
		return nil
	}
	setter := &Setter{
		Name:         m.required(keyName),
		Aliases:      m.texts(keyAliases),
		Help:         m.text("help", ""),
		Method:       strings.ToUpper(m.text("method", defaultMethod)),
		API:          m.text("api", ""),
		Resource:     m.text("resource", ""),
		RequiresID:   m.flag("requiresId", true),
		PathTemplate: m.text("pathTemplate", ""),
		Message:      m.text("message", ""),
		Token:        m.text("token", ""),
		path:         path,
	}
	if payload, ok := m.get("payloadTemplate"); ok {
		switch payload.(type) {
		case string, *apikit.Object, []any:
			setter.PayloadTemplate = payload
		default:
			l.problemf(m.at("payloadTemplate"), "must be a string, a mapping or a list, not %s", kindOf(payload))
		}
	}
	for i, item := range m.items(keyParameters) {
		if parameter := l.parameter(index(m.at(keyParameters), i), item, false); parameter != nil {
			setter.Parameters = append(setter.Parameters, parameter)
		}
	}
	for i, item := range m.items(keyStepParameters) {
		if parameter := l.parameter(index(m.at(keyStepParameters), i), item, true); parameter != nil {
			setter.StepParameters = append(setter.StepParameters, parameter)
		}
	}
	setter.Exclusive = l.groups(m, "exclusiveParameters")
	setter.OneOf = l.groups(m, "oneOfParameters")
	for i, item := range m.items("steps") {
		if step := l.step(index(m.at("steps"), i), item); step != nil {
			setter.Steps = append(setter.Steps, step)
		}
	}
	for i, item := range m.items("then") {
		if step := l.step(index(m.at("then"), i), item); step != nil {
			setter.Then = append(setter.Then, step)
		}
	}
	m.finish()
	return setter
}

// groups decodes a list of lists of parameter names.
func (l *loader) groups(m mapping, key string) [][]string {
	var groups [][]string
	for i, item := range m.items(key) {
		path := index(m.at(key), i)
		names, ok := item.([]any)
		if !ok {
			l.problemf(path, "must be a list of parameter names, not %s", kindOf(item))
			continue
		}
		groups = append(groups, l.texts(path, names))
	}
	return groups
}

// parameter decodes a parameter of a setter, or one of its step parameters.
func (l *loader) parameter(path string, value any, step bool) *Parameter {
	m, ok := l.mapping(path, value)
	if !ok {
		return nil
	}
	parameter := &Parameter{
		Name:     m.required(keyName),
		Aliases:  m.texts(keyAliases),
		Type:     ParamType(m.text("type", string(TypeString))),
		Help:     m.text("help", ""),
		Required: !step,
		path:     path,
		raw:      m.object,
	}
	if step {
		l.stepParameter(m, parameter)
		m.finish()
		return parameter
	}

	parameter.Required = m.flag("required", true)
	parameter.Template = m.flag("template", false)
	parameter.MaxLength = m.integer("maxLength", 0, 1)
	parameter.Variables = m.pairs("variables")
	isString := parameter.Type == TypeString
	switch parameter.Type {
	case TypeString, TypeInt, TypeFloat, TypeList:
	case TypeBool:
		// A flag is given or not; a required flag could never be false.
		if parameter.Required {
			l.problemf(m.at("required"), "must be false for a parameter of type bool, which is a flag")
		}
	default:
		l.problemf(m.at("type"), "must be str, int, float, bool or list, not %q", string(parameter.Type))
	}
	if parameter.Template && !isString {
		l.problemf(m.at("template"), "only a parameter of type str can take a template")
	}
	if parameter.MaxLength > 0 && !isString {
		l.problemf(m.at("maxLength"), "only a parameter of type str has a length")
	}

	// "resource" is a resource, or one per value of another parameter.
	switch target, present := m.get("resource"); target := target.(type) {
	case string:
		parameter.Resource = &ParameterResource{Name: target}
	default:
		if !present {
			break
		}
		by, ok := l.mapping(m.at("resource"), target)
		if !ok {
			break
		}
		parameter.Resource = &ParameterResource{By: by.required("parameter"), Map: by.pairs("resources")}
		if len(parameter.Resource.Map) == 0 {
			l.problemf(by.at("resources"), "needs the resource for each value of the parameter")
		}
		by.finish()
	}

	// "choices" is a list, or one list per other parameter.
	switch choices, present := m.get("choices"); choices := choices.(type) {
	case []any:
		parameter.Choices = l.texts(m.at("choices"), choices)
		if len(choices) == 0 {
			l.problemf(m.at("choices"), "needs at least one value")
		}
	case *apikit.Object:
		for other, values := range choices.All() {
			list, ok := values.([]any)
			if !ok || len(list) == 0 {
				l.problemf(join(m.at("choices"), other), "must be a list of at least one value")
				continue
			}
			parameter.ChoicesBy = append(parameter.ChoicesBy, ChoiceSet{Parameter: other, Values: l.texts(join(m.at("choices"), other), list)})
		}
		if choices.Len() == 0 {
			l.problemf(m.at("choices"), "needs at least one value")
		}
	default:
		if present {
			l.problemf(m.at("choices"), "must be a list of values, or one list per other parameter, not %s", kindOf(choices))
		}
	}
	// A number given on the command line could never equal a string.
	if m.has("choices") && !isString {
		l.problemf(m.at("choices"), "only a parameter of type str can have choices")
	}

	if lookup, ok := m.child("lookup"); ok {
		parameter.Lookup = &Lookup{
			Get:      lookup.required("get"),
			ID:       lookup.text("id", ""),
			Sub:      lookup.text("sub", ""),
			Filters:  lookup.texts("filters"),
			Match:    lookup.texts("match"),
			Select:   lookup.required("select"),
			Complete: lookup.text("complete", ""),
			raw:      lookup.object,
		}
		if len(parameter.Lookup.Match) == 0 {
			l.problemf(lookup.at("match"), "needs at least one field the value may equal")
		}
		lookup.finish()
	}
	m.finish()
	return parameter
}

// stepParameter decodes what only a step parameter has: a default, and a
// type that has one.
func (l *loader) stepParameter(m mapping, parameter *Parameter) {
	switch parameter.Type {
	case TypeString, TypeInt, TypeFloat:
	default:
		l.problemf(m.at("type"), "a step parameter must be of type str, int or float, not %q", string(parameter.Type))
	}
	value, _ := m.get("default")
	switch value.(type) {
	case string, json.Number:
		parameter.Default = value
	default:
		l.problemf(m.at("default"), "a step parameter needs a default value: a string or a number")
	}
}

func (l *loader) step(path string, value any) *Step {
	m, ok := l.mapping(path, value)
	if !ok {
		return nil
	}
	step := &Step{
		As:       m.required("as"),
		Get:      m.text("get", ""),
		Call:     m.text("call", ""),
		Resource: m.text("resource", ""),
		ID:       m.text("id", ""),
		Sub:      m.text("sub", ""),
		Filters:  m.texts("filters"),
		Select:   m.required("select"),
		path:     path,
		raw:      m.object,
	}
	if m.has("get") == m.has("call") {
		l.problemf(path, "needs either 'get' or 'call'")
	}
	if m.has("call") && step.Resource == "" {
		l.problemf(m.at("resource"), "a call step needs the resource the call belongs to")
	}
	switch pick := m.text("pick", "one"); pick {
	case "one":
	case "first":
		step.First = true
	default:
		l.problemf(m.at("pick"), "must be one or first, not %q", pick)
	}
	if parameters, ok := m.child("parameters"); ok {
		step.Parameters = parameters.object
	}
	m.finish()
	return step
}

// sameSet reports whether two lists hold the same names, in any order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, name := range a {
		if !slices.Contains(b, name) {
			return false
		}
	}
	return true
}
