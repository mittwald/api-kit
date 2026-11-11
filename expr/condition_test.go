package expr_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mittwald/api-kit/expr"
)

func TestParseCondition(t *testing.T) {
	tests := []struct {
		condition string
		path      string
		op        expr.Op
		value     string
	}{
		{"enabled=false", "enabled", expr.OpEqual, "false"},
		{"status!=closed", "status", expr.OpNotEqual, "closed"},
		{"quantity<10", "quantity", expr.OpLess, "10"},
		{"quantity<=10", "quantity", expr.OpLessEqual, "10"},
		{"quantity>10", "quantity", expr.OpGreater, "10"},
		{"createdAt>=2026-01-31", "createdAt", expr.OpGreaterEqual, "2026-01-31"},
		{"customer.address.city=Springfield", "customer.address.city", expr.OpEqual, "Springfield"},

		// The value may be empty, and is taken as it is.
		{"note=", "note", expr.OpEqual, ""},
		{"note!=", "note", expr.OpNotEqual, ""},
		{" status = open ", "status", expr.OpEqual, " open "},
		{"name=Ada Lovelace", "name", expr.OpEqual, "Ada Lovelace"},
		{"first name=Ada", "first name", expr.OpEqual, "Ada"},

		// The first operator ends the path, the longer one where two fit.
		{"a=b=c", "a", expr.OpEqual, "b=c"},
		{"a==b", "a", expr.OpEqual, "=b"},
		{"a=>b", "a", expr.OpEqual, ">b"},
		{"a<>b", "a", expr.OpLess, ">b"},
		{"a>=<b", "a", expr.OpGreaterEqual, "<b"},
		{"a!==b", "a", expr.OpNotEqual, "=b"},
		{"a=b!c", "a", expr.OpEqual, "b!c"},
		{"a=[b]", "a", expr.OpEqual, "[b]"},

		// What stands between the brackets of a filter is part of the path.
		{"items[sku=leash].quantity>=2", "items[sku=leash].quantity", expr.OpGreaterEqual, "2"},
		{"items[a>b].c<3", "items[a>b].c", expr.OpLess, "3"},
		{"items[sku!=x]=y", "items[sku!=x]", expr.OpEqual, "y"},
	}
	for _, test := range tests {
		condition, err := expr.ParseCondition(test.condition)
		if err != nil {
			t.Errorf("ParseCondition(%q): %v", test.condition, err)
			continue
		}
		want := expr.Condition{Path: test.path, Op: test.op, Value: test.value, Source: test.condition}
		if condition != want {
			t.Errorf("ParseCondition(%q) = %+v, want %+v", test.condition, condition, want)
		}
		if condition.String() != test.condition {
			t.Errorf("ParseCondition(%q).String() = %q", test.condition, condition.String())
		}
	}
}

func TestParseConditionErrors(t *testing.T) {
	invalid := []string{
		"",
		"status",
		"=open",
		"   =open",
		"!=",
		"a!b",
		"a!b=c",
		"a~b",
		"items[sku=leash",
		"items[sku=leash].quantity",
		"items]=x",
		"[=x",
		"note=two\nlines",
		"note=line\n",
	}
	for _, text := range invalid {
		condition, err := expr.ParseCondition(text)
		if err == nil {
			t.Errorf("ParseCondition(%q) = %+v, want an error", text, condition)
			continue
		}
		// The message names what was given and shows what is expected.
		for _, part := range []string{strconv.Quote(text), "enabled=false", "status!=closed", "createdAt>=2026-01-31"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("ParseCondition(%q): %v, want a message with %s", text, err, part)
			}
		}
	}
}

