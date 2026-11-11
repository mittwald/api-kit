package output

import (
	"regexp"
	"strconv"
	"time"
)

// timestamp matches what a table shows as an age: a date and a time of day
// with a zone, as APIs write a point in time.
var timestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})$`)

// timestampLayouts are the spellings of the zone that timestamp admits.
var timestampLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z0700"}

// parseTimestamp reads a point in time as an API writes it.
func parseTimestamp(text string) (time.Time, bool) {
	if !timestamp.MatchString(text) {
		return time.Time{}, false
	}
	for _, layout := range timestampLayouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// Age describes how long ago a point in time was, or how far ahead it is,
// seen from now: "now", "5 minutes ago", "a day ago", "1 year, 2 months
// ago", "3 hours from now". Months count as 30.5 days and years as 365
// days, so the wording is rough on purpose.
func Age(at, now time.Time) string {
	distance := now.Sub(at)
	template := " ago"
	if distance < 0 {
		distance, template = -distance, " from now"
	}
	span := span(distance)
	if span == "a moment" {
		return "now"
	}
	return span + template
}

// span words a length of time the way people say it.
func span(distance time.Duration) string {
	total := int64(distance / time.Second)
	days, seconds := total/86400, total%86400
	years, days := days/365, days%365
	months := int64(float64(days) / 30.5)

	count := func(n int64, one, many string) string {
		if n == 1 {
			return one
		}
		return strconv.FormatInt(n, 10) + " " + many
	}
	switch {
	case years == 0 && days < 1:
		switch {
		case seconds == 0:
			return "a moment"
		case seconds < 60:
			return count(seconds, "a second", "seconds")
		case seconds < 120:
			return "a minute"
		case seconds < 3600:
			return strconv.FormatInt(seconds/60, 10) + " minutes"
		case seconds < 7200:
			return "an hour"
		}
		return strconv.FormatInt(seconds/3600, 10) + " hours"
	case years == 0:
		if months == 0 {
			return count(days, "a day", "days")
		}
		return count(months, "a month", "months")
	case years == 1:
		switch {
		case months == 0 && days == 0:
			return "a year"
		case months == 0:
			return "1 year, " + count(days, "1 day", "days")
		}
		return "1 year, " + count(months, "1 month", "months")
	}
	return strconv.FormatInt(years, 10) + " years"
}
