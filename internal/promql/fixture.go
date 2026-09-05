package promql

import (
	"fmt"
	"strconv"
	"strings"
)

// LoadFixture parses a promqltest-style series table (used by the test
// suites of this package, the server and the metrics registry):
//
//	metric{label="v"}  v0 v1 _ v3
//
// Sample i of a line sits at baseMs + i×stepMs; `_` is a gap.
func LoadFixture(text string, stepMs, baseMs int64) ([]SeriesData, error) {
	var out []SeriesData
	for ln, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		ms, err := ParseMetricSelector(fields[0])
		if err != nil {
			return nil, fmt.Errorf("fixture line %d: %w", ln+1, err)
		}
		lbls := map[string]string{}
		for _, m := range ms {
			lbls[m.Name] = m.Value
		}
		sd := SeriesData{Metric: NewLabels(lbls)}
		for i, v := range fields[1:] {
			if v == "_" {
				continue
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("fixture line %d: value %q: %w", ln+1, v, err)
			}
			sd.Points = append(sd.Points, Point{T: baseMs + int64(i)*stepMs, F: f})
		}
		out = append(out, sd)
	}
	return out, nil
}
