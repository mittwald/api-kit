package expr

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	apikit "github.com/mittwald/api-kit"
)

// Op is a comparison operator of a condition.
type Op string

// The operators of a condition.
const (
	// OpEqual holds when the value equals the expected one.
	OpEqual Op = "="
	// OpNotEqual holds when the value differs from the expected one.
	OpNotEqual Op = "!="
	// OpLess holds when the value comes before the expected one.
	OpLess Op = "<"
	// OpLessEqual holds when the value does not come after the expected one.
	OpLessEqual Op = "<="
	// OpGreater holds when the value comes after the expected one.
	OpGreater Op = ">"
	// OpGreaterEqual holds when the value does not come before the expected
	// one.
	OpGreaterEqual Op = ">="
)

// A Condition is a test of one value: a path to it, an operator and the
// value to compare with, written one after the other as in enabled=false,
// status!=closed or createdAt>=2026-01-31.
type Condition struct {
	// Path is the left-hand side as written, without the white space around
	// it. It is not checked here; the caller parses it with [ParsePath].
	Path string
	// Op is the operator.
	Op Op
	// Value is the right-hand side: everything after the operator, white
	// space included.
	Value string
	// Source is the condition as written.
	Source string
}

// conditionPattern splits a condition at its first operator outside the
// brackets of a path filter. Of two operators that start at the same place
// the longer one wins.
var conditionPattern = regexp.MustCompile(
	`^(?P<path>(?:[^=!<>\[\]]|\[[^\]]*\])+?)(?P<operator>!=|>=|<=|=|>|<)(?P<value>.*)$`,
)

// ParseCondition parses s as a [Condition]. The path ends at the first
// operator that is not between the brackets of a filter, so
// items[sku=leash].quantity>=2 tests the quantity. The value is the rest of
// s and may be empty. It returns an error when s has no operator, nothing
// but white space before the operator, a ! that is not part of !=, a
// bracket without its counterpart before the operator, or a line break in
// the value.
func ParseCondition(s string) (Condition, error) {
	match := conditionPattern.FindStringSubmatch(s)
	if match == nil {
		return Condition{}, conditionError(s)
	}
	path := strings.TrimSpace(match[conditionPattern.SubexpIndex("path")])
	if path == "" {
		return Condition{}, conditionError(s)
	}
	return Condition{
		Path:   path,
		Op:     Op(match[conditionPattern.SubexpIndex("operator")]),
		Value:  match[conditionPattern.SubexpIndex("value")],
		Source: s,
	}, nil
}

func conditionError(s string) error {
	return fmt.Errorf(
		"%q is not a condition: expected a path, one of the operators = != < <= > >= and a value, "+
			"like enabled=false, status!=closed or createdAt>=2026-01-31", s)
}

// String returns the condition as written.
func (c Condition) String() string { return c.Source }

// Holds reports whether actual, a value of the types [apikit.Decode]
// produces, compares to expected as op says. The value is compared as the
// text [apikit.Text] shows for it: nothing for null, true or false for a
// boolean.
//
// [OpEqual] holds when the text equals expected. When the two differ and
// neither is empty, it also holds
//
//   - for a JSON number, when expected is a number of the same value: 2
//     equals 2.0;
//   - for any other value, when both are times that denote the same
//     instant: 2026-03-09T15:00:00Z equals 2026-03-09T16:00:00+01:00.
//
// A string is not compared as a number here, so the string 01234 does not
// equal 1234. [OpNotEqual] holds when [OpEqual] does not.
//
// The other operators never hold for a value whose text is empty. They
// compare as numbers when both sides are numbers, else as times when both
// sides are times, else as strings, byte by byte. Here a string that is
// written as a number is one: the string 10 comes after 9.
//
// A number is written in decimal digits with an optional sign, fraction and
// exponent: 7, -1.5, +.5, 2e3. Numbers are compared by value, with far more
// precision than a float64 holds.
//
// A time is a date, YYYY-MM-DD, which alone is midnight UTC. One separator
// character, usually T or a space, and a time of day may follow: HH, HH:MM
// or HH:MM:SS, or HHMM or HHMMSS without the colons. The seconds may have a
// fraction after a point or a comma, of which nine digits count. The time
// of day may end with a zone: Z, or an offset from UTC written as +HH,
// +HHMM or +HH:MM, with - for a zone behind UTC. Without a zone the time is
// UTC. A text with a field that is out of range, a thirteenth month or an
// hour 24, is not a time.
//
// An operator that is none of the six never holds.
func Holds(actual any, op Op, expected string) bool {
	text := apikit.Text(actual)
	switch op {
	case OpEqual:
		return equal(actual, text, expected)
	case OpNotEqual:
		return !equal(actual, text, expected)
	}
	// Nothing is neither before nor after a value.
	if text == "" {
		return false
	}
	order := compare(text, expected)
	switch op {
	case OpLess:
		return order < 0
	case OpLessEqual:
		return order <= 0
	case OpGreater:
		return order > 0
	case OpGreaterEqual:
		return order >= 0
	}
	return false
}

