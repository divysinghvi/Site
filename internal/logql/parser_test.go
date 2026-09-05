package logql

import (
	"strings"
	"testing"
)

// parserCases mirrors docs/logql-subset.md: want is Query.String() (the
// canonical form), err is the full error text.
var parserCases = []struct {
	name, in, want, err string
}{
	{"1 selector", `{service="gradr"}`, `{service="gradr"}`, ""},
	{"2 two matchers", `{service="gradr", level!="debug"}`, `{service="gradr", level!="debug"}`, ""},
	{"3 regex matcher", `{service=~"gradr|euro-tech"}`, `{service=~"gradr|euro-tech"}`, ""},
	{"4 nre plus eq", `{service!~"oss.*", level="info"}`, `{service!~"oss.*", level="info"}`, ""},
	{"5 empty braces", `{}`, "", "parse error : " + errNonEmpty},
	{"6 empty regex", `{service=~".*"}`, "", "parse error : " + errNonEmpty},
	{"7 neq only", `{level!="debug"}`, "", "parse error : " + errNonEmpty},
	{"8 neq empty", `{level!=""}`, `{level!=""}`, ""},
	{"9 line filter", `{service="gradr"} |= "promoted"`, `{service="gradr"} |= "promoted"`, ""},
	{"10 two line filters", `{service="gradr"} != "intern" |~ "Product (Engineer|Manager)"`, `{service="gradr"} != "intern" |~ "Product (Engineer|Manager)"`, ""},
	{"11 bad regex", `{service="gradr"} |~ "["`, "", "parse error at line 1, col 22: error parsing regexp: missing closing ]: `[`"},
	{"12 raw string", "{service=\"gradr\"} |= `raw\\d`", `{service="gradr"} |= "raw\\d"`, ""},
	{"13 single quote", `{service="gradr"} |= 'x'`, "", "parse error at line 1, col 22: syntax error: unexpected '"},
	{"14 json", `{service="gradr"} | json`, `{service="gradr"} | json`, ""},
	{"15 json string filter", `{service="gradr"} | json | from="intern"`, `{service="gradr"} | json | from="intern"`, ""},
	{"16 numeric filter", `{service="gradr"} | json | containers > 60`, `{service="gradr"} | json | containers > 60`, ""},
	{"17 and", `{service="gradr"} | json | containers >= 65 and resolved="true"`, `{service="gradr"} | json | containers >= 65 and resolved="true"`, ""},
	{"18 or in parens", `{service="gradr"} | json | (incident="INC-001" or incident="INC-002"), resolved="true"`, `{service="gradr"} | json | (incident="INC-001" or incident="INC-002") and resolved="true"`, ""},
	{"19 eq number", `{service="ef-polymer"} | json | months_with_team == 12`, `{service="ef-polymer"} | json | months_with_team == 12`, ""},
	{"20 duration", `{service="gradr"} | json | duration > 5m`, `{service="gradr"} | json | duration > 5m`, ""},
	{"21 error idiom", `{service="gradr"} | json | __error__=""`, `{service="gradr"} | json | __error__=""`, ""},
	{"22 filter without json", `{service="gradr"} | level="warn"`, `{service="gradr"} | level="warn"`, ""},
	{"23 numeric vs string", `{service="gradr"} | json | containers > "x"`, "", `parse error at line 1, col 41: numeric comparison needs a number, duration or bytes literal, got string`},
	{"24 logfmt", `{service="gradr"} | logfmt`, "", `parse error at line 1, col 21: unsupported parser "logfmt" (supported: json)`},
	{"25 pattern", `{service="gradr"} | pattern "<_> msg"`, "", `parse error at line 1, col 21: unsupported parser "pattern" (supported: json)`},
	{"26 line_format", `{service="gradr"} | line_format "{{.msg}}"`, "", `parse error at line 1, col 21: unsupported stage "line_format"`},
	{"27 label_format", `{service="gradr"} | json | label_format x=y`, "", `parse error at line 1, col 28: unsupported stage "label_format"`},
	{"28 unwrap", `{service="gradr"} | unwrap containers`, "", `parse error at line 1, col 21: unsupported stage "unwrap"`},
	{"29 drop", `{service="gradr"} | drop level`, "", `parse error at line 1, col 21: unsupported stage "drop"`},
	{"30 missing pipe", `{service="gradr"} json`, "", `parse error at line 1, col 19: syntax error: unexpected IDENTIFIER "json", expecting |, |=, !=, |~, !~ or end of query`},
	{"31 count_over_time", `count_over_time({service="gradr"}[1h])`, `count_over_time({service="gradr"}[1h])`, ""},
	{"32 rate with filter", `rate({service="gradr"} |= "incident" [7d])`, `rate({service="gradr"} |= "incident" [7d])`, ""},
	{"33 range 1w", `count_over_time({service="gradr"}[1w])`, `count_over_time({service="gradr"}[1w])`, ""},
	{"34 sum by", `sum by (level) (count_over_time({service=~".+"}[30d]))`, `sum by (level) (count_over_time({service=~".+"}[30d]))`, ""},
	{"35 trailing grouping", `sum(count_over_time({service="gradr"}[1d])) by (level, detected_level)`, `sum by (level, detected_level) (count_over_time({service="gradr"}[1d]))`, ""},
	{"36 avg without", `avg without (component) (rate({level="warn"}[1d]))`, `avg without (component) (rate({level="warn"}[1d]))`, ""},
	{"37 duplicate grouping", `sum by (a) (count_over_time({level="warn"}[1d])) by (b)`, "", `parse error at line 1, col 50: duplicate grouping`},
	{"38 offset", `count_over_time({service="gradr"}[5m] offset 1h)`, "", `parse error at line 1, col 39: offset modifier is not supported`},
	{"39 bytes_rate", `bytes_rate({service="gradr"}[5m])`, "", `parse error at line 1, col 1: unsupported function "bytes_rate" (supported: count_over_time, rate)`},
	{"40 topk", `topk(3, count_over_time({service="gradr"}[1d]))`, "", `parse error at line 1, col 1: unsupported aggregation "topk" (supported: sum, count, min, max, avg)`},
	{"41 binary op", `sum(count_over_time({service="gradr"}[1d])) / 2`, "", `parse error at line 1, col 45: binary operators are only supported between vector() literals`},
	{"42 vector sum", `vector(1)+vector(1)`, `vector(2)`, ""},
	{"43 vector arithmetic", `(vector(2) - vector(0.5)) * vector(4) / vector(2)`, `vector(3)`, ""},
	{"44 unclosed selector", `{service="gradr"`, "", `parse error at line 1, col 17: syntax error: unexpected $end, expecting }`},
	{"45 unterminated string", `{service="gradr}`, "", `parse error at line 1, col 10: literal not terminated`},
	{"46 reserved label", `{service="gradr"} | json | by="x"`, "", `parse error at line 1, col 28: reserved word "by" cannot be a label name`},
	{"47 json args", `{service="gradr"} | json a="b"`, "", `parse error at line 1, col 26: json parser takes no arguments`},
	{"48 trailing comma", `{service="gradr", }`, "", `parse error at line 1, col 19: syntax error: unexpected }`},
	{"49 bytes filter", `{service="gradr"} | json | size > 3MiB`, `{service="gradr"} | json | size > 3MiB`, ""},
	{"50 by empty", `sum by () (count_over_time({service="gradr"}[1d]))`, `sum by () (count_over_time({service="gradr"}[1d]))`, ""},
	{"51 pipeline after range", `count_over_time({service="gradr"}[1d] |= "x")`, `count_over_time({service="gradr"} |= "x" [1d])`, ""},
	{"52 bare number", `2 * count_over_time({service="gradr"}[1d])`, "", `parse error at line 1, col 1: binary operators are only supported between vector() literals`},
	{"53 metric plus vector", `count_over_time({service="gradr"}[1d]) + vector(1)`, "", `parse error at line 1, col 40: binary operators are only supported between vector() literals`},
	{"54 no range", `count_over_time({service="gradr"})`, "", `parse error at line 1, col 34: syntax error: unexpected ), expecting [`},
	{"55 nested aggregation", `sum(sum(count_over_time({service="gradr"}[1d])))`, "", `parse error at line 1, col 5: syntax error: unexpected IDENTIFIER "sum", expecting count_over_time or rate`},
	{"56 unknown function", `foo({service="gradr"}[1d])`, "", `parse error at line 1, col 1: syntax error: unexpected IDENTIFIER "foo"`},
	{"57 empty", `   `, "", `parse error : syntax error: unexpected $end`},
	{"58 bad duration", `count_over_time({service="gradr"}[1m1h])`, "", `parse error at line 1, col 35: not a valid duration string: "1m1h"`},
	{"59 division by zero", `vector(1)/vector(0)`, `vector(+Inf)`, ""},
	{"60 matcher not string", `{service=gradr}`, "", `parse error at line 1, col 10: syntax error: unexpected IDENTIFIER "gradr", expecting STRING`},
	{"61 neq number filter", `{service="gradr"} | json | containers != 65`, `{service="gradr"} | json | containers != 65`, ""},
	{"62 neq line filter needs string", `{service="gradr"} != level`, "", `parse error at line 1, col 22: syntax error: unexpected IDENTIFIER "level", expecting STRING`},
	{"63 multi-line", "{service=\"gradr\"}\n  | json\n  | logfmt", "", `parse error at line 3, col 5: unsupported parser "logfmt" (supported: json)`},
}

