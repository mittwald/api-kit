package output

import (
	"slices"

	"github.com/mittwald/cli-kit/table"
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
type Palette = table.Palette

// NewPalette returns the default palette with the changes the environment
// asks for, as table.NewPalette of cli-kit reads them from the variables
// <prefix>_COLOR_TABLE_HEADER, <prefix>_COLOR_TABLE_COLUMN_1 to _6,
// <prefix>_TEXT_COLOR_DURATION and <prefix>_TEXT_DURATION_FRESH.
//
// Deprecated: Use table.NewPalette of cli-kit.
func NewPalette(getenv func(string) string, prefix string) Palette {
	return table.NewPalette(getenv, prefix)
}

// rules returns the rules that paint the cells of the column with that
// heading, in the order they apply: for every set of colors that names the
// column, its color for the whole cell, then its rules.
func rules(colors []definition.ColorSet, label string, getenv func(string) string) theme.Rules {
	resolve := func(color, env string) (theme.Style, bool) {
		if env != "" && getenv != nil {
			if style, ok := theme.ParseStyle(getenv(env)); ok {
				return style, true
			}
		}
		return theme.ParseStyle(color)
	}
	var steps theme.Rules
	for _, set := range colors {
		if !slices.Contains(set.Labels, label) {
			continue
		}
		if style, ok := resolve(set.Color, set.Env); ok {
			steps = append(steps, theme.Rule{Style: style})
		}
		for _, rule := range set.Rules {
			if style, ok := resolve(rule.Color, rule.Env); ok && rule.Pattern != nil {
				steps = append(steps, theme.Rule{Pattern: rule.Pattern, Style: style})
			}
		}
	}
	return steps
}
