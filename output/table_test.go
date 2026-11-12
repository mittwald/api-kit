package output_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mittwald/cli-kit/theme"

	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/output"
)

func TestParseStyle(t *testing.T) {
	tests := map[string]theme.Style{
		"245":                     "38;5;245",
		"160:bold":                "38;5;160;1",
		" Red : B ":               "31;1",
		"lightblue:underline":     "94;4",
		"fg=white:bg=red":         "37;41",
		"background=darkgray":     "100",
		"bg=17":                   "48;5;17",
		"#ff8000":                 "38;2;255;128;0",
		"0xF80":                   "38;2;255;136;0",
		"bg=#000000":              "48;2;0;0;0",
		"300":                     "38;2;51;0;0", // past the palette, so three hex digits
		"italic:blink:reverse":    "3;5;7",
		"i:u:invert:fuzzy":        "3;4;7;2",
		"strikethrough":           "9",
		"bold::unknown:shade=red": "1",
		"foreground=cyan":         "36",
	}
	for spec, want := range tests {
		got, ok := output.ParseStyle(spec)
		if !ok || got != want {
			t.Errorf("ParseStyle(%q) = %q, %v; want %q", spec, got, ok, want)
		}
	}
	for _, spec := range []string{"", ":", "nonsense", "shade=red", "fg=nonsense", "256x"} {
		if got, ok := output.ParseStyle(spec); ok || got != "" {
			t.Errorf("ParseStyle(%q) = %q, %v; want nothing", spec, got, ok)
		}
	}
}

