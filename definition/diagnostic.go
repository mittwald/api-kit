package definition

import (
	"fmt"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// Severity says how serious a [Diagnostic] is.
type Severity uint8

// The severities of a diagnostic.
const (
	// Warning marks something that was ignored; the definition still loads.
	Warning Severity = iota + 1
	// Problem marks a mistake; the definition does not load.
	Problem
)

// Diagnostic is one finding about a definition document.
type Diagnostic struct {
	// Severity says whether the definition still loads.
	Severity Severity
	// Source names the document the finding is about, as [Source.Name] gave
	// it. It is empty when several documents are involved.
	Source string
	// Path leads to the key the finding is about, such as
	// "resources.pets.setters[2].parameters[0]". It is empty for a finding
	// about the document as a whole.
	Path string
	// Message says what is wrong, without the path.
	Message string
}

// String returns the finding on one line: the path, then the message.
func (d Diagnostic) String() string {
	if d.Path == "" {
		return d.Message
	}
	return d.Path + ": " + d.Message
}

// Error is returned by [Load] for a definition with mistakes in it. Load
// wraps it in an [*apikit.Error] of kind [apikit.KindDefinition], whose
// message lists every problem.
type Error struct {
	// Source names the document, or documents, the problems are in.
	Source string
	// Problems are all mistakes found, in document order.
	Problems []Diagnostic
}

// Error lists the problems, one per line.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "invalid definition in %s:", e.Source)
	for _, problem := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(problem.String())
	}
	return b.String()
}

// newError wraps the problems found in source into the error [Load] returns.
func newError(source string, problems []Diagnostic) error {
	err := &Error{Source: source, Problems: problems}
	return &apikit.Error{Kind: apikit.KindDefinition, Msg: err.Error(), Err: err}
}

// join appends a key to a path.
func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// index appends a list index to a path.
func index(path string, i int) string {
	return fmt.Sprintf("%s[%d]", path, i)
}
