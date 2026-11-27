package output

import (
	"time"

	"github.com/mittwald/cli-kit/format"
)

// Age describes how long ago a point in time was, or how far ahead it is,
// seen from now: "now", "5 minutes ago", "a day ago", "1 year, 2 months
// ago", "3 hours from now".
//
// Deprecated: Use format.Ago of cli-kit.
func Age(at, now time.Time) string {
	return format.Ago(at, now)
}
