package definition_test

import (
	"slices"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// shop is the base definition the merge tests extend.
const shop = environments + `
output: {maxCharacters: 50}
resources:
  pets:
    filters: ["species=", "ownerId="]
    urlTemplate: {url: pets/view}
    setters:
      - {name: feed, aliases: [meal], parameters: [{name: food}]}
      - {name: groom}
`

func TestAdditiveMergeAddsButNeverChanges(t *testing.T) {
	doc, warnings, err := load(t, shop, `
output: {maxCharacters: 12}
resources:
  pets:
    filters: ["ownerId=", "color="]
    urlTemplate: {url: old/route, parameter: "${id}"}
  toys:
    filters: ["size="]
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("an overlay that only adds caused warnings: %v", warnings)
	}
	// A value the definition has keeps it...
	if doc.Output.MaxCharacters != 50 {
		t.Errorf("MaxCharacters = %d, want the base value 50", doc.Output.MaxCharacters)
	}
	pets := doc.Resource("pets")
	if pets.URLTemplate.URL != "pets/view" {
		t.Errorf("URL = %q, want the base value", pets.URLTemplate.URL)
	}
	// ...while new keys are taken over,
	if pets.URLTemplate.Parameter != "${id}" {
		t.Errorf("Parameter = %q, want the overlay's new key", pets.URLTemplate.Parameter)
	}
	if !doc.Resource("toys").Defined() {
		t.Error("the overlay's new resource is missing")
	}
	// ...and lists gain the entries they lack, in order.
	if got, want := pets.Filters, []string{"species=", "ownerId=", "color="}; !slices.Equal(got, want) {
		t.Errorf("Filters = %v, want %v", got, want)
	}
	var names []string
	for _, resource := range doc.Resources() {
		names = append(names, resource.Name)
	}
	if !slices.Equal(names, []string{"pets", "toys"}) {
		t.Errorf("resources in order = %v, want the base ones first", names)
	}
}

func TestOverrideMergeReplaces(t *testing.T) {
	doc, warnings, err := loadMode(t, definition.Override, shop, `
output: {maxCharacters: 12}
resources:
  pets:
    filters: ["color="]
    urlTemplate: {url: new/route}
`)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Load: %v, warnings %v", err, warnings)
	}
	pets := doc.Resource("pets")
	if doc.Output.MaxCharacters != 12 || pets.URLTemplate.URL != "new/route" || !slices.Equal(pets.Filters, []string{"color="}) {
		t.Errorf("MaxCharacters = %d, URL = %q, Filters = %v", doc.Output.MaxCharacters, pets.URLTemplate.URL, pets.Filters)
	}
	// Mappings are still merged key by key: what the overlay does not
	// mention stays.
	if len(pets.Setters) != 2 {
		t.Errorf("the setters the overlay does not mention are gone: %+v", pets.Setters)
	}
}

func TestAdditiveMergeWarnsAboutValuesOfAnotherType(t *testing.T) {
	doc, warnings, err := load(t, shop, "output: wide\nresources:\n  pets:\n    filters: species\n    urlTemplate: [a]\n")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, warning := range warnings {
		if warning.Severity != definition.Warning || warning.Source != "overlay.yaml" {
			t.Errorf("warning %+v has the wrong severity or source", warning)
		}
		got = append(got, warning.String())
	}
	want := []string{
		"output: ignored: the definition has a mapping here, the overlay a string",
		"resources.pets.filters: ignored: the definition has a list here, the overlay a string",
		"resources.pets.urlTemplate: ignored: the definition has a mapping here, the overlay a list",
	}
	if len(got) != len(want) {
		t.Fatalf("warnings = %q", got)
	}
	for i, prefix := range want {
		if !strings.HasPrefix(got[i], prefix) {
			t.Errorf("warning %d = %q, want it to start with %q", i, got[i], prefix)
		}
	}
	if doc.Output.MaxCharacters != 50 || len(doc.Resource("pets").Filters) != 2 {
		t.Error("an ignored value changed the definition")
	}
}

func TestOverrideMergeWithAWrongTypeFails(t *testing.T) {
	_, _, err := loadMode(t, definition.Override, shop, "output: wide\n")
	got := problemsOf(t, err)
	if len(got) != 1 || got[0] != "output: must be a mapping, not a string" {
		t.Errorf("problems = %q", got)
	}
	failure, _ := errorsAs(err)
	if failure.Source != "overlay.yaml" || failure.Problems[0].Source != "overlay.yaml" {
		t.Errorf("the problem is not attributed to the overlay: %+v", failure)
	}
}

func TestOverlayWithAnUnknownKeyFails(t *testing.T) {
	_, _, err := load(t, shop, "output: {customKey: kept}\n")
	wantProblems(t, err, "output.customKey: unknown key")
}

func TestMistakeInTheBaseIsReportedBeforeOverlaysAreRead(t *testing.T) {
	_, _, err := load(t, "environments: {}\n", "this is: [not valid")
	failure, ok := errorsAs(err)
	if !ok || failure.Source != "base.yaml" {
		t.Errorf("Load error = %v, want the problems of base.yaml", err)
	}
}

func TestAdditiveMergeLeavesOutSettersThatClash(t *testing.T) {
	tests := []struct {
		name    string
		overlay string
		want    []string // setter names after the merge
	}{
		{"a changed copy of a setter", `- {name: feed, aliases: [meal], parameters: [{name: food, help: Changed}]}`,
			[]string{"feed", "groom"}},
		{"an identical copy", `- {name: feed, aliases: [meal], parameters: [{name: food}]}`,
			[]string{"feed", "groom"}},
		{"a setter named like an alias", `- {name: meal}`,
			[]string{"feed", "groom"}},
		{"a setter with an alias that is a name", `- {name: wash, aliases: [groom]}`,
			[]string{"feed", "groom"}},
		{"a setter that shares an alias under another name", `- {name: wash, aliases: [meal]}`,
			[]string{"feed", "groom"}},
		{"a second setter of a name without parameters", `- {name: groom, parameters: [{name: brush}]}`,
			[]string{"feed", "groom"}},
		{"a second setter with a parameter of the first", `- {name: feed, parameters: [{name: bowl, aliases: [food]}]}`,
			[]string{"feed", "groom"}},
		{"a second setter with other parameters", `- {name: feed, parameters: [{name: bowl}]}`,
			[]string{"feed", "groom", "feed"}},
		{"a new setter", `- {name: wash}`,
			[]string{"feed", "groom", "wash"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := mustLoad(t, shop, "resources:\n  pets:\n    setters:\n      "+tt.overlay+"\n")
			var names []string
			for _, setter := range doc.Resource("pets").Setters {
				names = append(names, setter.Name)
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("setters = %v, want %v", names, tt.want)
			}
			if help := doc.Resource("pets").Setters[0].Parameters[0].Help; help != "" {
				t.Errorf("the base setter was changed: help = %q", help)
			}
		})
	}
}

func TestAdditiveMergeLeavesOutStepParametersThatClash(t *testing.T) {
	const base = environments + `
resources:
  pets:
    setters:
      - {name: feed, parameters: [{name: food}], stepParameters: [{name: role, default: keeper}]}
`
	tests := []struct {
		name    string
		overlay string
		want    int
	}{
		{"the same step parameter", `- {name: feed, parameters: [{name: bowl}], stepParameters: [{name: role, default: keeper}]}`, 2},
		{"a step parameter defined differently", `- {name: feed, parameters: [{name: bowl}], stepParameters: [{name: role, default: owner}]}`, 1},
		{"a parameter named like a step parameter", `- {name: feed, parameters: [{name: role}]}`, 1},
		{"a step parameter named like a parameter", `- {name: feed, parameters: [{name: bowl}], stepParameters: [{name: food, default: x}]}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := mustLoad(t, base, "resources:\n  pets:\n    setters:\n      "+tt.overlay+"\n")
			if got := len(doc.Resource("pets").Setters); got != tt.want {
				t.Errorf("%d setters after the merge, want %d", got, tt.want)
			}
		})
	}
}

