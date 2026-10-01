package web

import (
	"fmt"
	"time"
)

// ago renders a time relative to now, for "fetched 12 min ago": the useful
// precision falls off with age, so it does too.
func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return day(t)
	}
}

// day renders a time as its local date, "3 Oct 14:05", or "" when unknown.
func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2 Jan 15:04")
}
