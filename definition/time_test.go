package definition_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mittwald/api-kit/definition"
)

func TestParseTime(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	// A Saturday, the day before summer time ends.
	now := time.Date(2026, time.October, 24, 14, 30, 15, 0, berlin)
	tests := []struct {
		text, want string
	}{
		{"2026-11-21T08:00:00+01:00", "2026-11-21T08:00:00+01:00"},
		{"2026-11-21T07:00:00Z", "2026-11-21T08:00:00+01:00"},
		{"2026-11-21T08:00:00", "2026-11-21T08:00:00+01:00"},
		{"2026-11-21T08:00", "2026-11-21T08:00:00+01:00"},
		{"2026-11-21 08:00", "2026-11-21T08:00:00+01:00"},
		{"2026-11-21", "2026-11-21T00:00:00+01:00"},
		{"now", "2026-10-24T14:30:15+02:00"},
		{"today", "2026-10-24T00:00:00+02:00"},
		{"tomorrow", "2026-10-25T00:00:00+02:00"},
		{"tomorrow 08:00", "2026-10-25T08:00:00+01:00"},
		{" tomorrow  8:05 ", "2026-10-25T08:05:00+01:00"},
		{"09:15", "2026-10-24T09:15:00+02:00"},
		{"+30m", "2026-10-24T15:00:15+02:00"},
		{"+24h", "2026-10-25T13:30:15+01:00"},
		// A day is one on the calendar, whatever the clock does.
		{"+1d", "2026-10-25T14:30:15+01:00"},
		{"+1w", "2026-10-31T14:30:15+01:00"},
		{"+1w 00:00", "2026-10-31T00:00:00+01:00"},
	}
	for _, tt := range tests {
		got, err := definition.ParseTime(tt.text, now)
		if err != nil {
			t.Errorf("ParseTime(%q): %v", tt.text, err)
			continue
		}
		if text := got.Format(time.RFC3339); text != tt.want {
			t.Errorf("ParseTime(%q) = %s, want %s", tt.text, text, tt.want)
		}
	}
}

func TestParseTimeRejectsWhatIsNoTime(t *testing.T) {
	now := time.Date(2026, time.October, 24, 14, 30, 0, 0, time.UTC)
	for _, text := range []string{"", "soon", "+1y", "-1d", "+d", "tomorrow noon", "tomorrow 08:00 sharp", "24:00", "tomorrow 08:60", "2026-13-01"} {
		_, err := definition.ParseTime(text, now)
		if err == nil {
			t.Errorf("ParseTime(%q) succeeded", text)
			continue
		}
		if !strings.Contains(err.Error(), "no time") {
			t.Errorf("ParseTime(%q) = %v, want an error that says it is no time", text, err)
		}
	}
}
