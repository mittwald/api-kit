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

// ParseTime reads a value given for a parameter of type time, relative to
// now: a preset, a time in one of the parameter's layouts, or one that
// [ParseTime] reads, in the time zone of now. A date written without a time
// of day stands for the parameter's DayTime on that day, or now when the
// day is today.
func (p *Parameter) ParseTime(text string, now time.Time) (time.Time, error) {
	text = strings.TrimSpace(text)
	for _, preset := range p.Presets {
		if preset.Key == text {
			text = preset.Value
			break
		}
	}
	for _, layout := range p.Layouts {
		if parsed, err := time.ParseInLocation(layout, text, now.Location()); err == nil {
			// A layout may carry a zone of its own; the time is one of the
			// parameter's zone all the same.
			parsed = parsed.In(now.Location())
			if !hasClock(layout) {
				return p.onDay(parsed, now), nil
			}
			return parsed, nil
		}
	}
	parsed, err := ParseTime(text, now)
	if err != nil {
		return parsed, err
	}
	if p.DayTime != "" && dayOnly(text, now) {
		return p.onDay(parsed, now), nil
	}
	return parsed, nil
}

// dayOnly reports whether text names a day without a time of day: an ISO
// 8601 date, today, tomorrow, or a number of days or weeks from now.
func dayOnly(text string, now time.Time) bool {
	if _, err := time.ParseInLocation(time.DateOnly, text, now.Location()); err == nil {
		return true
	}
	switch text {
	case "today", "tomorrow":
		return true
	}
	match := relativeTime.FindStringSubmatch(text)
	return match != nil && (match[2] == "d" || match[2] == "w")
}

// hasClock reports whether a layout writes a time of day: a time with a
// clock reads differently in it than midnight of the same day.
func hasClock(layout string) bool {
	day := time.Date(2001, time.February, 3, 0, 0, 0, 0, time.UTC)
	return day.Format(layout) != day.Add(13*time.Hour+14*time.Minute+15*time.Second).Format(layout)
}

// onDay returns the time a date without a time of day stands for: the
// DayTime of the parameter on that day, or now when the day is today.
func (p *Parameter) onDay(day, now time.Time) time.Time {
	match := clockTime.FindStringSubmatch(p.DayTime)
	if match == nil {
		return day
	}
	if day.Year() == now.Year() && day.YearDay() == now.YearDay() {
		return now
	}
	hour, _ := strconv.Atoi(match[1])
	minute, _ := strconv.Atoi(match[2])
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, day.Location())
}
