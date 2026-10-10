package textshot

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Unit is a unit of measure read off a screen.
type Unit string

const (
	UnitSecond  Unit = "second"
	UnitMinute  Unit = "minute"
	UnitHour    Unit = "hour"
	UnitDay     Unit = "day"
	UnitPercent Unit = "percent"
)

// IsTime reports whether the unit is a span of time.
func (u Unit) IsTime() bool {
	switch u {
	case UnitSecond, UnitMinute, UnitHour, UnitDay:
		return true
	}
	return false
}

// span is the unit's length; zero for percent.
func (u Unit) span() time.Duration {
	switch u {
	case UnitSecond:
		return time.Second
	case UnitMinute:
		return time.Minute
	case UnitHour:
		return time.Hour
	case UnitDay:
		return 24 * time.Hour
	}
	return 0
}

// Quantity is a number with a unit of measure found in an OCR line, such
// as the "3 minutes" in "About 3 minutes remaining..." or the "10%" in
// "10% complete". Value is the number as written ("1.5 hours" is 1.5,
// "10%" is 10). Text is the matched span of the line.
type Quantity struct {
	Value float64 `json:"value"`
	UOM   Unit    `json:"uom"`
	Text  string  `json:"text,omitempty"`
}

// Duration converts a time quantity to a time.Duration. ok is false for
// non-time units.
func (q Quantity) Duration() (d time.Duration, ok bool) {
	if !q.UOM.IsTime() {
		return 0, false
	}
	return time.Duration(q.Value * float64(q.UOM.span())), true
}

// Bare numbers ("Installing Windows 11") are not quantities. Time units are
// matched as a spelled-out unit after a number or the words "a", "an" or
// "one" ("Less than a minute", "About an hour"), or as a single letter
// attached to the number ("2h 30m"). Percent is "%" or the word "percent".
// Decimal commas are accepted ("12,5%"). Clock forms such as "12:34" are
// not parsed: a menu bar clock would read as a duration.
var (
	timeRe    = regexp.MustCompile(`(?i)\b(?:(\d+(?:[.,]\d+)?|an?|one)\s*(days?|hours?|hrs?|minutes?|mins?|seconds?|secs?)|(\d+(?:[.,]\d+)?)([dhms]))\b`)
	percentRe = regexp.MustCompile(`(?i)\b(\d+(?:[.,]\d+)?)\s*(?:%|percent\b)`)
)

// Quantities returns every number with a unit of measure in s, in the
// order they appear. The units are seconds, minutes, hours, days and
// percent.
func Quantities(s string) []Quantity {
	spans := quantitySpans(s)
	if len(spans) == 0 {
		return nil
	}
	out := make([]Quantity, len(spans))
	for i, sp := range spans {
		out[i] = sp.q
	}
	return out
}

type quantitySpan struct {
	q          Quantity
	start, end int
}

func quantitySpans(s string) []quantitySpan {
	var spans []quantitySpan
	for _, m := range timeRe.FindAllStringSubmatchIndex(s, -1) {
		num, unit := group(s, m, 1), group(s, m, 2)
		if unit == "" {
			num, unit = group(s, m, 3), group(s, m, 4)
		}
		v, ok := number(num)
		if !ok {
			continue
		}
		spans = append(spans, quantitySpan{Quantity{Value: v, UOM: timeUnit(unit), Text: s[m[0]:m[1]]}, m[0], m[1]})
	}
	for _, m := range percentRe.FindAllStringSubmatchIndex(s, -1) {
		v, ok := number(group(s, m, 1))
		if !ok {
			continue
		}
		spans = append(spans, quantitySpan{Quantity{Value: v, UOM: UnitPercent, Text: s[m[0]:m[1]]}, m[0], m[1]})
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	return spans
}

// ParseQuantity reduces a line to the one quantity it reports, the way
// DetectText and MergeElements fill Value and UOM: the first percentage
// when there is one ("45% (12 minutes left)" is 45 percent), otherwise the
// time quantities summed in the smallest unit present ("About 1 hour and
// 5 minutes remaining" is 65 minutes, "2h 30m" is 150 minutes). ok is
// false when the line has none, as in "Calculating time remaining…".
func ParseQuantity(s string) (q Quantity, ok bool) {
	spans := quantitySpans(s)
	var sum time.Duration
	first, last, smallest := -1, -1, -1
	for i, sp := range spans {
		if sp.q.UOM == UnitPercent {
			return sp.q, true
		}
		d, _ := sp.q.Duration()
		sum += d
		if first < 0 {
			first = i
		}
		last = i
		if smallest < 0 || sp.q.UOM.span() < spans[smallest].q.UOM.span() {
			smallest = i
		}
	}
	if first < 0 {
		return Quantity{}, false
	}
	unit := spans[smallest].q.UOM
	return Quantity{
		Value: float64(sum) / float64(unit.span()),
		UOM:   unit,
		Text:  s[spans[first].start:spans[last].end],
	}, true
}

// ParseDuration sums the time quantities in s ("About 1 hour and 5 minutes
// remaining" is 65 minutes). ok is false when s has none.
func ParseDuration(s string) (d time.Duration, ok bool) {
	for _, q := range Quantities(s) {
		if qd, isTime := q.Duration(); isTime {
			d += qd
			ok = true
		}
	}
	return d, ok
}

// ParsePercent returns the first percentage in s, as written (0 to 100).
// ok is false when s has none.
func ParsePercent(s string) (float64, bool) {
	for _, q := range Quantities(s) {
		if q.UOM == UnitPercent {
			return q.Value, true
		}
	}
	return 0, false
}

func group(s string, m []int, i int) string {
	if m[2*i] < 0 {
		return ""
	}
	return s[m[2*i]:m[2*i+1]]
}

func number(s string) (float64, bool) {
	switch strings.ToLower(s) {
	case "a", "an", "one":
		return 1, true
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	return v, err == nil
}

func timeUnit(s string) Unit {
	switch strings.ToLower(s)[0] {
	case 'd':
		return UnitDay
	case 'h':
		return UnitHour
	case 'm':
		return UnitMinute
	default:
		return UnitSecond
	}
}
