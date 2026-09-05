package promql

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// durationUnits is the Prometheus unit table: position enforces the
// descending order y w d h m s ms, mult is the unit in nanoseconds.
var durationUnits = map[string]struct {
	pos  int
	mult uint64
}{
	"ms": {7, uint64(time.Millisecond)},
	"s":  {6, uint64(time.Second)},
	"m":  {5, uint64(time.Minute)},
	"h":  {4, uint64(time.Hour)},
	"d":  {3, uint64(24 * time.Hour)},
	"w":  {2, uint64(7 * 24 * time.Hour)},
	"y":  {1, uint64(365 * 24 * time.Hour)},
}

func isDigitByte(b byte) bool { return '0' <= b && b <= '9' }

// ParseDuration is a port of prometheus/common model.ParseDuration: units
// y w d h m s ms in strictly descending order, each at most once, "0" allowed
// without a unit, and the same error texts.
func ParseDuration(s string) (time.Duration, error) {
	switch s {
	case "0":
		return 0, nil
	case "":
		return 0, errors.New("empty duration string")
	}
	orig := s
	var dur uint64
	lastUnitPos := 0
	for s != "" {
		if !isDigitByte(s[0]) {
			return 0, fmt.Errorf("not a valid duration string: %q", orig)
		}
		i := 0
		for ; i < len(s) && isDigitByte(s[i]); i++ {
		}
		v, err := strconv.ParseUint(s[:i], 10, 0)
		if err != nil {
			return 0, fmt.Errorf("not a valid duration string: %q", orig)
		}
		s = s[i:]
		for i = 0; i < len(s) && !isDigitByte(s[i]); i++ {
		}
		if i == 0 {
			return 0, fmt.Errorf("not a valid duration string: %q", orig)
		}
		u := s[:i]
		s = s[i:]
		unit, ok := durationUnits[u]
		if !ok {
			return 0, fmt.Errorf("unknown unit %q in duration %q", u, orig)
		}
		if unit.pos <= lastUnitPos {
			return 0, fmt.Errorf("not a valid duration string: %q", orig)
		}
		lastUnitPos = unit.pos
		if v > 1<<63/unit.mult {
			return 0, errors.New("duration out of range")
		}
		dur += v * unit.mult
		if dur > 1<<63-1 {
			return 0, errors.New("duration out of range")
		}
	}
	return time.Duration(dur), nil
}

// FormatDuration renders a duration the way Prometheus prints it
// (model.Duration.String): y and w only when exact, then d h m s ms.
func FormatDuration(d time.Duration) string {
	ms := int64(d / time.Millisecond)
	if ms == 0 {
		return "0s"
	}
	sign := ""
	if ms < 0 {
		sign, ms = "-", -ms
	}
	r := ""
	f := func(unit string, mult int64, exact bool) {
		if exact && ms%mult != 0 {
			return
		}
		if v := ms / mult; v > 0 {
			r += fmt.Sprintf("%d%s", v, unit)
			ms -= v * mult
		}
	}
	f("y", 1000*60*60*24*365, true)
	f("w", 1000*60*60*24*7, true)
	f("d", 1000*60*60*24, false)
	f("h", 1000*60*60, false)
	f("m", 1000*60, false)
	f("s", 1000, false)
	f("ms", 1, false)
	return sign + r
}

// ParseAPIDuration parses an HTTP API duration parameter the way Prometheus'
// web/api does: float seconds first, then a Prometheus duration literal.
func ParseAPIDuration(s string) (time.Duration, error) {
	if d, err := strconv.ParseFloat(s, 64); err == nil {
		ts := d * float64(time.Second)
		if ts > float64(1<<63-1) || ts < float64(-1<<63) {
			return 0, fmt.Errorf("cannot parse %q to a valid duration. It overflows int64", s)
		}
		return time.Duration(ts), nil
	}
	if d, err := ParseDuration(s); err == nil {
		return d, nil
	}
	return 0, fmt.Errorf("cannot parse %q to a valid duration", s)
}
