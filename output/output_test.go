package output_test

import (
	"bytes"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/output"
)

// pets is the result most tests write.
const pets = `[
  {"id": "p-1", "name": "Rex", "species": "dog", "age": 3, "weight": 12.50, "vaccinated": true,
   "owner": {"name": "Ada", "city": "Linz"}, "tags": ["calm", "old"], "note": null,
   "extra": {"clinic": {"name": "North"}}},
  {"id": "p-2", "name": "Tom", "species": "cat", "age": 12, "vaccinated": false,
   "owner": {"name": "Bob", "city": "Graz"}, "tags": [], "note": "eats\tlittle\nat night"}
]`

// items decodes a JSON list for a test.
func items(t *testing.T, source string) []any {
	t.Helper()
	value, err := apikit.Unmarshal([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is no list", source)
	}
	return list
}

// written runs a writer and returns what it wrote.
func written(t *testing.T, write func(*bytes.Buffer) error) string {
	t.Helper()
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestParse(t *testing.T) {
	tests := []struct {
		value string
		want  output.Spec
	}{
		{"", output.Spec{Format: output.Text}},
		{"text", output.Spec{Format: output.Text}},
		{"plain", output.Spec{Format: output.Text}},
		{"table", output.Spec{Format: output.Text}},
		{"txt", output.Spec{Format: output.Text}},
		{"wide", output.Spec{Format: output.Wide}},
		{"json", output.Spec{Format: output.JSON}},
		{"json-raw", output.Spec{Format: output.JSONRaw}},
		{"jsonraw", output.Spec{Format: output.JSONRaw, Deprecated: "jsonraw"}},
		{"yaml", output.Spec{Format: output.YAML}},
		{"yml", output.Spec{Format: output.YAML}},
		{"id", output.Spec{Format: output.ID}},
		{"name", output.Spec{Format: output.Name}},
		{"link", output.Spec{Format: output.Link}},
		{"csv", output.Spec{Format: output.CSV}},
		{"tsv", output.Spec{Format: output.TSV}},
		{"markdown", output.Spec{Format: output.Markdown}},
		{"md", output.Spec{Format: output.Markdown}},
		{"jsonpath=$.name", output.Spec{Format: output.JSONPath, Argument: "$.name"}},
		{`jsonpath=$[?(@.a == "b=c")].id`, output.Spec{Format: output.JSONPath, Argument: `$[?(@.a == "b=c")].id`}},
		{"columns=id, owner.name", output.Spec{Format: output.Columns, Argument: "id, owner.name"}},
	}
	for _, tt := range tests {
		got, err := output.Parse(tt.value)
		if err != nil {
			t.Errorf("Parse(%q): %v", tt.value, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tt.value, got, tt.want)
		}
	}
}

func TestParseRejectsMistakes(t *testing.T) {
	tests := map[string]string{
		"xml":          `invalid output format "xml", must be one of text, wide, json, json-raw, yaml, jsonpath, columns, id, name, link, csv, tsv, markdown`,
		"JSON":         `invalid output format "JSON"`,
		"=x":           `invalid output format ""`,
		"jsonpath":     "output format jsonpath needs an expression",
		"jsonpath=":    "output format jsonpath needs an expression",
		"jsonpath=  ":  "output format jsonpath needs an expression",
		"columns":      "output format columns needs fields",
		"columns= , ,": "output format columns needs fields",
		"json=x":       "output format json takes no argument",
		"plain=":       "output format text takes no argument",
	}
	for value, want := range tests {
		_, err := output.Parse(value)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want it to contain %q", value, err, want)
		}
		if kind := apikit.KindOf(err); kind != apikit.KindUsage {
			t.Errorf("Parse(%q) error kind = %v, want %v", value, kind, apikit.KindUsage)
		}
	}
}

func TestSpecString(t *testing.T) {
	for _, value := range []string{"text", "json-raw", "jsonpath=$.name", "columns=id,name"} {
		spec, err := output.Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		if got := spec.String(); got != value {
			t.Errorf("String() = %q, want %q", got, value)
		}
	}
}

func TestFormatProperties(t *testing.T) {
	if got := strings.Join(output.Formats(), " "); got != "text wide json json-raw yaml jsonpath columns id name link csv tsv markdown" {
		t.Errorf("Formats() = %s", got)
	}
	tabular := []output.Format{output.Text, output.Wide, output.CSV, output.TSV, output.Markdown}
	raw := []output.Format{output.JSONRaw, output.ID, output.Name}
	for _, name := range output.Formats() {
		format := output.Format(name)
		isTabular, isRaw := false, false
		for _, f := range tabular {
			isTabular = isTabular || f == format
		}
		for _, f := range raw {
			isRaw = isRaw || f == format
		}
		if format.Tabular() != isTabular {
			t.Errorf("%s.Tabular() = %v", format, format.Tabular())
		}
		if format.Enriched() == isRaw {
			t.Errorf("%s.Enriched() = %v", format, format.Enriched())
		}
		if format.TakesArgument() != (format == output.JSONPath || format == output.Columns) {
			t.Errorf("%s.TakesArgument() = %v", format, format.TakesArgument())
		}
	}
}

func TestWriteJSONKeepsOrderAndNumbers(t *testing.T) {
	got := written(t, func(b *bytes.Buffer) error {
		return output.WriteJSON(b, items(t, `[{"zeta": 1.50, "alpha": "<a&b>", "list": [1, {"y": null, "x": true}]}]`))
	})
	want := `[
  {
    "zeta": 1.50,
    "alpha": "<a&b>",
    "list": [
      1,
      {
        "y": null,
        "x": true
      }
    ]
  }
]
`
	if got != want {
		t.Errorf("WriteJSON wrote:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteYAMLKeepsOrder(t *testing.T) {
	got := written(t, func(b *bytes.Buffer) error {
		return output.WriteYAML(b, items(t, `[{"zeta": 1, "alpha": "text", "flag": "true", "list": ["a", {"beta": null}]}]`))
	})
	// cli-kit indents sequences, the outermost one included.
	want := `  - zeta: 1
    alpha: text
    flag: "true"
    list:
      - a
      - beta: null
`
	if got != want {
		t.Errorf("WriteYAML wrote:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteColumns(t *testing.T) {
	tests := []struct {
		fields string
		want   string
	}{
		{"id", "p-1\np-2\n"},
		{"id,name", "p-1\tRex\np-2\tTom\n"},
		{" id , , owner.name ,", "p-1\tAda\np-2\tBob\n"},
		// Booleans, numbers as spelled, nothing for null and for a missing field.
		{"vaccinated,age,weight,note,missing", "true\t3\t12.50\t\t\nfalse\t12\t\teats little at night\t\n"},
		// Lists and objects as one line of JSON.
		{"tags,owner", "[\"calm\", \"old\"]\t{\"name\": \"Ada\", \"city\": \"Linz\"}\n[]\t{\"name\": \"Bob\", \"city\": \"Graz\"}\n"},
		// A path falls back into the values of joined resources.
		{"clinic.name", "North\n\n"},
	}
	for _, tt := range tests {
		got := written(t, func(b *bytes.Buffer) error { return output.WriteColumns(b, items(t, pets), tt.fields) })
		if got != tt.want {
			t.Errorf("WriteColumns(%q) wrote %q, want %q", tt.fields, got, tt.want)
		}
	}

	if got := written(t, func(b *bytes.Buffer) error { return output.WriteColumns(b, items(t, `["text", 5]`), "id") }); got != "\n\n" {
		t.Errorf("items that are no objects wrote %q, want an empty line each", got)
	}
	if got := written(t, func(b *bytes.Buffer) error { return output.WriteColumns(b, nil, "id") }); got != "" {
		t.Errorf("no items wrote %q", got)
	}
	var buf bytes.Buffer
	err := output.WriteColumns(&buf, items(t, pets), "id,owner[name")
	if apikit.KindOf(err) != apikit.KindUsage || !strings.Contains(err.Error(), "output format columns") {
		t.Errorf("a broken path gave %v, want a usage error", err)
	}
}

func TestWriteIDs(t *testing.T) {
	byID := func(item *apikit.Object) (string, bool) {
		value, ok := item.Get("id")
		return apikit.Text(value), ok
	}
	if got := written(t, func(b *bytes.Buffer) error { return output.WriteIDs(b, items(t, pets), byID) }); got != "p-1\np-2\n" {
		t.Errorf("WriteIDs wrote %q", got)
	}
	// Later items without an ID leave their line empty, as their place in the list matters.
	if got := written(t, func(b *bytes.Buffer) error {
		return output.WriteIDs(b, items(t, `[{"id": "a"}, {"name": "b"}, "c"]`), byID)
	}); got != "a\n\n\n" {
		t.Errorf("WriteIDs with gaps wrote %q", got)
	}
	for name, source := range map[string]string{"first item without ID": `[{"name": "b"}, {"id": "a"}]`, "no objects": `["a"]`, "no items": `[]`} {
		if got := written(t, func(b *bytes.Buffer) error { return output.WriteIDs(b, items(t, source), byID) }); got != "" {
			t.Errorf("%s: WriteIDs wrote %q, want nothing", name, got)
		}
	}
}

func TestWriteNames(t *testing.T) {
	fields := []string{"name", "title", "description"}
	tests := map[string]string{
		pets: "Rex\nTom\n",
		`[{"title": "First", "description": "x"}, {"title": 2}, {"name": "ignored"}]`: "First\n2\n\n",
		`[{"description": "Only this"}]`:                                              "Only this\n",
		`[{"id": 1}]`:                                                                 "",
		`[]`:                                                                          "",
		`["text"]`:                                                                    "",
	}
	for source, want := range tests {
		if got := written(t, func(b *bytes.Buffer) error { return output.WriteNames(b, items(t, source), fields) }); got != want {
			t.Errorf("WriteNames(%s) wrote %q, want %q", source, got, want)
		}
	}
}

func TestWriteJSONPath(t *testing.T) {
	tests := []struct {
		expression string
		want       string
	}{
		{"$.name", "Rex\nTom\n"},
		{"$.owner.city", "Linz\nGraz\n"},
		// A filter picks from the items, as it would over the whole result.
		{`$[?(@.species == "cat")].id`, "p-2\n"},
		{`$[?(@.species == 'dog')].name`, "Rex\n"},
		{"$[?(@.age > 5)].name", "Tom\n"},
		{"$[?(@.vaccinated == true)].id", "p-1\n"},
		// The spelling of another dialect.
		{"$.[*].name", "Rex\nTom\n"},
		{"$[*].name", "Rex\nTom\n"},
		{"$.tags[*]", "calm\nold\n"},
		// Scalars as text: numbers as numbers, booleans in lower case, nothing for null.
		{"$.age", "3\n12\n"},
		{"$.vaccinated", "true\nfalse\n"},
		{"$.note", "\neats\tlittle\nat night\n"},
		{"$.missing", ""},
	}
	for _, tt := range tests {
		got := written(t, func(b *bytes.Buffer) error { return output.WriteJSONPath(b, items(t, pets), tt.expression) })
		if got != tt.want {
			t.Errorf("WriteJSONPath(%q) wrote %q, want %q", tt.expression, got, tt.want)
		}
	}
}

func TestWriteJSONPathWritesStructuresAsJSON(t *testing.T) {
	one := written(t, func(b *bytes.Buffer) error {
		return output.WriteJSONPath(b, items(t, pets), `$[?(@.id == "p-1")].owner`)
	})
	if want := "{\n  \"name\": \"Ada\",\n  \"city\": \"Linz\"\n}\n"; one != want {
		t.Errorf("one object wrote %q, want %q", one, want)
	}
	several := written(t, func(b *bytes.Buffer) error { return output.WriteJSONPath(b, items(t, pets), "$.owner") })
	if want := "[\n  {\n    \"name\": \"Ada\",\n    \"city\": \"Linz\"\n  },\n  {\n    \"name\": \"Bob\",\n    \"city\": \"Graz\"\n  }\n]\n"; several != want {
		t.Errorf("several objects wrote %q, want %q", several, want)
	}
	lists := written(t, func(b *bytes.Buffer) error { return output.WriteJSONPath(b, items(t, pets), "$.tags") })
	if want := "[\n  [\n    \"calm\",\n    \"old\"\n  ],\n  []\n]\n"; lists != want {
		t.Errorf("lists wrote %q, want %q", lists, want)
	}
	// The spelling of a number survives inside a structure.
	whole := written(t, func(b *bytes.Buffer) error {
		return output.WriteJSONPath(b, items(t, `[{"id": 1, "size": {"kg": 12.50}}]`), "$.size")
	})
	if want := "{\n  \"kg\": 12.50\n}\n"; whole != want {
		t.Errorf("an object with a number wrote %q, want %q", whole, want)
	}
}

func TestWriteJSONPathRejectsABrokenExpression(t *testing.T) {
	var buf bytes.Buffer
	err := output.WriteJSONPath(&buf, items(t, pets), "$.[")
	if apikit.KindOf(err) != apikit.KindUsage || !strings.Contains(err.Error(), `invalid JSONPath expression "$.["`) {
		t.Errorf("error = %v, want a usage error naming the expression", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q before failing", buf.String())
	}
}
