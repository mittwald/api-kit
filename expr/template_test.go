package expr_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/expr"
)

func braced(name string) expr.Placeholder {
	return expr.Placeholder{Name: name, Braced: true, Raw: "${" + name + "}"}
}

func plain(name string) expr.Placeholder {
	return expr.Placeholder{Name: name, Raw: "$" + name}
}

func TestParseTemplate(t *testing.T) {
	tests := []struct {
		template     string
		placeholders []expr.Placeholder
		// rendered is the template with <Name> in place of every placeholder.
		rendered string
	}{
		{"", nil, ""},
		{"no placeholders", nil, "no placeholders"},
		{"{name}", nil, "{name}"},

		// Plain placeholders: the longest ASCII identifier after the $.
		{"$name", []expr.Placeholder{plain("name")}, "<name>"},
		{"$Name", []expr.Placeholder{plain("Name")}, "<Name>"},
		{"$_x9", []expr.Placeholder{plain("_x9")}, "<_x9>"},
		{"$name_1.x", []expr.Placeholder{plain("name_1")}, "<name_1>.x"},
		{"$first-name", []expr.Placeholder{plain("first")}, "<first>-name"},
		{"$café", []expr.Placeholder{plain("caf")}, "<caf>é"},
		{"$a$b", []expr.Placeholder{plain("a"), plain("b")}, "<a><b>"},
		{"a${b}c", []expr.Placeholder{braced("b")}, "a<b>c"},
		{"a$bc", []expr.Placeholder{plain("bc")}, "a<bc>"},

		// Braced placeholders: whatever stands between the braces.
		{"${name}", []expr.Placeholder{braced("name")}, "<name>"},
		{"${order.id}", []expr.Placeholder{braced("order.id")}, "<order.id>"},
		{"${first-name}", []expr.Placeholder{braced("first-name")}, "<first-name>"},
		{"${items[sku=a-1].price}", []expr.Placeholder{braced("items[sku=a-1].price")}, "<items[sku=a-1].price>"},
		{"${ a b }", []expr.Placeholder{braced(" a b ")}, "< a b >"},
		{"${a\nb}", []expr.Placeholder{braced("a\nb")}, "<a\nb>"},
		{"${1}", []expr.Placeholder{braced("1")}, "<1>"},
		{"${$}", []expr.Placeholder{braced("$")}, "<$>"},
		{"${$$}", []expr.Placeholder{braced("$$")}, "<$$>"},
		{"${{}", []expr.Placeholder{braced("{")}, "<{>"},
		// The name ends at the first closing brace.
		{"${a${b}}", []expr.Placeholder{braced("a${b")}, "<a${b>}"},

		// $$ is a literal $, and hides what follows it.
		{"$$", nil, "$"},
		{"$$$$", nil, "$$"},
		{"$$name", nil, "$name"},
		{"$${name}", nil, "${name}"},
		{"$$$name", []expr.Placeholder{plain("name")}, "$<name>"},
		{"$$${name}", []expr.Placeholder{braced("name")}, "$<name>"},

		// Any other $ is literal text.
		{"total: $", nil, "total: $"},
		{"$5", nil, "$5"},
		{"$1st", nil, "$1st"},
		{"$-", nil, "$-"},
		{"$}", nil, "$}"},
		{"$ {name}", nil, "$ {name}"},
		{"${}", nil, "${}"},
		{"${}}", nil, "${}}"},
		{"${", nil, "${"},
		{"${name", nil, "${name"},
		{"${name $other", []expr.Placeholder{plain("other")}, "${name <other>"},

		// In order of appearance, duplicates included.
		{
			"x${a}y$b$$z${a}",
			[]expr.Placeholder{braced("a"), plain("b"), braced("a")},
			"x<a>y<b>$z<a>",
		},
	}
	everything := func(name string) (any, bool) { return "<" + name + ">", true }
	for _, test := range tests {
		template := expr.ParseTemplate(test.template)
		if got := template.String(); got != test.template {
			t.Errorf("ParseTemplate(%q).String() = %q", test.template, got)
		}
		if got := template.Placeholders(); !slices.Equal(got, test.placeholders) {
			t.Errorf("ParseTemplate(%q).Placeholders() = %+v, want %+v", test.template, got, test.placeholders)
		}
		if got, missing := template.Render(everything, expr.RenderOptions{}); got != test.rendered || missing != nil {
			t.Errorf("ParseTemplate(%q).Render() = %q, %v, want %q", test.template, got, missing, test.rendered)
		}
	}
}

func TestRender(t *testing.T) {
	values := map[string]any{"order": "o-17", "pet": "Rex", "first-name": "Ada"}
	tests := []struct {
		template string
		missing  expr.Missing
		want     string
		// reported lists the Raw text of the placeholders without a value.
		reported []string
	}{
		// Escapes and plain names side by side.
		{"$$5, $${order}, $order and $$TERM", expr.Keep, "$5, ${order}, o-17 and $TERM", nil},
		{"${first-name} owns $pet", expr.Keep, "Ada owns Rex", nil},

		// Keep leaves a placeholder without a value as it is written.
		{"Keep ${owner} and $owner", expr.Keep, "Keep ${owner} and $owner", nil},
		{"$pet and ${owner.name} owe $$5 to $shop", expr.Keep, "Rex and ${owner.name} owe $5 to $shop", nil},

		// Empty and Report render it as nothing.
		{"$pet and ${owner.name} owe $$5 to $shop", expr.Empty, "Rex and  owe $5 to ", nil},
		{
			"$pet and ${owner.name} owe $$5 to $shop",
			expr.Report,
			"Rex and  owe $5 to ",
			[]string{"${owner.name}", "$shop"},
		},
		// Both spellings of a name are reported, each time they appear.
		{"$TERM and ${TERM} and $TERM", expr.Report, " and  and ", []string{"$TERM", "${TERM}", "$TERM"}},
		{"${toys} ${pet.name}", expr.Report, " ", []string{"${toys}", "${pet.name}"}},
		{"$pet of ${order}", expr.Report, "Rex of o-17", nil},
		// An escaped placeholder uses no value, so none is missing.
		{"$${owner} $$owner", expr.Report, "${owner} $owner", nil},
	}
	for _, test := range tests {
		got, missing := expr.ParseTemplate(test.template).Render(lookupIn(values), expr.RenderOptions{Missing: test.missing})
		var reported []string
		for _, placeholder := range missing {
			reported = append(reported, placeholder.Raw)
		}
		if got != test.want || !slices.Equal(reported, test.reported) {
			t.Errorf("Render(%q, %d) = %q, %q, want %q, %q",
				test.template, test.missing, got, reported, test.want, test.reported)
		}
	}
}

