package expr

import (
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// A Template is text with placeholders, parsed once by [ParseTemplate] and
// rendered as often as needed. The zero Template is the empty text.
//
// A $ starts one of four things, tried in this order:
//
//   - $$ is a literal $.
//   - ${ followed by at least one character and a } is a braced placeholder.
//     Its name is the text between the braces, whatever it contains: dots,
//     dashes, brackets, white space. The name ends at the first }.
//   - $ followed by an identifier is a plain placeholder. An identifier
//     starts with an ASCII letter or an underscore, continues with ASCII
//     letters, digits and underscores, and is as long as possible.
//   - Any other $ is literal text, including the $ of ${} and of a ${ that
//     is never closed.
type Template struct {
	source string
	parts  []part
}

// A part is a run of literal text or one placeholder.
type part struct {
	// literal is text that is rendered as it is; $$ is already a single $.
	literal string
	// placeholder is set, with a Raw that is not empty, for a placeholder.
	placeholder Placeholder
}

func (p part) isPlaceholder() bool { return p.placeholder.Raw != "" }

// A Placeholder is a place in a template that a value goes to.
type Placeholder struct {
	// Name is what the value is looked up by: the identifier of $name, and
	// exactly the text between the braces of ${...}.
	Name string
	// Braced reports whether the placeholder is written as ${...}.
	Braced bool
	// Raw is the placeholder as written in the template, for example
	// "${a-b}" or "$name".
	Raw string
}

// ParseTemplate parses s. It never fails: whatever is not a placeholder is
// literal text.
func ParseTemplate(s string) Template {
	t := Template{source: s}
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			t.parts = append(t.parts, part{literal: literal.String()})
			literal.Reset()
		}
	}
	rest := s
	for {
		dollar := strings.IndexByte(rest, '$')
		if dollar < 0 {
			literal.WriteString(rest)
			break
		}
		literal.WriteString(rest[:dollar])
		rest = rest[dollar:]

		placeholder, size := lexDollar(rest)
		if placeholder.Raw != "" {
			flush()
			t.parts = append(t.parts, part{placeholder: placeholder})
		} else {
			literal.WriteByte('$')
		}
		rest = rest[size:]
	}
	flush()
	return t
}

// lexDollar reads what the $ at the start of s begins and returns how many
// bytes it takes up. The placeholder is empty when the $ is literal text: a
// $$, which takes two bytes, or a $ that starts nothing, which takes one.
func lexDollar(s string) (placeholder Placeholder, size int) {
	after := s[1:]
	if strings.HasPrefix(after, "$") {
		return Placeholder{}, 2
	}
	if strings.HasPrefix(after, "{") {
		// closing is 1 for ${}, which names nothing.
		if closing := strings.IndexByte(after, '}'); closing > 1 {
			size = closing + 2
			return Placeholder{Name: after[1:closing], Braced: true, Raw: s[:size]}, size
		}
		return Placeholder{}, 1
	}
	if length := identifierLength(after); length > 0 {
		size = length + 1
		return Placeholder{Name: after[:length], Raw: s[:size]}, size
	}
	return Placeholder{}, 1
}

// identifierLength returns the length of the longest identifier s starts
// with, 0 when it starts with none.
func identifierLength(s string) int {
	for i := range len(s) {
		c := s[i]
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		digit := c >= '0' && c <= '9'
		if !letter && (!digit || i == 0) {
			return i
		}
	}
	return len(s)
}

// String returns the template as written.
func (t Template) String() string { return t.source }

// Placeholders returns the placeholders of the template, braced and plain
// ones, in order of appearance. A placeholder that is used twice is listed
// twice. The result is nil for a template without placeholders.
func (t Template) Placeholders() []Placeholder {
	var placeholders []Placeholder
	for _, p := range t.parts {
		if p.isPlaceholder() {
			placeholders = append(placeholders, p.placeholder)
		}
	}
	return placeholders
}

// Sole reports whether the template is one braced placeholder and nothing
// else but white space around it, and returns the name of that placeholder.
// A caller uses it to pass a value on with its type, a boolean or a list for
// example, where rendering would turn it into text.
func (t Template) Sole() (name string, ok bool) {
	for _, p := range t.parts {
		switch {
		case !p.isPlaceholder():
			if strings.TrimSpace(p.literal) != "" {
				return "", false
			}
		case ok || !p.placeholder.Braced:
			return "", false
		default:
			name, ok = p.placeholder.Name, true
		}
	}
	return name, ok
}

// Missing says what [Template.Render] does with a placeholder that has no
// value.
type Missing uint8

// The ways to treat a placeholder without a value.
const (
	// Keep leaves the placeholder in the output as it is written.
	Keep Missing = iota
	// Empty renders the placeholder as nothing.
	Empty
	// Report renders the placeholder as nothing and lists it in the second
	// result of [Template.Render].
	Report
)

// RenderOptions controls [Template.Render]. The zero value keeps
// placeholders without a value and formats values with [apikit.Text].
type RenderOptions struct {
	// Missing says what happens to a placeholder without a value.
	Missing Missing
	// Format returns the text that replaces a placeholder for its value.
	// Nil means [apikit.Text].
	Format func(any) string
}

// Render returns the template with every placeholder replaced by the value
// lookup returns for its Name. lookup is called once for each placeholder,
// in order of appearance; its second result reports whether there is a
// value. A placeholder without a value is treated as o.Missing says. With
// [Report] the second result lists these placeholders in order of
// appearance, one that is used twice is listed twice; otherwise it is nil.
//
// Rendering is a single pass: the text of a value is never read as a
// template, so a value that contains ${x} or $$ comes out exactly as it is.
func (t Template) Render(lookup func(name string) (any, bool), o RenderOptions) (out string, missing []Placeholder) {
	format := o.Format
	if format == nil {
		format = apikit.Text
	}
	var b strings.Builder
	for _, p := range t.parts {
		if !p.isPlaceholder() {
			b.WriteString(p.literal)
			continue
		}
		value, ok := lookup(p.placeholder.Name)
		switch {
		case ok:
			b.WriteString(format(value))
		case o.Missing == Keep:
			b.WriteString(p.placeholder.Raw)
		case o.Missing == Report:
			missing = append(missing, p.placeholder)
		}
	}
	return b.String(), missing
}
