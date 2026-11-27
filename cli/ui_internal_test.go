package cli

import (
	"io"
	"strings"
	"testing"
)

// Piped answers are read a line at a time, and nothing after them.
func TestPipedLinesLeaveTheRestOfTheInput(t *testing.T) {
	in := strings.NewReader("e1\r\nhunter2\nrest of the input")
	lines := &pipedLines{}
	for _, want := range []string{"e1", "hunter2"} {
		if got, err := lines.next(in); err != nil || got != want {
			t.Fatalf("next = %q, %v, want %q", got, err, want)
		}
	}
	rest, _ := io.ReadAll(in)
	if string(rest) != "rest of the input" {
		t.Errorf("rest = %q", rest)
	}
	if got, err := lines.next(strings.NewReader("last")); err != nil || got != "last" {
		t.Errorf("a last line without a break: %q, %v", got, err)
	}
	if _, err := lines.next(strings.NewReader("")); err == nil {
		t.Error("no line at all must fail")
	}
}
