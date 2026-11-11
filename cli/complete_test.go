package cli_test

import (
	"slices"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
)

// complete asks the tool what it offers for the last word of a command
// line, and returns the words (without their descriptions) and whether the
// shell is told to add no space after the word.
func complete(t *testing.T, h *tool, words ...string) (offers []string, noSpace bool) {
	t.Helper()
	stdout, stderr, code := h.runBare(append([]string{"__complete"}, words...)...)
	if code != apikit.ExitOK {
		t.Fatalf("completion of %v: exit code = %d, stderr = %q", words, code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	directive := lines[len(lines)-1]
	if !strings.HasPrefix(directive, ":") {
		t.Fatalf("completion of %v does not end with a directive:\n%s", words, stdout)
	}
	for _, line := range lines[:len(lines)-1] {
		word, _, _ := strings.Cut(line, "\t")
		offers = append(offers, word)
	}
	// 2 is cobra's ShellCompDirectiveNoSpace.
	return offers, slices.Contains([]string{":6", ":2"}, directive)
}

func TestCompleteGetArguments(t *testing.T) {
	h := newTool(t)

	// Resources that can be listed on their own.
	offers, _ := complete(t, h, "get", "")
	if !slices.Equal(offers, []string{"owners", "pets", "breeds", "orders"}) {
		t.Errorf("resources = %v", offers)
	}
	if offers, _ = complete(t, h, "get", "o"); !slices.Equal(offers, []string{"owners", "orders"}) {
		t.Errorf("resources starting with o = %v", offers)
	}
	// Nothing for an ID, and no file names either.
	if offers, _ = complete(t, h, "get", "pets", ""); len(offers) != 0 {
		t.Errorf("offers for an ID = %v", offers)
	}
	// What belongs to an item of the resource, also when it is named in
	// the singular.
	for _, resource := range []string{"pets", "pet"} {
		if offers, _ = complete(t, h, "get", resource, "0a1b2c3d", ""); !slices.Equal(offers, []string{"visits"}) {
			t.Errorf("sub-resources of %s = %v", resource, offers)
		}
	}
	if offers, _ = complete(t, h, "get", "owners", "0a1b2c3d", ""); len(offers) != 0 {
		t.Errorf("sub-resources of owners = %v", offers)
	}
}

func TestCompleteFilters(t *testing.T) {
	h := newTool(t)
	h.overlay("filters: [\"search=\"]\nresources:\n  pets:\n    filters: [\"species=dog\", \"species=cat\"]\n")

	offers, noSpace := complete(t, h, "get", "pets", "--filter", "")
	if !slices.Equal(offers, []string{"search=", "species=", "ownerId=", "species=dog", "species=cat"}) || noSpace {
		t.Errorf("filters of pets = %v, no space = %v", offers, noSpace)
	}
	// A key that wants its value gets no space after it.
	if offers, noSpace = complete(t, h, "get", "pets", "-f", "own"); !slices.Equal(offers, []string{"ownerId="}) || !noSpace {
		t.Errorf("a filter key = %v, no space = %v", offers, noSpace)
	}
	// After a comma the next filter is completed.
	if offers, _ = complete(t, h, "get", "pets", "-f", "ownerId=1,species=d"); !slices.Equal(offers, []string{"ownerId=1,species=dog"}) {
		t.Errorf("the second filter = %v", offers)
	}
	// With a sub-resource, its filters.
	if offers, _ = complete(t, h, "get", "pets", "0a1b2c3d", "visits", "-f", ""); !slices.Equal(offers, []string{"search=", "reason="}) {
		t.Errorf("filters of visits = %v", offers)
	}
	// Without a resource yet, the ones every resource has.
	if offers, _ = complete(t, h, "get", "-f", ""); !slices.Equal(offers, []string{"search="}) {
		t.Errorf("filters without a resource = %v", offers)
	}
}

func TestCompleteOutputFormats(t *testing.T) {
	h := newTool(t)
	offers, noSpace := complete(t, h, "get", "pets", "-o", "")
	want := []string{"text", "wide", "json", "json-raw", "yaml", "jsonpath=", "columns=", "id", "name", "link", "csv", "tsv", "markdown"}
	if !slices.Equal(offers, want) || noSpace {
		t.Errorf("formats = %v, no space = %v", offers, noSpace)
	}
	if offers, _ = complete(t, h, "get", "pets", "--output", "j"); !slices.Equal(offers, []string{"json", "json-raw", "jsonpath="}) {
		t.Errorf("formats starting with j = %v", offers)
	}
	// A format that takes an argument gets no space after its equals sign.
	if offers, noSpace = complete(t, h, "config", "-o", "col"); !slices.Equal(offers, []string{"columns="}) || !noSpace {
		t.Errorf("a format with an argument = %v, no space = %v", offers, noSpace)
	}

	stdout, _, _ := h.runBare("__complete", "get", "pets", "-o", "w")
	if !strings.HasPrefix(stdout, "wide\ttable of every field\n") {
		t.Errorf("a format is offered without its description:\n%s", stdout)
	}
}

func TestCompleteOptionValues(t *testing.T) {
	h := newTool(t)
	for _, option := range []string{"--color", "--links"} {
		if offers, _ := complete(t, h, "get", option+"="); !slices.Equal(offers, []string{option + "=auto", option + "=always", option + "=never"}) &&
			!slices.Equal(offers, []string{"auto", "always", "never"}) {
			t.Errorf("values of %s = %v", option, offers)
		}
	}
	// Options whose value is free text offer nothing, and no file names.
	if offers, _ := complete(t, h, "get", "pets", "--token", ""); len(offers) != 0 {
		t.Errorf("offers for --token = %v", offers)
	}
}
