package output

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mittwald/cli-kit/theme"

	"github.com/mittwald/api-kit/definition"
)

// styleNames are the text attributes a color specification can name.
var styleNames = map[string]theme.Style{
	"bold": "1", "b": "1",
	"fuzzy":  "2",
	"italic": "3", "i": "3",
	"underline": "4", "u": "4",
	"blink":   "5",
	"reverse": "7", "invert": "7",
	"strikethrough": "9",
}

// colorNames are the named colors, as their foreground SGR parameter.
var colorNames = map[string]int{
	"black": 30, "red": 31, "green": 32, "yellow": 33, "blue": 34,
	"magenta": 35, "purple": 35, "cyan": 36, "white": 37, "gray": 37, "grey": 37,
	"darkgray": 90, "darkgrey": 90, "lightred": 91, "lightgreen": 92, "lime": 92,
	"lightyellow": 93, "lightblue": 94, "lightmagenta": 95, "lightcyan": 96, "lightwhite": 97,
}

var (
	paletteIndex = regexp.MustCompile(`^\d{1,3}$`)
	hexColor     = regexp.MustCompile(`^(?:#|0x)?([0-9a-f]{3}|[0-9a-f]{6})$`)
)

// ParseStyle reads a color specification: parts separated by colons, each
// a color or a text attribute, as in "245", "160:bold" or
// "fg=white:bg=#aa0000:underline".
//
// A color is a name (red, lightblue, gray, ...), a number from 0 to 255 of
// the 256-color palette, or a hex color of three or six digits with an
// optional "#" or "0x". It colors the text; prefixed with "bg=" it colors
// the background. An attribute is one of bold, fuzzy, italic, underline,
// blink, reverse and strikethrough. Upper and lower case are the same.
//
// Parts that are neither are skipped. The result is false when nothing is
// left, so a mistyped specification paints nothing.
func ParseStyle(spec string) (theme.Style, bool) {
	var parts []theme.Style
	for token := range strings.SplitSeq(spec, ":") {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		if style, ok := styleNames[token]; ok {
			parts = append(parts, style)
			continue
		}
		background := false
		if key, value, found := strings.Cut(token, "="); found {
			switch strings.TrimSpace(key) {
			case "fg", "foreground":
			case "bg", "background":
				background = true
			default:
				continue
			}
			token = strings.TrimSpace(value)
		}
		if style, ok := colorStyle(token, background); ok {
			parts = append(parts, style)
		}
	}
	style := theme.Join(parts...)
	return style, style != ""
}

// colorStyle returns the style for one color.
func colorStyle(value string, background bool) (theme.Style, bool) {
	if code, ok := colorNames[value]; ok {
		if background {
			code += 10
		}
		return theme.Style(strconv.Itoa(code)), true
	}
	if paletteIndex.MatchString(value) {
		// A number past 255 is no palette index; it may still be a hex color.
		if index, err := strconv.ParseUint(value, 10, 8); err == nil {
			if background {
				return theme.Bg256(uint8(index)), true
			}
			return theme.Fg256(uint8(index)), true
		}
	}
	if match := hexColor.FindStringSubmatch(value); match != nil {
		digits := match[1]
		if len(digits) == 3 {
			digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
		}
		var channels [3]uint8
		for i := range channels {
			channel, err := strconv.ParseUint(digits[2*i:2*i+2], 16, 8)
			if err != nil {
				return "", false
			}
			channels[i] = uint8(channel)
		}
		if background {
			return theme.BgRGB(channels[0], channels[1], channels[2]), true
		}
		return theme.RGB(channels[0], channels[1], channels[2]), true
	}
	return "", false
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
// color specification as [ParseStyle] reads it. An empty prefix or a nil
// getenv gives the defaults.
func NewPalette(getenv func(string) string, prefix string) Palette {
	spec := func(suffix, def string) theme.Style {
		if getenv != nil && prefix != "" {
			if style, ok := ParseStyle(getenv(prefix + suffix)); ok {
				return style
			}
		}
		style, _ := ParseStyle(def)
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
			if style, ok := ParseStyle(getenv(env)); ok {
				return style, true
			}
		}
		return ParseStyle(color)
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

// sgr matches a sequence that sets or resets text attributes.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// paint applies the steps to the text of a cell.
func paint(text string, steps []painter) string {
	if text == "" {
		return text
	}
	for _, step := range steps {
		if step.pattern == nil {
			text = step.style.Paint(sgr.ReplaceAllString(text, ""))
		} else {
			text = paintMatches(text, step)
		}
	}
	return text
}

// paintMatches paints what the pattern matches in text that may be painted
// already. After each match the attributes that were active before it are
// switched on again, so the rest of the cell keeps its color.
func paintMatches(text string, step painter) string {
	var out strings.Builder
	var active []string
	segment := func(plain string) {
		if plain == "" {
			return
		}
		resume, reset := "", ""
		if len(active) > 0 {
			resume, reset = "\x1b["+strings.Join(active, ";")+"m", theme.Reset
		}
		out.WriteString(step.pattern.ReplaceAllStringFunc(plain, func(match string) string {
			if match == "" {
				return match
			}
			return reset + step.style.Paint(match) + resume
		}))
	}
	last := 0
	for _, position := range sgr.FindAllStringIndex(text, -1) {
		segment(text[last:position[0]])
		sequence := text[position[0]:position[1]]
		out.WriteString(sequence)
		active = activeAfter(active, sequence)
		last = position[1]
	}
	segment(text[last:])
	return out.String()
}

// activeAfter returns the attributes that are on after an SGR sequence,
// given those that were on before it. A sequence without parameters, or
// with a 0 among them, switches everything before it off.
func activeAfter(active []string, sequence string) []string {
	parameters := sequence[2 : len(sequence)-1]
	if parameters == "" {
		return nil
	}
	codes := strings.Split(parameters, ";")
	for i, code := range slices.Backward(codes) {
		if code == "0" {
			return slices.Clone(codes[i+1:])
		}
	}
	return slices.Concat(active, codes)
}