func TestHolds(t *testing.T) {
	number := func(text string) json.Number { return json.Number(text) }
	tests := []struct {
		actual   any
		op       expr.Op
		expected string
		want     bool
	}{
		// Values read as text: nothing for null, true or false.
		{false, "=", "false", true},
		{true, "=", "true", true},
		{true, "!=", "false", true},
		{nil, "=", "", true},
		{nil, "!=", "", false},
		{"", "=", "", true},
		{nil, "!=", "open", true},
		{"open", "=", "", false},
		{"open", "!=", "", true},
		{"archived", "=", "archived", true},
		{"archived", "!=", "archived", false},
		{"archived", "=", "Archived", false},
		{decode(t, `["red", 2]`), "=", `["red", 2]`, true},
		{decode(t, `{"name": "Ada"}`), "=", `{"name": "Ada"}`, true},
		{decode(t, `[]`), "!=", "", true},

		// Numbers.
		{number("3"), ">", "2", true},
		{number("10"), "<", "9", false},
		{number("10"), ">", "9", true},
		{number("2"), "=", "2.0", true},
		{number("2"), "!=", "2.0", false},
		{number("2"), "<=", "2.0", true},
		{number("2"), ">=", "2.0", true},
		{number("2"), "<", "2.0", false},
		{number("2"), ">", "2.0", false},
		{number("1e3"), "=", "1000", true},
		{number("0.5"), "=", ".5", true},
		{number("5"), "=", "+5.", true},
		{number("-0"), "=", "0", true},
		{number("-1.5"), "<", "-.5", true},
		{number("2"), "=", "two", false},
		{number("2"), "!=", "two", true},
		// Beyond the precision of a float64.
		{number("12345678901234567890"), "=", "12345678901234567891", false},
		{number("12345678901234567890"), "<", "12345678901234567891", true},
		{number("0.1"), "<", "0.10000000000000000001", true},
		// A number in a string is ordered as a number.
		{"10", ">", "9", true},
		{"10", "<", "9", false},
		// But it equals only the same text.
		{"01234", "=", "1234", false},
		{"1.10", "=", "1.1", false},
		{"1.10", "!=", "1.1", true},
		{"1.10", "<=", "1.1", true},

		// What is not written as a number is text: no words, no digit
		// separators, no white space around it, no other bases.
		{number("10"), "=", "1_0", false},
		{number("10"), "=", " 10", false},
		{number("10"), "=", "10 ", false},
		{number("16"), "=", "0x10", false},
		{number("1"), "=", "1e", false},
		{number("1"), "=", "1.0.0", false},
		{"2", ">", "1_0", true},
		{"9", ">", " 10", true},
		{"1", "<", "nan", true},
		{"nan", "=", "nan", true},
		{"inf", ">", "9e999", true},

		// Times.
		{"2026-03-14T15:08:08.000Z", ">=", "2026-03-09", true},
		{"2026-03-09T10:00:00Z", ">", "2026-03-09", true},
		{"2026-03-08T23:59:59+00:00", "<", "2026-03-09", true},
		{"2026-03-09", "<", "2026-03-09T00:00:01Z", true},
		{"2026-03-09T00:00:00Z", "<=", "2026-03-09", true},
		{"2026-03-09T00:00:00Z", ">=", "2026-03-09", true},
		{"2026-03-09T00:00:00Z", "<", "2026-03-09", false},
		// The same instant, written in another zone.
		{"2026-03-14T15:00:00Z", "=", "2026-03-14T16:00:00+01:00", true},
		{"2026-03-14T15:00:00Z", "!=", "2026-03-14T16:00:00+01:00", false},
		{"2026-03-14T00:00:00.000Z", "=", "2026-03-14", true},
		{"2026-03-14T00:00:00.001Z", "=", "2026-03-14", false},
		// As text the left side would come first.
		{"2026-03-14T23:00:00-02:00", ">", "2026-03-15", true},
		{"", "=", "2026-03-14", false},
		{nil, "<", "2026-03-09", false},
		// A number and a time are compared as text.
		{"2026-03-14", ">", "2025", true},
		{number("2026"), "<", "2026-03-14", true},
		// So are two dates of which one does not exist.
		{"2026-02-30", "<", "2026-03-01", true},

		// Text, in the order of its bytes.
		{"b", ">", "a", true},
		{"a", "<", "b", true},
		{"a", "<=", "a", true},
		{"a", ">=", "a", true},
		{"a", "<", "a", false},
		{"apple", "<", "apples", true},
		{"Zebra", "<", "apple", true},
		{"é", ">", "z", true},
		{"open", ">", "", true},
		{true, ">", "false", true},

		// Nothing is neither before nor after a value.
		{nil, "<", "a", false},
		{nil, "<=", "", false},
		{"", ">=", "", false},
		{"", ">", "a", false},

		// Only the six operators hold.
		{"a", "==", "a", false},
		{"a", "", "a", false},
		{"a", "~", "b", false},
		{nil, "=<", "", false},
	}
	for _, test := range tests {
		if got := expr.Holds(test.actual, test.op, test.expected); got != test.want {
			t.Errorf("Holds(%#v, %q, %q) = %v, want %v", test.actual, test.op, test.expected, got, test.want)
		}
	}
}