func TestRenderReportsWholePlaceholders(t *testing.T) {
	_, missing := expr.ParseTemplate("${owner.name} $shop").Render(lookupIn(nil), expr.RenderOptions{Missing: expr.Report})
	want := []expr.Placeholder{braced("owner.name"), plain("shop")}
	if !slices.Equal(missing, want) {
		t.Errorf("missing = %+v, want %+v", missing, want)
	}
}

// A value is text, not a template: what looks like a placeholder or an
// escape in it comes out as it went in.
func TestRenderDoesNotScanValues(t *testing.T) {
	values := map[string]any{"a": "${b}", "b": "$$ $a ${c}", "c": "$"}
	for _, missing := range []expr.Missing{expr.Keep, expr.Empty, expr.Report} {
		got, reported := expr.ParseTemplate("${a} $b ${c}$$").Render(lookupIn(values), expr.RenderOptions{Missing: missing})
		if want := "${b} $$ $a ${c} $$"; got != want || reported != nil {
			t.Errorf("Render with policy %d = %q, %v, want %q", missing, got, reported, want)
		}
	}
}

func TestRenderLooksUpEachPlaceholderInOrder(t *testing.T) {
	var names []string
	lookup := func(name string) (any, bool) {
		names = append(names, name)
		return nil, false
	}
	expr.ParseTemplate("$b ${a.b} $$c ${first-name} $b").Render(lookup, expr.RenderOptions{})
	if want := []string{"b", "a.b", "first-name", "b"}; !slices.Equal(names, want) {
		t.Errorf("lookup was called with %q, want %q", names, want)
	}
}

func TestRenderFormat(t *testing.T) {
	values := map[string]any{
		"flag":  true,
		"count": json.Number("1.50"),
		"null":  nil,
		"tags":  []any{"red", json.Number("2")},
		"owner": decode(t, `{"name":"Ada","pets":["Rex"]}`),
	}
	template := expr.ParseTemplate("${flag}|$count|${null}|${tags}|${owner}|${gone}")

	// Without Format a value is shown as apikit.Text shows it. A null that
	// lookup returns is a value, not a missing one.
	got, missing := template.Render(lookupIn(values), expr.RenderOptions{Missing: expr.Report})
	if want := `true|1.50||["red", 2]|{"name": "Ada", "pets": ["Rex"]}|`; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
	if want := []expr.Placeholder{braced("gone")}; !slices.Equal(missing, want) {
		t.Errorf("missing = %+v, want %+v", missing, want)
	}

	// Format gets the value as lookup returned it, and is not asked about a
	// placeholder without a value.
	joined := func(value any) string {
		list, ok := value.([]any)
		if !ok {
			return strings.ToUpper(apikit.Text(value))
		}
		texts := make([]string, len(list))
		for i, item := range list {
			texts[i] = apikit.Text(item)
		}
		return strings.Join(texts, "+")
	}
	got, _ = template.Render(lookupIn(values), expr.RenderOptions{Format: joined})
	if want := `TRUE|1.50||red+2|{"NAME": "ADA", "PETS": ["REX"]}|${gone}`; got != want {
		t.Errorf("Render() with Format = %q, want %q", got, want)
	}
}

func TestSole(t *testing.T) {
	tests := []struct {
		template string
		name     string
		ok       bool
	}{
		{"${flag}", "flag", true},
		{"${order.id}", "order.id", true},
		{"${first-name}", "first-name", true},
		{"${ flag }", " flag ", true},
		// White space around the placeholder does not count.
		{"  ${flag}\t\n", "flag", true},

		{"", "", false},
		{"   ", "", false},
		{"flag", "", false},
		{"$flag", "", false},
		{"${flag}s", "", false},
		{"is ${flag}", "", false},
		{"${a}${b}", "", false},
		{"${a} ${a}", "", false},
		{"${a} $b", "", false},
		{"$$${flag}", "", false},
		{"$${flag}", "", false},
		{"${}", "", false},
		{"${flag", "", false},
	}
	for _, test := range tests {
		name, ok := expr.ParseTemplate(test.template).Sole()
		if name != test.name || ok != test.ok {
			t.Errorf("ParseTemplate(%q).Sole() = %q, %v, want %q, %v", test.template, name, ok, test.name, test.ok)
		}
	}
}

func TestZeroTemplate(t *testing.T) {
	var template expr.Template
	if got, missing := template.Render(lookupIn(nil), expr.RenderOptions{Missing: expr.Report}); got != "" || missing != nil {
		t.Errorf("Render() = %q, %v", got, missing)
	}
	if template.String() != "" || template.Placeholders() != nil {
		t.Error("the zero Template is not empty")
	}
}
