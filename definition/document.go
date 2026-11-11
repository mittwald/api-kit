package definition

import (
	"regexp"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// Keys of a definition document that code outside the decoder refers to.
const (
	keyResources      = "resources"
	keySetters        = "setters"
	keyName           = "name"
	keyAliases        = "aliases"
	keyParameters     = "parameters"
	keyStepParameters = "stepParameters"
)

// ExtraKey is the key under which the engine stores what it adds to an item:
// the values of joined resources and the link to the item. Paths fall back
// to it, so a column "owner.name" finds a joined owner.
const ExtraKey = "extra"

// Names that every call defines itself and that parameters, variables and
// steps therefore cannot take.
const (
	// VariableID is the ID of the resource a call changes.
	VariableID = "id"
	// VariableResult is the ID in the response of a setter, for its "then"
	// steps.
	VariableResult = "result"
	// VariableChanges lists what a setter sent, for its message.
	VariableChanges = "changes"
)

// Document is a loaded and checked definition.
type Document struct {
	// Environments are the deployments of the API, such as production and
	// staging, in document order. The first one is the default.
	Environments []Environment
	// Defaults name the API a request goes to when nothing else does.
	Defaults Defaults
	// HTTP tunes requests.
	HTTP HTTP
	// Pagination names the query parameters that page through a listing.
	Pagination Pagination
	// Identifiers describe what IDs look like and where items keep them.
	Identifiers Identifiers
	// Columns names fields the engine reads by convention.
	Columns Columns
	// Filters are offered for every resource, as "key=" or "key=value".
	Filters []string
	// OrderFilters are filter keys that order a listing and do not narrow
	// it.
	OrderFilters []string
	// FilterIDs resolve the values of filters to IDs, in document order.
	FilterIDs []FilterID
	// Output tunes how results are shown.
	Output Output
	// Auth describes how requests are authenticated; package auth works
	// from it.
	Auth Auth

	resources []*Resource
	byName    map[string]*Resource
	raw       *apikit.Object
}

// Environment is one deployment of the API.
type Environment struct {
	// Name selects the environment on the command line.
	Name string
	// APIs are the base URLs of the environment, in document order. Every
	// environment defines the same names.
	APIs []API
	// LinkBase is prepended to link templates that hold no host.
	LinkBase string
}

// API is one base URL of an environment.
type API struct {
	// Name is what resources and setters refer to.
	Name string
	// URL is the base URL, without a trailing slash.
	URL string
}

// Defaults name the API a request goes to when neither the resource nor the
// setter names one.
type Defaults struct {
	// API is used for requests without an ID, such as listings.
	API string
	// ItemAPI is used for requests with an ID. It defaults to API.
	ItemAPI string
}

// HTTP tunes requests.
type HTTP struct {
	// TimeoutSeconds is how long to wait for a connection or a response.
	TimeoutSeconds float64
	// Retries is how often a GET is sent again after a timeout, a lost
	// connection or one of RetryStatuses. Other requests are never repeated.
	Retries int
	// RetryStatuses are the response statuses worth another attempt.
	RetryStatuses []int
	// RetryDelaySeconds is the pause before the first retry; it doubles
	// with every further one.
	RetryDelaySeconds float64
	// JoinMaxWorkers limits the requests joins send at the same time.
	JoinMaxWorkers int
	// PaginationMaxWorkers limits the pages of one listing fetched at the
	// same time.
	PaginationMaxWorkers int
}

// Pagination names the query parameters that page through a listing.
type Pagination struct {
	// LimitParam carries the page size.
	LimitParam string
	// SkipParam carries the number of items to skip.
	SkipParam string
	// PageParam carries a page number.
	PageParam string
	// PageSize is the page size when the caller names none.
	PageSize int
}

// Identifiers describe what IDs look like and where items keep them.
type Identifiers struct {
	// Native matches a value that is an ID already and needs no lookup. Nil
	// means no value is taken for one.
	Native *regexp.Regexp
	// Fields are the fields that may hold the ID of an item, in order of
	// preference. "${singular}" stands for the singular name of the
	// resource.
	Fields []string
	// Display are fields that identify an item to a person, such as a short
	// ID, shown in messages next to the name.
	Display []string
}

// Columns names fields the engine reads by convention.
type Columns struct {
	// ID is the field holding the ID in an entry that relates one item to
	// another.
	ID string
}

// FilterID resolves the values of one filter to IDs.
type FilterID struct {
	// Filter is the filter key.
	Filter string
	// Rules are tried in order; the first whose Match fits the value names
	// the resource the value is looked up in.
	Rules []FilterIDRule
}

// FilterIDRule is one rule of a [FilterID].
type FilterIDRule struct {
	// Match fits the values the rule is for. Nil fits every value.
	Match *regexp.Regexp
	// Resource is where the value is resolved to an ID.
	Resource string
}

// Output tunes how results are shown.
type Output struct {
	// MaxCharacters is the length a table cell is cut to.
	MaxCharacters int
	// ColumnOrder are field names that come first when all fields of an
	// item are shown, in this order.
	ColumnOrder []string
	// NameFields are the fields tried, in order, for the name of an item.
	NameFields []string
	// KeepWhole match cell values that are never cut, such as IDs.
	KeepWhole []*regexp.Regexp
}

// Resource describes one kind of item of the API.
type Resource struct {
	// Name is the name on the command line and in the definition.
	Name string
	// API names the API the resource lives on. Empty means the default.
	API string
	// APIResource is the path segment of the resource. It defaults to Name.
	APIResource string
	// Parent makes this a sub-resource, reached as /parent/{id}/resource.
	Parent string
	// SupportsListing is false for a resource that cannot be listed on its
	// own.
	SupportsListing bool
	// Singular is the name of one item, used in messages and ID fields. It
	// defaults to Name without a trailing "s".
	Singular string
	// IDField is the field holding the ID when it is none of
	// [Identifiers.Fields].
	IDField string
	// Headers are sent with a request for one item, with values taken from
	// a sub-resource.
	Headers []Header
	// URLTemplate builds the link to an item in a web interface.
	URLTemplate *URLTemplate
	// Filters are offered for listings, as "key=" or "key=value". A
	// condition on one of these keys is sent to the API as a filter.
	Filters []string
	// RequiredFilters are filter keys a listing must have.
	RequiredFilters []string
	// Conditions are offered for the conditions of a call.
	Conditions []string
	// Columns are shown in a table.
	Columns []Column
	// Colors paint table cells.
	Colors []ColorSet
	// Joins add values of related resources to an item.
	Joins []*Join
	// IDAliases resolve a value to an ID by searching for it.
	IDAliases []IDAlias
	// IDSearch resolves a value to an ID by scanning the whole listing.
	IDSearch []IDSearch
	// Setters are the requests that change the resource.
	Setters []*Setter

	defined bool
}

// Defined reports whether the definition describes the resource. A resource
// it does not describe can still be fetched by name, with defaults.
func (r *Resource) Defined() bool { return r.defined }

// Header is a request header whose value comes from a sub-resource.
type Header struct {
	// Name is the header name.
	Name string
	// Column is the field of the sub-resource holding the value.
	Column string
	// Resource is the sub-resource to fetch.
	Resource string
}

// URLTemplate builds the link to an item.
type URLTemplate struct {
	// URL is the address; without a host it is appended to
	// [Environment.LinkBase].
	URL string
	// Parameter is a template appended to URL.
	Parameter string
	// EncodeParameter appends the whole item instead, as base64-encoded
	// JSON.
	EncodeParameter bool
	// Query are the query parameters, in document order.
	Query []QueryParameter
}

// QueryParameter is one query parameter of a [URLTemplate].
type QueryParameter struct {
	// Name is the parameter name.
	Name string
	// Template is rendered from the item for the value.
	Template string
}

// Column is one column of a table.
type Column struct {
	// Label is the heading. It defaults to the path of a single-path
	// column.
	Label string
	// Paths lead to the values shown; several are merged into one cell.
	Paths []string
}

// ColorSet paints the cells of some columns.
type ColorSet struct {
	// Labels are the headings of the columns to paint.
	Labels []string
	// Color is the color of a cell no rule matches.
	Color string
	// Env names an environment variable that overrides Color.
	Env string
	// Rules paint cells by their content, the first match wins.
	Rules []ColorRule
}

// ColorRule paints the parts of a cell a pattern matches.
type ColorRule struct {
	// Pattern finds the text to paint. A rule that matches literal text has
	// it quoted here; a rule that ignores case carries the (?i) flag.
	Pattern *regexp.Regexp
	// Color is the color of the matching text.
	Color string
	// Env names an environment variable that overrides Color.
	Env string
}

// Join adds values of a related resource to an item.
type Join struct {
	// Name is the heading of the joined column and the prefix of paths
	// into it.
	Name string
	// Column leads to the ID, or IDs, of the related items.
	Column string
	// RelationFields are the fields holding relation entries to search
	// with Conditions, in place of Column.
	RelationFields []string
	// Resource is the related resource.
	Resource string
	// Fields are the paths taken from a related item. One path joins its
	// value; several join an object with the ID of the item and each
	// path's value.
	Fields []string
	// Many keeps a list even for one related item, and leaves the join out
	// of tables.
	Many bool
	// Conditions are fields a relation entry must have for its ID to count.
	Conditions []Pair
	// Sort orders the related items.
	Sort []Sort
	// Nested are joins from the related items onward.
	Nested []*Join
}

// Pair is a key with a value, kept in document order.
type Pair struct {
	// Key is the key.
	Key string
	// Value is the value.
	Value string
}

// Sort orders items by a field.
type Sort struct {
	// Column is the path of the field.
	Column string
	// Descending reverses the order.
	Descending bool
}

// IDAlias resolves a value to an ID by searching for it.
type IDAlias struct {
	// Match fits the values the rule is for.
	Match *regexp.Regexp
	// Not inverts Match.
	Not bool
	// Query is the filter to search with, a template of "${id}".
	Query string
	// Field must equal the value in a hit when a call resolves it, so that
	// a hit merely mentioning the value does not count.
	Field string
}

// IDSearch resolves a value to an ID by scanning the whole listing.
type IDSearch struct {
	// Match fits the values the rule is for.
	Match *regexp.Regexp
	// Not inverts Match.
	Not bool
	// Column leads to the field that must equal the value.
	Column string
	// IDColumn leads to the ID of the hit. It defaults to "id".
	IDColumn string
}

// Setter is one request that changes a resource.
type Setter struct {
	// Name is the name of the call. Setters of one resource that share a
	// name form one call.
	Name string
	// Aliases are further names that run this setter alone.
	Aliases []string
	// Help describes the setter.
	Help string
	// Method is the HTTP method.
	Method string
	// API names the API. Empty means that of the resource.
	API string
	// Resource describes the response, when it is not the resource itself.
	Resource string
	// RequiresID is false for a setter that works without the ID of an
	// existing item, such as one that creates an item.
	RequiresID bool
	// PathTemplate is the path below the API's base URL. Empty means
	// resource/id.
	PathTemplate string
	// PayloadTemplate is the request body: nil for none, a string holding
	// JSON, or a mapping or list. Its strings are templates.
	PayloadTemplate any
	// Message is the template of the line printed on success.
	Message string
	// Token is the template of the token to send the request with, in
	// place of the caller's own.
	Token string
	// Parameters are the values the caller gives.
	Parameters []*Parameter
	// StepParameters are optional values with a default, for the steps.
	StepParameters []*Parameter
	// Exclusive are groups of parameters of which at most one may be
	// given.
	Exclusive [][]string
	// OneOf are groups of parameters of which exactly one must be given.
	OneOf [][]string
	// Steps run before the request and keep one value each.
	Steps []*Step
	// Then are call steps that run after the request succeeded.
	Then []*Step

	path string
}

// ParamType is the type of a parameter's value.
type ParamType string

// The parameter types.
const (
	// TypeString is any text.
	TypeString ParamType = "str"
	// TypeInt is a whole number.
	TypeInt ParamType = "int"
	// TypeFloat is a number.
	TypeFloat ParamType = "float"
	// TypeBool is a flag: given or not.
	TypeBool ParamType = "bool"
	// TypeList is one or more texts.
	TypeList ParamType = "list"
)

// Parameter is one value a caller gives to a call.
type Parameter struct {
	// Name is the name of the option and of the template variable.
	Name string
	// Aliases are further option names.
	Aliases []string
	// Type is the type of the value.
	Type ParamType
	// Help describes the parameter.
	Help string
	// Required is false for a parameter that may be left out.
	Required bool
	// Resource makes the value the ID of an item of a resource; a name or
	// alias is resolved first.
	Resource *ParameterResource
	// Default is the value of a step parameter that was not given: a
	// string or a [json.Number].
	Default any
	// Variables are template variables the parameter sets when it is
	// given, in document order.
	Variables []Pair
	// Choices are the values the parameter takes.
	Choices []string
	// ChoicesBy are the values the parameter takes depending on which
	// other parameter is given, in document order.
	ChoicesBy []ChoiceSet
	// Template lets the value be given as a template as well.
	Template bool
	// MaxLength is the most characters the value may have; 0 means any.
	MaxLength int
	// Lookup resolves the value against a listing.
	Lookup *Lookup

	path string
	raw  *apikit.Object
}

// OptionNames returns the name and the aliases of the parameter: the
// options it is given with.
func (p *Parameter) OptionNames() []string {
	return append([]string{p.Name}, p.Aliases...)
}

// ParameterResource names the resource a parameter's value is the ID of.
type ParameterResource struct {
	// Name is the resource.
	Name string
	// By names another parameter whose value decides the resource, in
	// place of Name.
	By string
	// Map gives the resource for each value of By.
	Map []Pair
}

// ChoiceSet are the choices of a parameter while another one is given.
type ChoiceSet struct {
	// Parameter is the other parameter.
	Parameter string
	// Values are the choices.
	Values []string
}

// Lookup resolves a parameter's value against a listing.
type Lookup struct {
	// Get is the resource to list, a template.
	Get string
	// ID is the ID of the item whose sub-resource is listed, a template.
	ID string
	// Sub is the sub-resource to list.
	Sub string
	// Filters narrow the listing: "key=value" is sent to the API,
	// "key!=value" drops items afterwards.
	Filters []string
	// Match are the fields the value may equal.
	Match []string
	// Select is the field of the hit to keep.
	Select string
	// Complete is the field shell completion offers.
	Complete string

	raw *apikit.Object
}

// Step is a request around the one of a setter that keeps one value.
type Step struct {
	// As names the value for later templates.
	As string
	// Get is the resource to fetch, for a step that reads.
	Get string
	// Call is the call to run, for a step that changes something.
	Call string
	// Resource is the resource Call belongs to.
	Resource string
	// ID is the ID of the item, a template.
	ID string
	// Sub is the sub-resource to list.
	Sub string
	// Filters narrow a listing: "key=value" is sent to the API,
	// "key!=value" drops items afterwards.
	Filters []string
	// First takes the first of several items; otherwise more than one item
	// is an error.
	First bool
	// Select is the field to keep.
	Select string
	// Parameters are the parameters of Call: templates by name.
	Parameters *apikit.Object

	path string
	raw  *apikit.Object
}

// Resources returns the resources the definition describes, in document
// order.
func (d *Document) Resources() []*Resource {
	return d.resources
}

// Resource returns the resource of that name. A name the definition does
// not describe yields a resource with the defaults, since any path of the
// API can be fetched by name.
func (d *Document) Resource(name string) *Resource {
	if resource, ok := d.byName[name]; ok {
		return resource
	}
	return &Resource{Name: name, APIResource: name, SupportsListing: true, Singular: singular(name)}
}

// ResourceName returns the resource a command-line argument names. Resource
// names are plural by convention, so a singular argument gets an "s"; a name
// the definition describes is taken as it is.
func (d *Document) ResourceName(arg string) string {
	if _, ok := d.byName[arg]; ok || strings.HasSuffix(arg, "s") {
		return arg
	}
	return arg + "s"
}

// Raw returns the merged document the definition was loaded from. The
// caller must not change it.
func (d *Document) Raw() *apikit.Object {
	return d.raw
}

// API returns the base URL of the named API in the named environment. An
// empty environment means the default one.
func (d *Document) API(environment, name string) (string, bool) {
	for _, env := range d.Environments {
		if environment != "" && env.Name != environment {
			continue
		}
		for _, api := range env.APIs {
			if api.Name == name {
				return api.URL, true
			}
		}
		return "", false
	}
	return "", false
}

// APINames returns the names of the APIs, in document order.
func (d *Document) APINames() []string {
	if len(d.Environments) == 0 {
		return nil
	}
	names := make([]string, len(d.Environments[0].APIs))
	for i, api := range d.Environments[0].APIs {
		names[i] = api.Name
	}
	return names
}

// singular returns the name of one item of a resource.
func singular(resource string) string {
	return strings.TrimSuffix(resource, "s")
}
