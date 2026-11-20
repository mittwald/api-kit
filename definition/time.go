package definition

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// relativeTime is a time relative to now: a count and a unit.
var relativeTime = regexp.MustCompile(`^\+(\d+)(m|h|d|w)$`)

// clockTime is a time of day.
var clockTime = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// localLayouts are the ISO 8601 layouts without a time zone that a time may
// be written in. It is a time of the zone of now then.
var localLayouts = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseTime reads the value of a parameter of type time, relative to now
// and in its time zone:
//
//   - an RFC 3339 time, as in 2006-01-02T15:04:05+01:00;
//   - an ISO 8601 date, or date and time, without a time zone, as in
//     2006-01-02 or 2006-01-02T15:04, in the time zone of now;
//   - now, today or tomorrow, the latter two at midnight;
//   - a time from now, as in +30m, +2h, +1d or +1w: minutes, hours, days or
//     weeks, a day being one on the calendar;
//   - a time of day, as in 08:00, today or after one of the two above.
//
// A time of day after a day sets the clock on that day, as in "tomorrow
// 08:00" or "+1w 00:00".
func ParseTime(text string, now time.Time) (time.Time, error) {
	text = strings.TrimSpace(text)
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.In(now.Location()), nil
	}
	for _, layout := range localLayouts {
		if parsed, err := time.ParseInLocation(layout, text, now.Location()); err == nil {
			return parsed, nil
		}
	}

	fields := strings.Fields(text)
	var day time.Time
	switch {
	case len(fields) == 0 || len(fields) > 2:
		return time.Time{}, timeMistake(text)
	case len(fields) == 1 && clockTime.MatchString(fields[0]):
		// A time of day alone is one of today.
		day = midnight(now)
		fields = append([]string{"today"}, fields...)
	default:
		var ok bool
		if day, ok = relativeDay(fields[0], now); !ok {
			return time.Time{}, timeMistake(text)
		}
	}
	if len(fields) == 1 {
		return day, nil
	}
	match := clockTime.FindStringSubmatch(fields[1])
	if match == nil {
		return time.Time{}, timeMistake(text)
	}
	hour, _ := strconv.Atoi(match[1])
	minute, _ := strconv.Atoi(match[2])
	if hour > 23 || minute > 59 {
		return time.Time{}, fmt.Errorf("%q has no time of day %s", text, fields[1])
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, now.Location()), nil
}

// relativeDay reads the first word of a time that is relative to now.
func relativeDay(word string, now time.Time) (time.Time, bool) {
	switch word {
	case "now":
		return now, true
	case "today":
		return midnight(now), true
	case "tomorrow":
		return midnight(now).AddDate(0, 0, 1), true
	}
	match := relativeTime.FindStringSubmatch(word)
	if match == nil {
		return time.Time{}, false
	}
	count, err := strconv.Atoi(match[1])
	if err != nil {
		return time.Time{}, false
	}
	switch match[2] {
	case "m":
		return now.Add(time.Duration(count) * time.Minute), true
	case "h":
		return now.Add(time.Duration(count) * time.Hour), true
	case "d":
		return now.AddDate(0, 0, count), true
	default:
		return now.AddDate(0, 0, 7*count), true
	}
}

// midnight returns the start of the day of t, in its time zone.
func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// timeMistake returns the error for a text that is no time.
func timeMistake(text string) error {
	return fmt.Errorf("%q is no time: give an ISO 8601 time such as 2006-01-02T15:04, "+
		"or now, today, tomorrow or +N with m, h, d or w, optionally followed by a time of day such as 08:00", text)
}