// Each form on the left is a time that equals the one on the right, which
// is written differently.
func TestHoldsTimeForms(t *testing.T) {
	tests := []struct{ form, same string }{
		// A date alone is midnight UTC.
		{"2026-03-14", "2026-03-14T00:00:00Z"},
		{"2024-02-29", "2024-02-29T00:00:00Z"},
		{"0001-01-01", "0001-01-01T00:00:00Z"},
		{"9999-12-31", "9999-12-31T00:00:00Z"},

		// Any one character separates the date from the time of day.
		{"2026-03-14 10:30:45", "2026-03-14T10:30:45Z"},
		{"2026-03-14t10:30:45", "2026-03-14T10:30:45Z"},
		{"2026-03-14_10:30:45", "2026-03-14T10:30:45Z"},
		{"2026-03-14 10:30:45", "2026-03-14T10:30:45Z"},

		// The time of day, with and without colons.
		{"2026-03-14T10", "2026-03-14T10:00:00Z"},
		{"2026-03-14T10:30", "2026-03-14T10:30:00Z"},
		{"2026-03-14T10:30:45", "2026-03-14T10:30:45Z"},
		{"2026-03-14T1030", "2026-03-14T10:30:00Z"},
		{"2026-03-14T103045", "2026-03-14T10:30:45Z"},
		{"2026-03-14T00:00:00", "2026-03-14"},
		{"2026-03-14T23:59:59", "2026-03-14T235959Z"},

		// A fraction of a second, of which nine digits count.
		{"2026-03-14T10:30:45.5", "2026-03-14T10:30:45.500Z"},
		{"2026-03-14T10:30:45,5", "2026-03-14T10:30:45.500Z"},
		{"2026-03-14T103045.25", "2026-03-14T10:30:45.250000Z"},
		{"2026-03-14T10:30:45.0", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45.1234567891", "2026-03-14T10:30:45.123456789Z"},

		// Zones.
		{"2026-03-14T10:30:45Z", "2026-03-14T10:30:45"},
		{"2026-03-14T10:30:45+00:00", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45-00:00", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45+01", "2026-03-14T09:30:45Z"},
		{"2026-03-14T10:30:45+0130", "2026-03-14T09:00:45Z"},
		{"2026-03-14T10:30:45+01:30", "2026-03-14T09:00:45Z"},
		{"2026-03-14T10:30:45-05", "2026-03-14T15:30:45Z"},
		{"2026-03-14T10:30:45-0530", "2026-03-14T16:00:45Z"},
		{"2026-03-14T10:30:45-05:30", "2026-03-14T16:00:45Z"},
		{"2026-03-14T10:30:45.5+01:00", "2026-03-14T09:30:45.5Z"},
		{"2026-03-14T10+02", "2026-03-14T08:00:00Z"},
		{"2026-03-14T10:30-00:30", "2026-03-14T11:00:00Z"},
		{"2026-03-14T1030-0030", "2026-03-14T11:00:00Z"},
		{"2026-03-14T00:30:00+01:00", "2026-03-13T23:30:00Z"},
		{"2026-03-14T23:59:59+23:59", "2026-03-14T00:00:59Z"},
		{"2026-12-31T23:30:00-01:00", "2027-01-01T00:30:00Z"},
	}
	for _, test := range tests {
		if test.form == test.same {
			t.Errorf("%q is compared with itself", test.form)
		}
		if !expr.Holds(test.form, expr.OpEqual, test.same) || !expr.Holds(test.same, expr.OpEqual, test.form) {
			t.Errorf("%q and %q are not the same instant", test.form, test.same)
		}
		if expr.Holds(test.form, expr.OpLess, test.same) || expr.Holds(test.form, expr.OpGreater, test.same) {
			t.Errorf("%q is before or after %q", test.form, test.same)
		}
	}
}

// Each text on the left is not a time; the time on the right is what a
// more lenient reader could take it for.
func TestHoldsRejectsWhatIsNoTime(t *testing.T) {
	tests := []struct{ text, lenient string }{
		// Fields out of range.
		{"2026-13-01", "2027-01-01"},
		{"2026-00-10", "2025-12-10"},
		{"2026-03-00", "2026-02-28"},
		{"2026-03-32", "2026-04-01"},
		{"2026-04-31", "2026-05-01"},
		{"2026-02-29", "2026-03-01"},
		{"2026-03-14T24:00:00", "2026-03-15"},
		{"2026-03-14T24", "2026-03-15"},
		{"2026-03-14T23:60:00", "2026-03-15"},
		{"2026-03-14T23:59:60", "2026-03-15"},
		{"2026-03-14T10:30:45+24:00", "2026-03-13T10:30:45Z"},
		{"2026-03-14T10:30:45+01:60", "2026-03-14T08:30:45Z"},

		// Dates in another form.
		{"2026-3-14", "2026-03-14"},
		{"20260314", "2026-03-14"},
		{"2026-03", "2026-03-01"},
		{"2026/03/14", "2026-03-14"},
		{"03/14/2026", "2026-03-14"},
		{" 2026-03-14", "2026-03-14"},

		// A separator needs a time of day after it.
		{"2026-03-14T", "2026-03-14"},
		{"2026-03-14 ", "2026-03-14"},
		{"2026-03-14Z", "2026-03-14"},
		{"2026-03-14T+01:00", "2026-03-13T23:00:00Z"},
		{"2026-03-14TZ", "2026-03-14"},

		// Fields of the wrong length, colons in some places only.
		{"2026-03-14T1", "2026-03-14T01:00:00Z"},
		{"2026-03-14T1:30", "2026-03-14T01:30:00Z"},
		{"2026-03-14T10:3", "2026-03-14T10:03:00Z"},
		{"2026-03-14T10:30:4", "2026-03-14T10:30:04Z"},
		{"2026-03-14T10:", "2026-03-14T10:00:00Z"},
		{"2026-03-14T10:30:", "2026-03-14T10:30:00Z"},
		{"2026-03-14T10:3045", "2026-03-14T10:30:45Z"},
		{"2026-03-14T1030:45", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10304", "2026-03-14T10:30:04Z"},

		// A fraction belongs to the seconds and has digits.
		{"2026-03-14T10.5", "2026-03-14T10:30:00Z"},
		{"2026-03-14T10:30.5", "2026-03-14T10:30:30Z"},
		{"2026-03-14T10:30:45.", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45.Z", "2026-03-14T10:30:45Z"},

		// Zones in another form, and text after the time.
		{"2026-03-14T10:30:45z", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45 Z", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45UTC", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45+1", "2026-03-14T09:30:45Z"},
		{"2026-03-14T10:30:45+013", "2026-03-14T09:27:45Z"},
		{"2026-03-14T10:30:45+01:3", "2026-03-14T09:27:45Z"},
		{"2026-03-14T10:30:45+01:", "2026-03-14T09:30:45Z"},
		{"2026-03-14T10:30:45+", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45Z+01:00", "2026-03-14T09:30:45Z"},
		{"2026-03-14T10:30:45Zulu", "2026-03-14T10:30:45Z"},
		{"2026-03-14T10:30:45\n", "2026-03-14T10:30:45Z"},
	}
	for _, test := range tests {
		if expr.Holds(test.text, expr.OpEqual, test.lenient) || expr.Holds(test.lenient, expr.OpEqual, test.text) {
			t.Errorf("%q is taken for the time %s", test.text, test.lenient)
		}
	}
}
