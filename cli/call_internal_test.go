package cli

import (
	"strings"
	"testing"

	"github.com/mittwald/api-kit/engine"
)

func TestDryRunLinesOnATerminalArePlain(t *testing.T) {
	// The YAML encoder writes control characters of a value as they are,
	// also in a block of several lines.
	for _, parameter := range []engine.ResolvedParam{
		{Name: "name", Value: "Rex\x1b]52;c;aGk=\x07"},
		{Name: "note", Value: "one\ntwo\x1b[2J"},
		{Name: "owner", Value: "11111111", Given: "Ada\x1b[31m"},
	} {
		line := dryRunLine(parameter)
		if !strings.Contains(line, "\x1b") {
			t.Fatalf("dryRunLine(%q) = %q: the encoder escapes now; shownLine may be needless", parameter.Value, line)
		}
		if plain := shownLine(line, true); strings.ContainsAny(plain, "\x1b\x07") || !strings.HasPrefix(plain, parameter.Name+":") {
			t.Errorf("on a terminal %q is shown as %q", line, plain)
		}
		if got := shownLine(line, false); got != line {
			t.Errorf("for a program %q is written as %q", line, got)
		}
	}
}