func TestNewPalette(t *testing.T) {
	defaults := output.NewPalette(nil, "")
	if defaults.Header != "38;5;248;1" || defaults.Age != "38;5;245" || defaults.FreshAge != "38;5;40;1" || len(defaults.Columns) != 6 || defaults.Columns[0] != "38;5;38" {
		t.Errorf("default palette = %+v", defaults)
	}
	env := map[string]string{
		"EXAMPLE_COLOR_TABLE_HEADER":   "red:bold",
		"EXAMPLE_COLOR_TABLE_COLUMN_2": "blue",
		"EXAMPLE_COLOR_TABLE_COLUMN_3": "nonsense", // ignored
		"EXAMPLE_TEXT_COLOR_DURATION":  "gray",
		"EXAMPLE_TEXT_DURATION_FRESH":  "green",
	}
	changed := output.NewPalette(func(name string) string { return env[name] }, "EXAMPLE")
	if changed.Header != "31;1" || changed.Age != "37" || changed.FreshAge != "32" ||
		changed.Columns[0] != defaults.Columns[0] || changed.Columns[1] != "34" || changed.Columns[2] != defaults.Columns[2] {
		t.Errorf("palette from the environment = %+v", changed)
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{0, "now"},
		{500 * time.Millisecond, "now"},
		{time.Second, "a second ago"},
		{59 * time.Second, "59 seconds ago"},
		{time.Minute, "a minute ago"},
		{119 * time.Second, "a minute ago"},
		{2 * time.Minute, "2 minutes ago"},
		{59 * time.Minute, "59 minutes ago"},
		{time.Hour, "an hour ago"},
		{2*time.Hour - time.Second, "an hour ago"},
		{2 * time.Hour, "2 hours ago"},
		{23 * time.Hour, "23 hours ago"},
		{24 * time.Hour, "a day ago"},
		{30 * 24 * time.Hour, "30 days ago"},
		{31 * 24 * time.Hour, "a month ago"},
		{61 * 24 * time.Hour, "2 months ago"},
		{364 * 24 * time.Hour, "11 months ago"},
		{365 * 24 * time.Hour, "a year ago"},
		{366 * 24 * time.Hour, "1 year, 1 day ago"},
		{(365 + 20) * 24 * time.Hour, "1 year, 20 days ago"},
		{(365 + 31) * 24 * time.Hour, "1 year, 1 month ago"},
		{(365 + 100) * 24 * time.Hour, "1 year, 3 months ago"},
		{2 * 365 * 24 * time.Hour, "2 years ago"},
		{12 * 365 * 24 * time.Hour, "12 years ago"},
		{-time.Second, "a second from now"},
		{-3 * time.Hour, "3 hours from now"},
		{-48 * time.Hour, "2 days from now"},
	}
	for _, tt := range tests {
		if got := output.Age(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("Age(%v ago) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}

// shop is the table most tests write.
func shop(t *testing.T) output.Table {
	t.Helper()
	rows := items(t, `[
	  ["p-1", "Rex", "dog", 3, ["calm", "", "old"], "2026-03-14T11:30:00Z", {"name": "Ada"}, [1, [2]]],
	  ["p-2", "Tom", "cat", null, [], "2026-03-01T12:00:00+02:00", null, []]
	]`)
	table := output.Table{Headers: []string{"id", "name", "species", "age", "tags", "seen", "owner", "nested"}}
	for _, row := range rows {
		table.Rows = append(table.Rows, row.([]any))
	}
	return table
}

func now() time.Time { return time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC) }

func writeTable(t *testing.T, table output.Table, options output.TableOptions) string {
	t.Helper()
	if options.Now == nil {
		options.Now = now
	}
	var buf bytes.Buffer
	if err := output.WriteTable(&buf, table, options); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestWriteTableForPeople(t *testing.T) {
	got := writeTable(t, shop(t), output.TableOptions{Format: output.Text})
	// Objects and nested lists have no place in a table; a list of scalars
	// is joined, leaving out what is empty; a point in time is an age.
	want := "" +
		"ID   NAME  SPECIES  AGE  TAGS       SEEN\n" +
		"p-1  Rex   dog      3    calm, old  30 minutes ago\n" +
		"p-2  Tom   cat                      13 days ago\n"
	if got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

// A value is whatever the API returned. In a table for people its escape
// sequences and control characters do not reach the terminal, painted or
// not, and a link whose address holds one is left out.
func TestWriteTableShowsValuesPlain(t *testing.T) {
	table := output.Table{
		Headers: []string{"name\x1b[2J", "note"},
		Rows: [][]any{
			{"web\x1b]52;c;ZXZpbA==\x07\x1b[2J", "first\x1b[31m line\nsecond\u009b2J line"},
		},
		Links: []string{"https://example.com/\x1b\\\x1b]0;title\x07"},
	}
	for name, options := range map[string]output.TableOptions{
		"plain":   {Format: output.Text},
		"wide":    {Format: output.Wide},
		"painted": {Format: output.Text, Color: true, Links: true, Palette: output.NewPalette(nil, "")},
	} {
		var out bytes.Buffer
		if err := output.WriteTable(&out, table, options); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		shown := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(out.String(), "")
		if strings.ContainsAny(shown, "\x1b\x07\u009b") {
			t.Errorf("%s: a control character reached the output: %q", name, out.String())
		}
		for _, want := range []string{"NAME", "web", "first line", "second line"} {
			if !strings.Contains(shown, want) {
				t.Errorf("%s: output misses %q: %q", name, want, shown)
			}
		}
	}
}

func TestWriteTableForPrograms(t *testing.T) {
	// The values are written as they are: no ages, nothing shortened.
	options := output.TableOptions{Format: output.CSV, Color: true, Links: true, Ellipsis: true, MaxCharacters: 5}
	table := shop(t)
	table.Links = []string{"https://app.example.test/p-1", ""}
	if got, want := writeTable(t, table, options), ""+
		"id,name,species,age,tags,seen\n"+
		"p-1,Rex,dog,3,\"calm, old\",2026-03-14T11:30:00Z\n"+
		"p-2,Tom,cat,,,2026-03-01T12:00:00+02:00\n"; got != want {
		t.Errorf("CSV:\n%s\nwant:\n%s", got, want)
	}
	options.Format = output.TSV
	if got := writeTable(t, table, options); !strings.HasPrefix(got, "id\tname\tspecies\tage\ttags\tseen\np-1\tRex\tdog\t3\tcalm, old\t2026-03-14T11:30:00Z\n") {
		t.Errorf("TSV:\n%s", got)
	}
	options.Format = output.Markdown
	if got := writeTable(t, table, options); !strings.HasPrefix(got, "| ID  | NAME | SPECIES | AGE | TAGS      | SEEN") || strings.Contains(got, "\x1b") {
		t.Errorf("Markdown:\n%s", got)
	}
}

func TestWriteTableShortensLongValues(t *testing.T) {
	table := output.Table{Headers: []string{"id", "note"}, Rows: [][]any{
		{"0123456789abcdef", "a long note\nover two lines"},
		{"short", "brief"},
	}}
	options := output.TableOptions{Format: output.Text, Ellipsis: true, MaxCharacters: 10, KeepWhole: []*regexp.Regexp{regexp.MustCompile(`^[0-9a-f]{16}$`)}}
	if got, want := writeTable(t, table, options), ""+
		"ID                NOTE\n"+
		"0123456789abcdef  a long no…\n"+
		"short             brief\n"; got != want {
		t.Errorf("table:\n%q\nwant:\n%q", got, want)
	}

	options.Ellipsis = false
	if got := writeTable(t, table, options); !strings.Contains(got, "over two lines") {
		t.Errorf("without ellipsis the value was shortened:\n%s", got)
	}
}

func TestWriteTablePaintsAndLinks(t *testing.T) {
	table := output.Table{
		Headers: []string{"id", "status", "note", "seen"},
		Rows: [][]any{
			{"p-1", "open and urgent", "x", "2026-03-14T11:59:00Z"},
			{"", "closed", "", "2026-03-10T12:00:00Z"},
		},
		Links: []string{"https://app.example.test/p-1", "https://app.example.test/p-2"},
	}
	env := map[string]string{"EXAMPLE_COLOR_URGENT": "yellow"}
	options := output.TableOptions{
		Format: output.Text, Color: true, Links: true,
		Palette: output.Palette{Header: "1", Columns: []theme.Style{"34", "35"}, Age: "90", FreshAge: "32"},
		Colors: []definition.ColorSet{{
			Labels: []string{"status", "missing"},
			Color:  "fuzzy",
			Rules: []definition.ColorRule{
				{Pattern: regexp.MustCompile("open"), Color: "red"},
				{Pattern: regexp.MustCompile("urgent"), Color: "nonsense", Env: "EXAMPLE_COLOR_URGENT"},
				{Pattern: regexp.MustCompile("and"), Color: "nonsense"}, // paints nothing
			},
		}},
		Getenv: func(name string) string { return env[name] },
	}
	got := writeTable(t, table, options)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table has %d lines:\n%q", len(lines), got)
	}
	const reset = "\x1b[0m"
	for _, want := range []string{
		// Headings in the header style.
		"\x1b[1mID ", "\x1b[1mSTATUS",
		// The first cell is a link, painted in the first palette color.
		"\x1b]8;;https://app.example.test/p-1\x1b\\\x1b[34mp-1" + reset + "\x1b]8;;\x1b\\",
		// The status column has rules: the cell is faded, matches are
		// painted and the fade resumes after each.
		reset + "\x1b[31mopen" + reset + "\x1b[2m and " + reset + "\x1b[33murgent" + reset + "\x1b[2m" + reset,
		// The note column has no rules and takes the next palette color.
		"\x1b[35mx" + reset,
		// An age of less than an hour is fresh.
		"\x1b[32ma minute ago" + reset,
	} {
		if !strings.Contains(lines[0]+"\n"+lines[1], want) {
			t.Errorf("first row lacks %q:\n%q", want, lines[1])
		}
	}
	// An empty first cell is no link; an older age is not fresh.
	if strings.Contains(lines[2], "\x1b]8") {
		t.Errorf("a row without a first cell got a link: %q", lines[2])
	}
	if !strings.Contains(lines[2], "\x1b[2mclosed"+reset) || !strings.Contains(lines[2], "\x1b[90m4 days ago"+reset) {
		t.Errorf("second row: %q", lines[2])
	}

	options.Color, options.Links = false, false
	if plain := writeTable(t, table, options); strings.Contains(plain, "\x1b") {
		t.Errorf("a table without color and links has escape sequences:\n%q", plain)
	}
}

func TestWriteTableWithoutRowsOrColumns(t *testing.T) {
	if got := writeTable(t, output.Table{Headers: []string{"id", "name"}}, output.TableOptions{Format: output.Text}); got != "ID  NAME\n" {
		t.Errorf("a table without rows wrote %q", got)
	}
	if got := writeTable(t, output.Table{}, output.TableOptions{Format: output.Text}); got != "" {
		t.Errorf("a table without columns wrote %q", got)
	}
}
