package output

import (
	"regexp"
	"strconv"

	"github.com/mittwald/cli-kit/theme"

	"github.com/mittwald/api-kit/definition"
)

// ParseStyle reads a color specification: parts separated by colons, each
// a color or a text attribute, as in "245", "160:bold" or
// "fg=white:bg=#aa0000:underline". It is theme.ParseStyle of cli-kit, which
// documents the colors and attributes it knows. A hex color is painted as
// it is where the COLORTERM environment variable announces 24-bit colors
// (truecolor or 24bit), and as the nearest of the 256 colors everywhere
// else. The result is false when nothing is left of the specification, so
// a mistyped one paints nothing.
//
// Deprecated: Use theme.ParseStyle.
func ParseStyle(spec string) (theme.Style, bool) {
	return theme.ParseStyle(spec)
}

// Palette holds the colors a table is painted with besides those the
// definition gives.
type Palette struct {
	// Header paints the headings.
	Header theme.Style
	// Columns paint the columns that have no colors of their own, in turn.
	Columns []theme.Style
	// Age paints a point in time shown as an age.
	Age theme.Style
	// FreshAge paints an age of less than an hour.
	FreshAge theme.Style
}

// Specifications of the default palette.
const (
	defaultHeader   = "248:bold"
	defaultAge      = "245"
	defaultFreshAge = "40:bold"
)

var defaultColumns = []string{"38", "42", "222", "104", "202", "207"}

// NewPalette returns the default palette with the changes the environment
// asks for. getenv reads a variable; prefix starts the names of the
// variables: <prefix>_COLOR_TABLE_HEADER, <prefix>_COLOR_TABLE_COLUMN_1 to
// _6, <prefix>_TEXT_COLOR_DURATION and <prefix>_TEXT_DURATION_FRESH, each a
// color specification as theme.ParseStyle reads it. An empty prefix or a nil
// getenv gives the defaults.
func NewPalette(getenv func(string) string, prefix string) Palette {
	spec := func(suffix, def string) theme.Style {
		if getenv != nil && prefix != "" {
			if style, ok := theme.ParseStyle(getenv(prefix + suffix)); ok {
				return style
			}
		}
		style, _ := theme.ParseStyle(def)
		return style
	}
	palette := Palette{
		Header:   spec("_COLOR_TABLE_HEADER", defaultHeader),
		Age:      spec("_TEXT_COLOR_DURATION", defaultAge),
		FreshAge: spec("_TEXT_DURATION_FRESH", defaultFreshAge),
	}
	for i, def := range defaultColumns {
		palette.Columns = append(palette.Columns, spec("_COLOR_TABLE_COLUMN_"+strconv.Itoa(i+1), def))
	}
	return palette
}

// painter is one step of painting a cell: the whole cell in a color, or
// the parts of it a pattern matches.
type painter struct {
	pattern *regexp.Regexp // nil paints the whole cell
	style   theme.Style
}

// painters returns the steps that paint the cells of the column with that
// heading, in the order they apply: for every set of colors that names the
// column, its color for the whole cell, then its rules.
func painters(colors []definition.ColorSet, label string, getenv func(string) string) []painter {
	resolve := func(color, env string) (theme.Style, bool) {
		if env != "" && getenv != nil {
			if style, ok := theme.ParseStyle(getenv(env)); ok {
				return style, true
			}
		}
		return theme.ParseStyle(color)
	}
	var steps []painter
	for _, set := range colors {
		named := false
		for _, candidate := range set.Labels {
			named = named || candidate == label
		}
		if !named {
			continue
		}
		if style, ok := resolve(set.Color, set.Env); ok {
			steps = append(steps, painter{style: style})
		}
		for _, rule := range set.Rules {
			if style, ok := resolve(rule.Color, rule.Env); ok && rule.Pattern != nil {
				steps = append(steps, painter{pattern: rule.Pattern, style: style})
			}
		}
	}
	return steps
}

// paint applies the steps to the text of a cell: a step for the whole cell
// paints it anew, without the colors of the steps before, and a step with
// a pattern paints what it matches, keeping the colors around the match.
func paint(text string, steps []painter) string {
	for _, step := range steps {
		text = step.style.PaintMatches(text, step.pattern)
	}
	return text
}
