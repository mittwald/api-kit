package output

import (
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

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
	// MaxCharacters is the width a value is shortened to, in the cells it
	// takes on a terminal, where a wide character such as 日 takes two; 0
	// means no limit.
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
	// Columns describe the columns of the table, in the order of its
	// headers: the values they replace, their format, alignment and style.
	// Nil, or a column without any, shows values as they are.
	Columns []definition.Column
	// CellFormats are the formats of the tool that columns name.
	CellFormats map[string]func(value any) string
	// Style is the cli-kit table style a drawn table takes. Empty means
	// the plain columns.
	Style string
}

// WriteTable writes a table. Columns that hold objects, or lists of
// anything but scalars, are left out: a table has no room for them. A list
// of scalars is joined with commas.
func WriteTable(w io.Writer, t Table, o TableOptions) error {
	if o.Now == nil {
		o.Now = time.Now
	}
	human := o.Format == Text || o.Format == Wide
	columns := o.simpleColumns(t)

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
		if style, ok := table.LookupStyle(o.Style); ok && o.Style != "" {
			options = append(options, table.WithStyle(style))
		}
	}
	aligns := make([]table.Align, len(columns))
	for i, column := range columns {
		switch o.column(column).Align {
		case definition.AlignRight:
			aligns[i] = table.AlignRight
		case definition.AlignCenter:
			aligns[i] = table.AlignCenter
		}
	}
	options = append(options, table.WithAlignment(aligns...))
	if human && o.Color {
		options = append(options, table.WithColumnStyles(o.columnStyles(columns, headers)...))
	}
	out := table.New(w, headers, options...)

	for rowIndex, row := range t.Rows {
		cells := make([]string, len(columns))
		for i, column := range columns {
			described := o.column(column)
			cell, formatted := o.formatted(described, row[column])
			if !human {
				cells[i] = cell
				continue
			}
			// A value is whatever the API returned, and a drawn table shows
			// its cells as they are: made plain first, an escape sequence
			// in a value does not reach the terminal, where it could clear
			// the screen or write to the clipboard. Records are plain text
			// already.
			cell = term.Plain(cell)
			if formatted {
				// A formatted value is what the definition wants shown: it
				// is not read as a time, only shortened.
				cell = o.shortened(cell)
			} else {
				cell = o.humanize(cell)
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

// columnStyles returns how the columns of a painted table are painted: in
// the style of the definition, else with its color rules, else in the
// next color of the palette, which leaves a cell painted already, such as
// an age, as it is.
func (o TableOptions) columnStyles(columns []int, headers []string) []table.ColumnStyle {
	styles := make([]table.ColumnStyle, len(columns))
	next := 0
	for i, header := range headers {
		steps := rules(o.Colors, header, o.Getenv)
		var fallback theme.Style
		if len(steps) == 0 && len(o.Palette.Columns) > 0 {
			fallback = o.Palette.Columns[next%len(o.Palette.Columns)]
			next++
		}
		switch o.column(columns[i]).Style {
		case definition.StyleBold:
			styles[i] = table.ColumnStyle{Style: theme.Bold}
		case definition.StyleItalic:
			styles[i] = table.ColumnStyle{Style: theme.Italic}
		default:
			styles[i] = table.ColumnStyle{Style: fallback, Rules: steps}
		}
	}
	return styles
}

// simpleColumns returns the indexes of the columns a table can show: those
// with a format, and those in which no cell is an object or a list that
// holds objects or lists.
func (o TableOptions) simpleColumns(t Table) []int {
	var columns []int
	for column := range t.Headers {
		// A format of the tool writes whatever the column holds.
		if format := o.column(column).Format; format != "" && o.CellFormats[format] != nil {
			columns = append(columns, column)
			continue
		}
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

// column returns the description of the column at index i of the table,
// the zero Column when there is none.
func (o TableOptions) column(i int) definition.Column {
	if i < len(o.Columns) {
		return o.Columns[i]
	}
	return definition.Column{}
}

// formatted returns the text of a cell of column: the text its Values give
// the value, or what its format writes, or the value as it is. It reports
// whether the column changed the value.
func (o TableOptions) formatted(column definition.Column, value any) (string, bool) {
	if len(column.Values) > 0 {
		text := cellText(value)
		for _, pair := range column.Values {
			if pair.Key == text {
				return pair.Value, true
			}
		}
	}
	if format, ok := o.CellFormats[column.Format]; ok && column.Format != "" {
		return format(value), true
	}
	return cellText(value), false
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
// becomes an age, painted in the colors of the palette for ages, and a
// long value is shortened.
func (o TableOptions) humanize(cell string) string {
	if at, ok := format.ParseTimestamp(cell); ok {
		now := o.Now()
		age := format.Ago(at, now)
		if !o.Color {
			return age
		}
		if distance := now.Sub(at); distance.Abs() < time.Hour {
			return o.Palette.FreshAge.Paint(age)
		}
		return o.Palette.Age.Paint(age)
	}
	return o.shortened(cell)
}

// shortened cuts a long value of a cell to the most characters a cell may
// have, unless the table shows whole values or the value is one to keep
// whole.
func (o TableOptions) shortened(cell string) string {
	// A shortened value stays on one line, so a value is measured as that
	// line, in the cells a terminal gives it: a wide character takes two.
	line := strings.ReplaceAll(cell, "\n", " ")
	if !o.Ellipsis || o.MaxCharacters <= 0 || term.VisibleWidth(line) <= o.MaxCharacters {
		return cell
	}
	if slices.ContainsFunc(o.KeepWhole, func(pattern *regexp.Regexp) bool { return pattern.MatchString(cell) }) {
		return cell
	}
	return term.Truncate(line, o.MaxCharacters)
}
