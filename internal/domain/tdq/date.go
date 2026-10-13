package tdq

import "time"

// dateLiteral is a resolved date on the right-hand side of a comparison.
// dayOnly records whether the query named a day ("2026-05-09", "today", "-7d")
// or an instant ("-3h"), which decides the granularity of the comparison.
type dateLiteral struct {
	t       time.Time
	dayOnly bool
}

// dateLiteralLayouts are the layouts a resolved date literal can take, in the
// order they are tried. resolveDate produces the first two; the ISO forms are
// accepted so a caller-supplied timestamp also works.
var dateLiteralLayouts = []struct {
	layout  string
	dayOnly bool
}{
	{"2006-01-02 15:04:05", false},
	{"2006-01-02T15:04:05Z07:00", false},
	{"2006-01-02T15:04:05", false},
	{"2006-01-02", true},
}

// parseDateLiteral parses an already-resolved date literal in loc.
func parseDateLiteral(s string, loc *time.Location) (dateLiteral, bool) {
	for _, l := range dateLiteralLayouts {
		if t, err := time.ParseInLocation(l.layout, s, loc); err == nil {
			return dateLiteral{t: t, dayOnly: l.dayOnly}, true
		}
	}
	return dateLiteral{}, false
}
