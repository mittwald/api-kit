package output

import (
	"bytes"
	"io"
	"testing"

	apikit "github.com/mittwald/api-kit"
)

// onTerminal makes every stream a terminal, or none, for a test.
func onTerminal(t *testing.T, terminal bool) {
	t.Helper()
	previous := isTerminal
	isTerminal = func(any) bool { return terminal }
	t.Cleanup(func() { isTerminal = previous })
}

// hostile is a result as an API could return it: values that carry an
// escape sequence, a control character and a line break.
const hostile = `[
  {"id": "p-1\u001b]52;c;ZXZpbA==\u0007", "name": "Rex\u001b[2J\u009b31m", "note": "one\ttwo\nthree\u001b[0m"},
  {"id": "p-2\nadmin", "name": "Tom\r\nroot", "note": "plain"}
]`

func hostileItems(t *testing.T) []any {
	t.Helper()
	value, err := apikit.Unmarshal([]byte(hostile))
	if err != nil {
		t.Fatal(err)
	}
	return value.([]any)
}

func idOf(item *apikit.Object) (string, bool) {
	id, ok := item.Get("id")
	return apikit.Text(id), ok
}

// A program gets every value byte for byte: a script that compares a name,
// or hands an ID back to the tool, needs the value and not something like
// it. Only a line break inside a value becomes a space, so that a value can
// never make up a line of its own.
func TestLineFormatsGiveProgramsTheValues(t *testing.T) {
	onTerminal(t, false)
	items := hostileItems(t)
	links := []string{"https://example.com/p-1\x1b[2J", "https://example.com/p-2\nhttps://example.com/admin"}
	for name, tt := range map[string]struct {
		write func(io.Writer) error
		want  string
	}{
		"id": {
			func(w io.Writer) error { return WriteIDs(w, items, idOf) },
			"p-1\x1b]52;c;ZXZpbA==\x07\np-2 admin\n",
		},
		"name": {
			func(w io.Writer) error { return WriteNames(w, items, []string{"name"}) },
			"Rex\x1b[2J\u009b31m\nTom root\n",
		},
		"link": {
			func(w io.Writer) error { return WriteLinks(w, links) },
			"https://example.com/p-1\x1b[2J\nhttps://example.com/p-2 https://example.com/admin\n",
		},
		"columns": {
			func(w io.Writer) error { return WriteColumns(w, items, "id,note") },
			"p-1\x1b]52;c;ZXZpbA==\x07\tone two three\x1b[0m\np-2 admin\tplain\n",
		},
		// An expression takes a value out as it is, line breaks included.
		"jsonpath": {
			func(w io.Writer) error { return WriteJSONPath(w, items, "$.note") },
			"one\ttwo\nthree\x1b[0m\nplain\n",
		},
	} {
		var out bytes.Buffer
		if err := tt.write(&out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := out.String(); got != tt.want {
			t.Errorf("%s wrote %q, want %q", name, got, tt.want)
		}
	}
}

// On a terminal a person reads the lines, and no value reaches it as an
// escape sequence or a control character: it could clear the screen, write
// to the clipboard or pass a word off as the tool's own.
func TestLineFormatsShowValuesPlainOnATerminal(t *testing.T) {
	onTerminal(t, true)
	items := hostileItems(t)
	links := []string{"https://example.com/p-1\x1b[2J", "https://example.com/p-2\nhttps://example.com/admin"}
	for name, tt := range map[string]struct {
		write func(io.Writer) error
		want  string
	}{
		"id": {
			func(w io.Writer) error { return WriteIDs(w, items, idOf) },
			"p-1\np-2 admin\n",
		},
		"name": {
			func(w io.Writer) error { return WriteNames(w, items, []string{"name"}) },
			"Rex\nTom root\n",
		},
		"link": {
			func(w io.Writer) error { return WriteLinks(w, links) },
			"https://example.com/p-1\nhttps://example.com/p-2 https://example.com/admin\n",
		},
		"columns": {
			func(w io.Writer) error { return WriteColumns(w, items, "id,note") },
			"p-1\tone two three\np-2 admin\tplain\n",
		},
		"jsonpath": {
			func(w io.Writer) error { return WriteJSONPath(w, items, "$.name") },
			"Rex\nTom\nroot\n",
		},
	} {
		var out bytes.Buffer
		if err := tt.write(&out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := out.String(); got != tt.want {
			t.Errorf("%s wrote %q, want %q", name, got, tt.want)
		}
	}
}

// What an expression selects as an object or a list is written as JSON,
// which writes control characters as escapes wherever it goes.
func TestJSONPathWritesStructuresAsJSON(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		onTerminal(t, terminal)
		var out bytes.Buffer
		if err := WriteJSONPath(&out, hostileItems(t), "$[0]"); err != nil {
			t.Fatal(err)
		}
		if bytes.ContainsAny(out.Bytes(), "\x1b\x07") {
			t.Errorf("terminal %v: a control character reached the output: %q", terminal, out.String())
		}
	}
}
