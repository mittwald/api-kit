package output

import (
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mittwald/cli-kit/format"
	"github.com/mittwald/cli-kit/table"
	"github.com/mittwald/cli-kit/term"
	"github.com/mittwald/cli-kit/theme"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// Table is a result laid out in columns, with the values as the API and
// the joins delivered them.
type Table struct {
	// Headers are the headings of the columns.
	Headers []string
	// Rows hold one value per column: a scalar, a list or an object.
	Rows [][]any
	// Links hold the link of each row, "" for a row without one. Nil means
	// the rows have no links.
	Links []string
}

// TableOptions say how a [Table] is written.
type TableOptions struct {
	// Format is one of [Text], [Wide], [CSV], [TSV] and [Markdown]. Text
	// and Wide are for people: values are shortened, points in time are
	// shown as ages, and cells are painted. The others are for programs
	// and documents and carry the values as they are.
	Format Format
	// Color paints headings and cells.
	Color bool
	// Links makes the first cell of a row a hyperlink to the row's link.
	Links bool
	// Ellipsis shortens long values.
	Ellipsis bool
	// MaxCharacters is the length a value is shortened to; 0 means no
	// limit.
	MaxCharacters int
	// KeepWhole match values that are never shortened, such as IDs.
	KeepWhole []*regexp.Regexp
	// Colors are the color rules of the resource.
	Colors []definition.ColorSet
	// Palette holds the colors for everything else.
	Palette Palette
	// Getenv reads the environment variables that color rules name. Nil
	// means none is set.
	Getenv func(string) string
	// Now is the time ages are counted from. Nil means time.Now.
	Now func() time.Time
}

// WriteTable writes a table. Columns that hold objects, or lists of
// anything but scalars, are left out: a table has no room for them. A list
// of scalars is joined with commas.
func WriteTable(w io.Writer, t Table, o TableOptions) error {
	if o.Now == nil {
		o.Now = time.Now
	}
	human := o.Format == Text || o.Format == Wide
	columns := simpleColumns(t)

	headers := make([]string, len(columns))
	for i, column := range columns {
		headers[i] = t.Headers[column]
		if human {
			headers[i] = term.PlainLine(headers[i])
		}
	}
	options := []table.Option{table.UppercaseHeaders(), table.WithColor(human && o.Color)}
	switch o.Format {
	case CSV:
		options = append(options, table.WithFormat(format.CSV))
	case TSV:
		options = append(options, table.WithFormat(format.TSV))
	case Markdown:
		options = append(options, table.WithFormat(format.Markdown))
	default:
		look := theme.Active()
		look.Header = o.Palette.Header
		options = append(options, table.WithTheme(look))
	}
	out := table.New(w, headers, options...)

	// Columns without color rules are painted in turn with the colors of
	// the palette.
	steps := make([][]painter, len(columns))
	fallback := make([]theme.Style, len(columns))
	if human && o.Color {
		next := 0
		for i, header := range headers {
			steps[i] = painters(o.Colors, header, o.Getenv)
			if len(steps[i]) == 0 && len(o.Palette.Columns) > 0 {
				fallback[i] = o.Palette.Columns[next%len(o.Palette.Columns)]
				next++
			}
		}
	}

	for rowIndex, row := range t.Rows {
		cells := make([]string, len(columns))
		for i, column := range columns {
			cell := cellText(row[column])
			if !human {
				cells[i] = cell
				continue
			}
			// A value is whatever the API returned, and a drawn table shows
			// its cells as they are: made plain first, an escape sequence
			// in a value does not reach the terminal, where it could clear
			// the screen or write to the clipboard. Records are plain text
			// already.
			cell, painted := o.humanize(term.Plain(cell))
			if o.Color {
				switch {
				case len(steps[i]) > 0:
					cell = paint(cell, steps[i])
				case !painted:
					cell = fallback[i].Paint(cell)
				}
			}
			if i == 0 && o.Links && rowIndex < len(t.Links) && cell != "" {
				cell = term.Hyperlink(t.Links[rowIndex], cell)
			}
			cells[i] = cell
		}
		out.Append(cells...)
	}
	return out.Render()
}

// simpleColumns returns the indexes of the columns a table can show: those
// in which no cell is an object or a list that holds objects or lists.
func simpleColumns(t Table) []int {
	var columns []int
	for column := range t.Headers {
		nested := slices.ContainsFunc(t.Rows, func(row []any) bool {
			switch cell := row[column].(type) {
			case *apikit.Object:
				return cell != nil
			case []any:
				return slices.ContainsFunc(cell, func(item any) bool {
					switch item.(type) {
					case *apikit.Object, []any:
						return true
					}
					return false
				})
			}
			return false
		})
		if !nested {
			columns = append(columns, column)
		}
	}
	return columns
}

// cellText returns a value as the text of a cell. The items of a list are
// joined with commas, leaving out those that are nothing.
func cellText(value any) string {
	list, ok := value.([]any)
	if !ok {
		return apikit.Text(value)
	}
	var parts []string
	for _, item := range list {
		if !apikit.Falsy(item) {
			parts = append(parts, apikit.Text(item))
		}
	}
	return strings.Join(parts, ", ")
}

// humanize prepares the text of a cell for a person: a point in time
// becomes an age, a long value is shortened. It reports whether the result
// is painted already.
func (o TableOptions) humanize(cell string) (string, bool) {
	if at, ok := parseTimestamp(cell); ok {
		now := o.Now()
		age := Age(at, now)
		if !o.Color {
			return age, false
		}
		if distance := now.Sub(at); distance.Abs() < time.Hour {
			return o.Palette.FreshAge.Paint(age), true
		}
		return o.Palette.Age.Paint(age), true
	}
	if !o.Ellipsis || o.MaxCharacters <= 0 || utf8.RuneCountInString(cell) <= o.MaxCharacters {
		return cell, false
	}
	if slices.ContainsFunc(o.KeepWhole, func(pattern *regexp.Regexp) bool { return pattern.MatchString(cell) }) {
		return cell, false
	}
	// A shortened value stays on one line.
	return term.Truncate(strings.ReplaceAll(cell, "\n", " "), o.MaxCharacters), false
}
