package output

import (
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// Format is one of the ways a result can be written.
type Format string

// The output formats.
const (
	// Text is a table of the columns the definition names for the resource.
	Text Format = "text"
	// Wide is a table of every field.
	Wide Format = "wide"
	// JSON is the result as JSON, with the values of joined resources.
	JSON Format = "json"
	// JSONRaw is the result as the API returned it, as JSON.
	JSONRaw Format = "json-raw"
	// YAML is the result as YAML, with the values of joined resources.
	YAML Format = "yaml"
	// JSONPath is what a JSONPath expression selects of each item.
	JSONPath Format = "jsonpath"
	// Columns is one line per item with the named fields, separated by
	// tabs.
	Columns Format = "columns"
	// ID is the ID of each item, one per line.
	ID Format = "id"
	// Name is the name of each item, one per line.
	Name Format = "name"
	// Link is the link to each item in a web interface, one per line.
	Link Format = "link"
	// CSV is the table of Text as comma-separated values.
	CSV Format = "csv"
	// TSV is the table of Text as tab-separated values.
	TSV Format = "tsv"
	// Markdown is the table of Text as a Markdown table.
	Markdown Format = "markdown"
)

// formats lists the formats in the order help and completion show them.
var formats = []Format{Text, Wide, JSON, JSONRaw, YAML, JSONPath, Columns, ID, Name, Link, CSV, TSV, Markdown}

// aliases are other spellings of a format. The ones in deprecated still
// work but are on their way out.
var (
	aliases    = map[string]Format{"plain": Text, "table": Text, "txt": Text, "yml": YAML, "md": Markdown, "jsonraw": JSONRaw}
	deprecated = []string{"jsonraw"}
)

// Formats returns the names of the formats, in the order help shows them.
func Formats() []string {
	names := make([]string, len(formats))
	for i, format := range formats {
		names[i] = string(format)
	}
	return names
}

// TakesArgument reports whether the format needs an argument, given after
// an equals sign: the expression of [JSONPath], the fields of [Columns].
func (f Format) TakesArgument() bool { return f == JSONPath || f == Columns }

// Tabular reports whether the format is a table.
func (f Format) Tabular() bool {
	return f == Text || f == Wide || f == CSV || f == TSV || f == Markdown
}

// Enriched reports whether the format shows the values of joined resources
// and so needs them fetched. [JSONRaw], [ID] and [Name] show only what the
// API returned.
func (f Format) Enriched() bool { return f != JSONRaw && f != ID && f != Name }

// Spec is a parsed --output value.
type Spec struct {
	// Format is the format.
	Format Format
	// Argument is the expression of [JSONPath] or the comma-separated
	// fields of [Columns]; empty for every other format.
	Argument string
	// Deprecated is the spelling that was used when it is one that still
	// works but should be replaced by the name of Format; empty otherwise.
	Deprecated string
}

// String returns the spec the way it is written on the command line.
func (s Spec) String() string {
	if s.Format.TakesArgument() {
		return string(s.Format) + "=" + s.Argument
	}
	return string(s.Format)
}

// Parse reads an --output value: the name of a format, followed by an
// equals sign and an argument for the formats that take one, as in
// "jsonpath=$.name" or "columns=id,name". The empty value is [Text]. A
// mistake is an error of kind [apikit.KindUsage].
func Parse(value string) (Spec, error) {
	name, argument, hasArgument := strings.Cut(value, "=")
	if name == "" && !hasArgument {
		return Spec{Format: Text}, nil
	}
	spec := Spec{Format: Format(name)}
	if format, ok := aliases[name]; ok {
		spec.Format = format
		if slices.Contains(deprecated, name) {
			spec.Deprecated = name
		}
	}
	if !slices.Contains(formats, spec.Format) {
		return Spec{}, apikit.Errorf(apikit.KindUsage, "invalid output format %q, must be one of %s", name, strings.Join(Formats(), ", "))
	}
	switch {
	case spec.Format == JSONPath:
		if strings.TrimSpace(argument) == "" {
			return Spec{}, apikit.Errorf(apikit.KindUsage, "output format jsonpath needs an expression, for example -o jsonpath='$.name'")
		}
		spec.Argument = argument
	case spec.Format == Columns:
		if strings.Trim(argument, ", ") == "" {
			return Spec{}, apikit.Errorf(apikit.KindUsage, "output format columns needs fields, for example -o columns=id,name")
		}
		spec.Argument = argument
	case hasArgument:
		return Spec{}, apikit.Errorf(apikit.KindUsage, "output format %s takes no argument", spec.Format)
	}
	return spec, nil
}