func TestParse(t *testing.T) {
	for _, c := range parserCases {
		t.Run(c.name, func(t *testing.T) {
			q, err := Parse(c.in)
			if c.err != "" {
				if err == nil {
					t.Fatalf("Parse(%q) = %s, want error %q", c.in, q.String(), c.err)
				}
				if err.Error() != c.err {
					t.Fatalf("Parse(%q) error = %q, want %q", c.in, err.Error(), c.err)
				}
				if _, ok := err.(*ParseError); !ok {
					t.Fatalf("error type %T, want *ParseError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", c.in, err)
			}
			if got := q.String(); got != c.want {
				t.Fatalf("Parse(%q).String() = %q, want %q", c.in, got, c.want)
			}
			if strings.Contains(q.String(), "Inf") {
				return // no literal spells infinity
			}
			// the canonical form parses back to itself
			q2, err := Parse(q.String())
			if err != nil || q2.String() != q.String() {
				t.Fatalf("round trip of %q failed: %v %q", q.String(), err, q2)
			}
		})
	}
}

func TestParseShapes(t *testing.T) {
	q, err := Parse(`{service="gradr"} | json | duration > 5m`)
	if err != nil {
		t.Fatal(err)
	}
	lq := q.(*LogQuery)
	if len(lq.Stages) != 2 {
		t.Fatalf("stages = %d", len(lq.Stages))
	}
	n := lq.Stages[1].(*LabelFilter).Expr.(*LFNumber)
	if n.Kind != NumDuration || n.Value != 300 || n.Op != CmpGt {
		t.Errorf("duration filter = %+v", n)
	}
	q, err = Parse(`sum(count_over_time({service="gradr"}[1d])) by (level, detected_level)`)
	if err != nil {
		t.Fatal(err)
	}
	mq := q.(*MetricQuery)
	if mq.Agg == nil || mq.Agg.Op != "sum" || strings.Join(mq.Agg.Labels, ",") != "level,detected_level" || mq.Range.Range.Hours() != 24 || mq.Range.Fn != "count_over_time" {
		t.Errorf("metric query = %+v %+v", mq.Agg, mq.Range)
	}
	q, err = Parse(`rate({service="gradr"} |= "incident" [7d])`)
	if err != nil {
		t.Fatal(err)
	}
	if mq := q.(*MetricQuery); mq.Agg != nil || mq.Range.Range.Hours() != 168 || len(mq.Range.Log.Stages) != 1 {
		t.Errorf("rate query = %+v", mq)
	}
	if s, err := ParseSelector(`{service="gradr"} | json`); err == nil || s != nil {
		t.Errorf("ParseSelector accepted stages")
	}
	if _, err := ParseLogQuery(`vector(1)`); err == nil {
		t.Errorf("ParseLogQuery accepted a scalar")
	}
	m, err := NewMatcher("service", OpRe, "gradr|euro-tech")
	if err != nil || !m.Matches("gradr") || m.Matches("gradr-x") || m.Matches("") {
		t.Errorf("regex matcher is not anchored")
	}
}
