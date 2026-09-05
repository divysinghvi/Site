package content

import (
	"fmt"
	"regexp"
	"time"

	"divy.dev/internal/model"
)

// Precision says how much of a date string was given.
type Precision string

// Precision values as emitted in Jaeger tags (divy.start_precision / divy.end_precision).
const (
	PrecisionYear  Precision = "year"
	PrecisionMonth Precision = "month"
	PrecisionDay   Precision = "day"
	PrecisionTodo  Precision = "todo"
	PrecisionOpen  Precision = "open"
)

var (
	reYear  = regexp.MustCompile(`^\d{4}$`)
	reMonth = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)
	reDay   = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$`)
)

// ParseDate parses YYYY, YYYY-MM or YYYY-MM-DD into its start instant (UTC)
// and precision. TODO(divy) markers are reported as PrecisionTodo with a zero
// time and no error. Calendar-invalid days (2025-02-30) are errors.
func ParseDate(s string) (time.Time, Precision, error) {
	if model.IsTodo(s) {
		return time.Time{}, PrecisionTodo, nil
	}
	switch {
	case reYear.MatchString(s):
		t, err := time.Parse("2006", s)
		return t.UTC(), PrecisionYear, err
	case reMonth.MatchString(s):
		t, err := time.Parse("2006-01", s)
		return t.UTC(), PrecisionMonth, err
	case reDay.MatchString(s):
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return time.Time{}, PrecisionDay, fmt.Errorf("invalid calendar date %q", s)
		}
		return t.UTC(), PrecisionDay, nil
	}
	return time.Time{}, "", fmt.Errorf("invalid date %q: want YYYY, YYYY-MM, YYYY-MM-DD or TODO(divy)", s)
}

// EndOf resolves a date string used as an *end*: the first instant after the
// period it names (next year, next month or next day at 00:00:00Z).
func EndOf(t time.Time, p Precision) time.Time {
	switch p {
	case PrecisionYear:
		return t.AddDate(1, 0, 0)
	case PrecisionMonth:
		return t.AddDate(0, 1, 0)
	case PrecisionDay:
		return t.AddDate(0, 0, 1)
	}
	return t
}

// FormatRFC3339 formats t as an RFC 3339 UTC string with second precision.
func FormatRFC3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