func TestMergeDoesNotChangeItsArguments(t *testing.T) {
	parse := func(text string) *apikit.Object {
		value, err := apikit.Unmarshal([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return value.(*apikit.Object)
	}
	const baseText, overlayText = `{"a":{"list":[1,2],"keep":"x"},"b":1}`, `{"a":{"list":[2,3],"new":{"deep":[true]}},"c":null}`
	base, overlay := parse(baseText), parse(overlayText)

	merged, warnings := definition.Merge(base, overlay, definition.Additive, "overlay")
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	if got, want := apikit.Text(merged), `{"a": {"list": [1, 2, 3], "keep": "x", "new": {"deep": [true]}}, "b": 1, "c": null}`; got != want {
		t.Errorf("merged = %s\n  want   %s", got, want)
	}

	// Changing the result must not reach back into the arguments.
	section, _ := merged.Get("a")
	section.(*apikit.Object).Set("keep", "changed")
	added, _ := section.(*apikit.Object).Get("new")
	added.(*apikit.Object).Set("deep", "changed")
	for name, pair := range map[string][2]string{"base": {apikit.Text(base), apikit.Text(parse(baseText))}, "overlay": {apikit.Text(overlay), apikit.Text(parse(overlayText))}} {
		if pair[0] != pair[1] {
			t.Errorf("Merge changed its %s argument: %s", name, pair[0])
		}
	}

	replaced, _ := definition.Merge(base, overlay, definition.Override, "overlay")
	if got, want := apikit.Text(replaced), `{"a": {"list": [2, 3], "keep": "x", "new": {"deep": [true]}}, "b": 1, "c": null}`; got != want {
		t.Errorf("merged in override mode = %s\n  want %s", got, want)
	}
}

func TestMergeComparesListEntriesByValue(t *testing.T) {
	doc := mustLoad(t, environments+"http: {retryStatuses: [502, 503]}\n", "http: {retryStatuses: [503.0, 504]}\n")
	if got := doc.HTTP.RetryStatuses; !slices.Equal(got, []int{502, 503, 504}) {
		t.Errorf("RetryStatuses = %v, want 503.0 recognized as the 503 already there", got)
	}
}

// errorsAs returns the *definition.Error inside a load error.
func errorsAs(err error) (*definition.Error, bool) {
	for err != nil {
		if failure, ok := err.(*definition.Error); ok { //nolint:errorlint // walks the chain itself
			return failure, true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = unwrapper.Unwrap()
	}
	return nil, false
}
