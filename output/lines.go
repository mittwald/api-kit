package output

import (
	"io"
	"regexp"
	"strings"

	"github.com/mittwald/cli-kit/format"
	"github.com/mittwald/cli-kit/term"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// WriteJSON writes the items as a JSON list, indented by two spaces.
func WriteJSON(w io.Writer, items []any) error {
	return format.WriteJSON(w, items)
}

// WriteYAML writes the items as a YAML list. Keys keep the order the API
// returned them in.
func WriteYAML(w io.Writer, items []any) error {
	return format.WriteYAML(w, items)
}

// lineBreaks are the characters that would break a line of fields apart,
// and lineEnds those that would break a line of one value apart.
var (
	lineBreaks = regexp.MustCompile(`[\t\r\n]+`)
	lineEnds   = regexp.MustCompile(`[\r\n]+`)
)

// isTerminal reports whether a stream is a terminal. It is a variable so
// that a test can put a terminal where there is none.
var isTerminal = term.IsTerminal

// shown returns what a value goes through before it is written to w as it
// is: nothing where a program reads w, which gets the value byte for byte.
// On a terminal a person reads it, and a value is whatever the API returned:
// its escape sequences and control characters are left out there, as they
// could clear the screen, write to the clipboard or pass a word off as
// something the tool said. ls(1) hides control characters in file names on
// the same terms.
func shown(w io.Writer) func(string) string {
	if isTerminal(w) {
		return term.Plain
	}
	return func(value string) string { return value }
}

// writeValues writes each value on a line of its own; nothing for no
// values. A line break inside a value becomes a space, wherever w leads: a
// program that reads the lines would otherwise take one value for two, and
// a value could make up a line of its own, an ID among the IDs.
func writeValues(w io.Writer, values []string) error {
	show := shown(w)
	lines := make([]string, len(values))
	for i, value := range values {
		lines[i] = show(lineEnds.ReplaceAllString(value, " "))
	}
	return writeLines(w, lines)
}

// WriteColumns writes one line per item with the values of the given
// fields, separated by tabs, for scripts. fields is a comma-separated list
// of paths as [expr.ParsePath] reads them; a path may lead into the values
// of joined resources. A field an item lacks is empty, and tabs and line
// breaks inside a value become a space. Written to a terminal, a value's
// escape sequences and control characters are left out; a program gets the
// value as it is otherwise. A path that cannot be parsed is an error of kind
// [apikit.KindUsage].
func WriteColumns(w io.Writer, items []any, fields string) error {
	var paths []expr.Path
	for field := range strings.SplitSeq(fields, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		path, err := expr.ParsePath(field)
		if err != nil {
			return apikit.Errorf(apikit.KindUsage, "output format columns: %w", err)
		}
		paths = append(paths, path)
	}
	show := shown(w)
	lines := make([]string, len(items))
	for i, item := range items {
		values := make([]string, len(paths))
		if _, ok := item.(*apikit.Object); ok {
			for j, path := range paths {
				values[j] = show(lineBreaks.ReplaceAllString(apikit.Text(path.Get(item, definition.ExtraKey)), " "))
			}
		}
		lines[i] = strings.Join(values, "\t")
	}
	return writeLines(w, lines)
}

// WriteIDs writes the ID of each item on a line of its own. idOf returns
// the ID of an item and whether it has one. Nothing is written when the
// first item has no ID: the result is then not a list of things with IDs.
//
// A line break inside an ID becomes a space, so that every line is one ID.
// Written to a terminal, an ID's escape sequences and control characters
// are left out; a program gets it as it is otherwise. [WriteNames] and
// [WriteLinks] treat their values the same way.
func WriteIDs(w io.Writer, items []any, idOf func(*apikit.Object) (string, bool)) error {
	if first, ok := firstObject(items); !ok {
		return nil
	} else if _, ok := idOf(first); !ok {
		return nil
	}
	lines := make([]string, len(items))
	for i, item := range items {
		if object, ok := item.(*apikit.Object); ok {
			lines[i], _ = idOf(object)
		}
	}
	return writeValues(w, lines)
}

// WriteNames writes the name of each item on a line of its own. The name is
// the first of nameFields that the first item has; nothing is written when
// it has none of them.
func WriteNames(w io.Writer, items []any, nameFields []string) error {
	first, ok := firstObject(items)
	if !ok {
		return nil
	}
	for _, field := range nameFields {
		if !first.Has(field) {
			continue
		}
		lines := make([]string, len(items))
		for i, item := range items {
			if object, ok := item.(*apikit.Object); ok {
				value, _ := object.Get(field)
				lines[i] = apikit.Text(value)
			}
		}
		return writeValues(w, lines)
	}
	return nil
}

// WriteLinks writes the link of each item on a line of its own; an item
// without a link leaves its line empty. Nothing is written for no links.
func WriteLinks(w io.Writer, links []string) error {
	return writeValues(w, links)
}

// firstObject returns the first item when it is an object.
func firstObject(items []any) (*apikit.Object, bool) {
	if len(items) == 0 {
		return nil, false
	}
	object, ok := items[0].(*apikit.Object)
	return object, ok && object != nil
}

// writeLines writes each line followed by a line break; nothing for no
// lines.
func writeLines(w io.Writer, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}