// equal reports whether actual, shown as text, equals expected.
func equal(actual any, text, expected string) bool {
	if text == expected {
		return true
	}
	if text == "" || expected == "" {
		return false
	}
	if _, isNumber := actual.(json.Number); isNumber {
		left, leftOK := parseNumber(text)
		right, rightOK := parseNumber(expected)
		return leftOK && rightOK && left.Cmp(right) == 0
	}
	left, leftOK := parseTime(text)
	right, rightOK := parseTime(expected)
	return leftOK && rightOK && left.Equal(right)
}

// compare returns -1, 0 or 1 for left before, equal to or after right: as
// numbers, else as times, else as strings.
func compare(left, right string) int {
	if a, ok := parseNumber(left); ok {
		if b, ok := parseNumber(right); ok {
			return a.Cmp(b)
		}
	}
	if a, ok := parseTime(left); ok {
		if b, ok := parseTime(right); ok {
			return a.Compare(b)
		}
	}
	return strings.Compare(left, right)
}

// numberPattern is what a condition takes for a number.
var numberPattern = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// numberPrecision is the size of the mantissa numbers are compared with, in
// bits. It holds every integer of up to 154 digits exactly.
const numberPrecision = 512

func parseNumber(s string) (*big.Float, bool) {
	if !numberPattern.MatchString(s) {
		return nil, false
	}
	// The pattern leaves one thing for ParseFloat to refuse: an exponent
	// that does not fit into 64 bits.
	number, _, err := big.ParseFloat(s, 10, numberPrecision, big.ToNearestEven)
	return number, err == nil
}

// parseTime reads a date, or a date and a time of day, as [Holds] describes
// them.
func parseTime(s string) (time.Time, bool) {
	scan := timeScanner{rest: s}
	year := scan.digits(4)
	scan.expect('-')
	month := scan.digits(2)
	scan.expect('-')
	day := scan.digits(2)
	if scan.failed {
		return time.Time{}, false
	}

	var hour, minute, second, nanosecond, offset int
	if scan.rest != "" {
		// One separator character, whichever it is.
		_, size := utf8.DecodeRuneInString(scan.rest)
		scan.rest = scan.rest[size:]

		hour = scan.digits(2)
		// The minutes and the seconds are both written with a colon or
		// both without.
		colons := scan.skip(':')
		if colons || scan.digitNext() {
			minute = scan.digits(2)
			if (colons && scan.skip(':')) || (!colons && scan.digitNext()) {
				second = scan.digits(2)
				if scan.skip('.') || scan.skip(',') {
					nanosecond = scan.fraction()
				}
			}
		}
		offset = scan.zone()
		if scan.failed || scan.rest != "" {
			return time.Time{}, false
		}
	}
	if hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	// Date moves a day or a month that does not exist into the next month
	// or year, and one that is zero into the one before.
	date := time.Date(year, time.Month(month), day, hour, minute, second, nanosecond, time.UTC)
	if date.Year() != year || date.Month() != time.Month(month) || date.Day() != day {
		return time.Time{}, false
	}
	return date.Add(-time.Duration(offset) * time.Second), true
}

// A timeScanner reads a time from left to right. A read that does not find
// what it needs sets failed, which stays set.
type timeScanner struct {
	rest   string
	failed bool
}

// digitNext reports whether a digit comes next.
func (s *timeScanner) digitNext() bool {
	return s.rest != "" && s.rest[0] >= '0' && s.rest[0] <= '9'
}

// digits reads exactly n digits and returns their value.
func (s *timeScanner) digits(n int) int {
	value := 0
	for range n {
		if !s.digitNext() {
			s.failed = true
			return 0
		}
		value = value*10 + int(s.rest[0]-'0')
		s.rest = s.rest[1:]
	}
	return value
}

// skip reads c when it comes next and reports whether it did.
func (s *timeScanner) skip(c byte) bool {
	if s.rest == "" || s.rest[0] != c {
		return false
	}
	s.rest = s.rest[1:]
	return true
}

// expect reads c, which has to come next.
func (s *timeScanner) expect(c byte) {
	if !s.skip(c) {
		s.failed = true
	}
}

// fraction reads the digits of a fraction of a second, at least one, and
// returns the first nine of them as nanoseconds.
func (s *timeScanner) fraction() int {
	if !s.digitNext() {
		s.failed = true
		return 0
	}
	nanoseconds := 0
	for unit := 100_000_000; s.digitNext(); unit /= 10 {
		nanoseconds += int(s.rest[0]-'0') * unit
		s.rest = s.rest[1:]
	}
	return nanoseconds
}

// zone reads a zone when one comes next and returns its offset from UTC in
// seconds, 0 without a zone.
func (s *timeScanner) zone() int {
	sign := 1
	switch {
	case s.skip('Z'):
		return 0
	case s.skip('+'):
	case s.skip('-'):
		sign = -1
	default:
		return 0
	}
	hours := s.digits(2)
	minutes := 0
	if s.skip(':') || s.digitNext() {
		minutes = s.digits(2)
	}
	if hours > 23 || minutes > 59 {
		s.failed = true
	}
	return sign * (hours*3600 + minutes*60)
}
