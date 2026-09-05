package promql

import (
	"testing"
)

// parserCases mirrors docs/drafts/promql.md §P.10.1: want is Expr.String(),
// err is the full error text. Rows verified against Prometheus 3.14's parser
// keep its messages; rows for constructs outside the subset carry our own.
var parserCases = []struct {
	name, in, want, err string
}{
	{"1 name", `github_commits_total`, `github_commits_total`, ""},
	{"2 name matcher", `{__name__="github_commits_total"}`, `{__name__="github_commits_total"}`, ""},
	{"3 matchers", `github_merged_prs_total{org!="gradr",repo=~"k.*"}`, `github_merged_prs_total{org!="gradr",repo=~"k.*"}`, ""},
	{"4 trailing comma", `github_stars{repo!~"a|b",}`, `github_stars{repo!~"a|b"}`, ""},
	{"5 empty regex", `{job=~".*"}`, "", `1:1: parse error: vector selector must contain at least one non-empty matcher`},
	{"6 empty braces", `{}`, "", `1:1: parse error: vector selector must contain at least one non-empty matcher`},
	{"7 name twice", `github_commits_total{__name__="x"}`, "", `1:1: parse error: metric name must not be set twice: "github_commits_total" or "x"`},
	{"8 range 7d", `github_commits_total[7d]`, `github_commits_total[1w]`, ""},
	{"9 range 1h30m", `github_commits_total[1h30m]`, `github_commits_total[1h30m]`, ""},
	{"10 range 90s", `github_commits_total[90s]`, `github_commits_total[1m30s]`, ""},
	{"11 range 1.5h", `github_commits_total[1.5h]`, "", `1:22: parse error: unknown unit "." in duration "1.5h"`},
	{"12 range 0s", `github_commits_total[0s]`, "", `1:22: parse error: duration must be greater than 0`},
	{"13 unclosed bracket", `github_commits_total[7d`, "", `1:24: parse error: unclosed left bracket`},
	{"14 range 1x", `github_commits_total[1x]`, "", `1:22: parse error: bad number or duration syntax: "1"`},
	{"15a exponent", `1e3`, `1000`, ""},
	{"15b hex", `0x1F`, `31`, ""},
	{"15c inf", `Inf`, `+Inf`, ""},
	{"15d nan", `NaN`, `NaN`, ""},
	{"15e underscore", `1_000`, `1000`, ""},
	{"16 unary vector", `-github_stars`, `-github_stars`, ""},
	{"17 double negation", `- - 1`, `1`, ""},
	{"18 precedence", `1 + 2 * 3 ^ 2 ^ 1`, `1 + 2 * 3 ^ 2 ^ 1`, ""},
	{"19 division", `github_stars / github_followers`, `github_stars / github_followers`, ""},
	{"20 comparison", `divy_open_to_work == 1`, `divy_open_to_work == 1`, ""},
	{"21 scalar cmp", `1 == 2`, "", `1:3: parse error: comparisons between scalars must use BOOL modifier`},
	{"22 scalar cmp bool", `1 == bool 2`, `1 == bool 2`, ""},
	{"23 bool on arith", `github_stars + bool 1`, "", `1:14: parse error: bool modifier can only be used on comparison operators`},
	{"24 sum by", `sum by (org) (github_merged_prs_total)`, `sum by (org) (github_merged_prs_total)`, ""},
	{"25 sum by trailing", `sum(github_merged_prs_total) by (org,)`, `sum by (org) (github_merged_prs_total)`, ""},
	{"26 sum without", `sum without (org) (github_merged_prs_total)`, `sum without (org) (github_merged_prs_total)`, ""},
	{"27 sum by empty", `sum(github_merged_prs_total) by ()`, `sum(github_merged_prs_total)`, ""},
	{"28 sum no args", `sum()`, "", `1:1: parse error: no arguments for aggregate expression provided`},
	{"29 unclosed paren", `sum(`, "", `1:5: parse error: unclosed left parenthesis`},
	{"30 sum two args", `sum(github_stars, github_followers)`, "", `1:1: parse error: wrong number of arguments for aggregate expression provided, expected 1, got 2`},
	{"31 rate", `rate(github_commits_total[7d])`, `rate(github_commits_total[1w])`, ""},
	{"32 rate instant", `rate(github_commits_total)`, "", `1:6: parse error: expected type range vector in call to function "rate", got instant vector`},
	{"33 rate arity", `rate(github_commits_total[7d], 1)`, "", `1:1: parse error: expected 1 argument(s) in call to "rate", got 2`},
	{"34a increase", `increase(x[1d])`, `increase(x[1d])`, ""},
	{"34b irate", `irate(x[1d])`, `irate(x[1d])`, ""},
	{"34c delta", `delta(x[1d])`, `delta(x[1d])`, ""},
	{"34d sum_over_time", `sum_over_time(x[1h])`, `sum_over_time(x[1h])`, ""},
	{"34e avg_over_time", `avg_over_time(x[1h])`, `avg_over_time(x[1h])`, ""},
	{"34f min_over_time", `min_over_time(x[1h])`, `min_over_time(x[1h])`, ""},
	{"34g max_over_time", `max_over_time(x[1h])`, `max_over_time(x[1h])`, ""},
	{"34h count_over_time", `count_over_time(x[1h])`, `count_over_time(x[1h])`, ""},
	{"34i last_over_time", `last_over_time(x[1h])`, `last_over_time(x[1h])`, ""},
	{"35a abs", `abs(x)`, `abs(x)`, ""},
	{"35b ceil", `ceil(x)`, `ceil(x)`, ""},
	{"35c floor", `floor(x)`, `floor(x)`, ""},
	{"35d round", `round(x)`, `round(x)`, ""},
	{"35e round nearest", `round(x, 0.5)`, `round(x, 0.5)`, ""},
	{"35f clamp_min", `clamp_min(x, 0)`, `clamp_min(x, 0)`, ""},
	{"35g clamp_max", `clamp_max(x, 10)`, `clamp_max(x, 10)`, ""},
	{"35h vector", `vector(1)`, `vector(1)`, ""},
	{"35i scalar", `scalar(x)`, `scalar(x)`, ""},
	{"35j time", `time()`, `time()`, ""},
	{"36 round arity", `round(x, 1, 2)`, "", `1:1: parse error: expected at most 2 argument(s) in call to "round", got 3`},
	{"37 time arity", `time(x)`, "", `1:1: parse error: expected 0 argument(s) in call to "time", got 1`},
	{"38 scalar type", `scalar(1)`, "", `1:8: parse error: expected type instant vector in call to function "scalar", got scalar`},
	{"39 unknown function", `foo(x)`, "", `1:1: parse error: unknown function with name "foo"`},
	{"40 removed function", `holt_winters(x[1h], 0.5, 0.5)`, "", `1:1: parse error: unknown function with name "holt_winters"`},
	{"41 string", `"a string"`, `"a string"`, ""},
	{"42 unclosed paren", `(github_stars`, "", `1:14: parse error: unclosed left parenthesis`},
	{"43 unexpected paren", `github_stars)`, "", `1:14: parse error: unexpected right parenthesis ')'`},
	{"44 empty", ``, "", `unknown position: parse error: no expression found in input`},
	{"45 dangling op", `github_stars +`, "", `1:15: parse error: unexpected end of input`},
	{"46 two identifiers", `github stars`, "", `1:8: parse error: unexpected identifier "stars"`},
	{"47 missing comma", `github_stars{repo="a" repo="b"}`, "", `1:23: parse error: unexpected identifier "repo" in label matching, expected "," or "}"`},
	{"48 missing value", `github_stars{repo=}`, "", `1:19: parse error: unexpected "}" in label matching, expected string`},
	{"49 unclosed brace", `github_stars{repo="a"`, "", `1:22: parse error: unexpected end of input inside braces`},
	{"50 bad regex", `github_stars{repo=~"("}`, "", "1:14: parse error: error parsing regexp: missing closing ): `(`"},
	{"51 range binop", `a[5m] + b[5m]`, "", `1:1: parse error: binary expression must contain only scalar and instant vector types`},
	{"52 range on call", `rate(x[5m])[5m]`, "", `1:12: parse error: ranges only allowed for vector selectors`},
	{"53 count by cmp", `count(github_stars) by (repo) > 0`, `count by (repo) (github_stars) > 0`, ""},
	{"54 bool alone", `bool`, "", `1:1: parse error: unexpected <bool>`},
	{"55 dangling and", `x and`, "", `1:6: parse error: unexpected end of input`},
	{"56 offset", `github_commits_total offset 1d`, "", `1:22: parse error: offset modifier is not supported`},
	{"57 at", `github_commits_total @ 1609746000`, "", `1:22: parse error: @ modifier is not supported`},
	{"57b at start", `github_commits_total @ start()`, "", `1:22: parse error: @ modifier is not supported`},
	{"58 subquery", `rate(github_commits_total[7d:1h])`, "", `1:29: parse error: subqueries are not supported`},
	{"59 duration expr", `rate(x[5m * 2])`, "", `1:11: parse error: unexpected <op:*> in range selector, expected "]"`},
	{"60 and", `github_stars and github_followers`, "", `1:14: parse error: set operator "and" is not supported`},
	{"61a or", `github_stars or vector(0)`, "", `1:14: parse error: set operator "or" is not supported`},
	{"61b unless", `a unless b`, "", `1:3: parse error: set operator "unless" is not supported`},
	{"61c atan2", `a atan2 b`, "", `1:3: parse error: binary operator "atan2" is not supported`},
	{"62 on", `github_stars + on(repo) github_stars`, "", `1:16: parse error: vector matching modifier "on" is not supported`},
	{"63 ignoring group_left", `github_stars + ignoring(repo) group_left github_followers`, "", `1:16: parse error: vector matching modifier "ignoring" is not supported`},
	{"64a topk", `topk(3, github_stars)`, "", `1:1: parse error: aggregation operator "topk" is not supported`},
	{"64b quantile", `quantile(0.9, x)`, "", `1:1: parse error: aggregation operator "quantile" is not supported`},
	{"64c count_values", `count_values("v", x)`, "", `1:1: parse error: aggregation operator "count_values" is not supported`},
	{"64d stddev", `stddev(x)`, "", `1:1: parse error: aggregation operator "stddev" is not supported`},
	{"64e group", `group(x)`, "", `1:1: parse error: aggregation operator "group" is not supported`},
	{"65a histogram_quantile", `histogram_quantile(0.9, x)`, "", `1:1: parse error: function "histogram_quantile" is not supported`},
	{"65b label_join", `label_join(x, "a", ",", "b")`, "", `1:1: parse error: function "label_join" is not supported`},
	{"65c absent", `absent(x)`, "", `1:1: parse error: function "absent" is not supported`},
	{"65d predict_linear", `predict_linear(x[1h], 3600)`, "", `1:1: parse error: function "predict_linear" is not supported`},
	{"65e changes", `changes(x[1h])`, "", `1:1: parse error: function "changes" is not supported`},
	{"65f sort", `sort(x)`, "", `1:1: parse error: function "sort" is not supported`},
	{"65g timestamp", `timestamp(x)`, "", `1:1: parse error: function "timestamp" is not supported`},
	{"65h day_of_week", `day_of_week()`, "", `1:1: parse error: function "day_of_week" is not supported`},
	{"65i clamp", `clamp(x, 0, 10)`, "", `1:1: parse error: function "clamp" is not supported`},
	{"66 utf8 name", `{"a.b"="1"}`, "", `1:2: parse error: unexpected string "a.b" in label matching, expected identifier or "}"`},
	{"67 alert expr", `sum(rate(github_commits_total[7d])) > 20`, `sum(rate(github_commits_total[1w])) > 20`, ""},
	{"68 panel expr", `sum(increase(pypi_downloads_total{package="codemind-ci"}[1d]))`, `sum(increase(pypi_downloads_total{package="codemind-ci"}[1d]))`, ""},
	{"69 lfx expr", `lfx_applications{status="pending"} > 0`, `lfx_applications{status="pending"} > 0`, ""},
	{"70a keyword case", `SUM(X)`, `sum(X)`, ""},
	{"70b function case", `Rate(x[5m])`, "", `1:1: parse error: unknown function with name "Rate"`},
	// extra rows: literals, strings, comments, keywords as names
	{"71 string operand", `"a" + 1`, "", `1:1: parse error: binary expression must contain only scalar and instant vector types`},
	{"72 unary range", `-x[5m]`, "", `1:1: parse error: unary expression only allowed on expressions of type scalar or instant vector, got "range vector"`},
	{"73 paren range", `(x)[5m]`, "", `1:4: parse error: ranges only allowed for vector selectors`},
	{"74 neg pow", `-2 ^ 2`, `-2 ^ 2`, ""},
	{"75 comment", "github_stars # stars\n", `github_stars`, ""},
	{"76 keyword metric", `sum{}`, `sum`, ""},
	{"77 keyword metric op", `by + 1`, `by + 1`, ""},
	{"78 single quotes", `x{a='b\'c'}`, `x{a="b'c"}`, ""},
	{"79 raw string", "x{a=`b\\c`}", `x{a="b\\c"}`, ""},
	{"80 escape", `x{a="\t\x41"}`, `x{a="\tA"}`, ""},
	{"81 bad escape", `x{a="\q"}`, "", `1:5: parse error: unknown escape sequence U+0071 'q'`},
	{"82 unterminated string", `x{a="b}`, "", `1:5: parse error: unterminated quoted string`},
	{"83 bad char", `x § y`, "", `1:3: parse error: unexpected character: '§'`},
	{"84 single eq", `x = 1`, "", `1:3: parse error: unexpected "="`},
	{"85 bang", `x !x`, "", `1:3: parse error: unexpected character after '!': 'x'`},
	{"86 trailing comma call", `rate(x[5m],)`, "", `1:11: parse error: trailing commas not allowed in function call args`},
	{"87 grouping missing paren", `sum by x (y)`, "", `1:8: parse error: unexpected identifier "x" in grouping opts, expected "("`},
	{"88 grouping bad label", `sum by (1) (y)`, "", `1:9: parse error: unexpected number "1" in grouping opts, expected label`},
	{"89 grouping missing comma", `sum by (a b) (y)`, "", `1:11: parse error: unexpected identifier "b" in grouping opts, expected "," or ")"`},
	{"90 aggregation body", `sum by (a) y`, "", `1:12: parse error: unexpected identifier "y" in aggregation`},
	{"91 matcher op missing", `x{a"b"}`, "", `1:4: parse error: unexpected string "\"b\"" in label matching, expected label matching operator`},
	{"92 matcher keyword label", `x{by="1",on!="2"}`, `x{by="1",on!="2"}`, ""},
	{"93 number range", `x[60]`, `x[1m]`, ""},
	{"94 group_right", `a / group_right b`, "", `1:5: parse error: vector matching modifier "group_right" is not supported`},
	{"95 scalar set op", `1 and 2`, "", `1:1: parse error: set operator "and" not allowed in binary scalar expression`},
	{"96 anchored", `x[5m] anchored`, "", `1:7: parse error: unexpected identifier "anchored"`},
	{"97 nested", `sum by (org) (rate(github_merged_prs_total{org=~"kube.*"}[30d])) * 86400 > bool 0.5`, `sum by (org) (rate(github_merged_prs_total{org=~"kube.*"}[30d])) * 86400 > bool 0.5`, ""},
	{"98 multiline", "sum(\n  github_stars\n) +", "", `3:4: parse error: unexpected end of input`},
	{"99 lookback expr", `last_over_time(github_stars[26h])`, `last_over_time(github_stars[1d2h])`, ""},
	{"100 metric identifier", `job:rate:5m`, `job:rate:5m`, ""},
}

func TestParser(t *testing.T) {
	for _, tc := range parserCases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := ParseExpr(tc.in)
			if tc.err != "" {
				if err == nil {
					t.Fatalf("want error %q, got expr %q", tc.err, expr.String())
				}
				if err.Error() != tc.err {
					t.Fatalf("error mismatch\n want: %s\n  got: %s", tc.err, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := expr.String(); got != tc.want {
				t.Fatalf("String() mismatch\n want: %s\n  got: %s", tc.want, got)
			}
			// the canonical form must re-parse to itself
			again, err := ParseExpr(expr.String())
			if err != nil {
				t.Fatalf("re-parse of %q: %v", expr.String(), err)
			}
			if again.String() != expr.String() {
				t.Fatalf("re-parse changed %q to %q", expr.String(), again.String())
			}
		})
	}
}

func TestParseMetricSelector(t *testing.T) {
	cases := []struct{ in, want, err string }{
		{`github_stars`, `[__name__="github_stars"]`, ""},
		{`github_stars{repo="a"}`, `[repo="a" __name__="github_stars"]`, ""},
		{`{__name__=~"probe_.*"}`, `[__name__=~"probe_.*"]`, ""},
		{`{job=~".*"}`, `[job=~".*"]`, ""}, // the API layer rejects it (no non-empty matcher)
		{`github_stars{`, "", `1:14: parse error: unexpected end of input inside braces`},
		{`sum(x)`, "", `1:4: parse error: unexpected "("`},
		{``, "", `unknown position: parse error: unexpected end of input`},
		{`1`, "", `1:1: parse error: unexpected number "1"`},
	}
	for _, tc := range cases {
		ms, err := ParseMetricSelector(tc.in)
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%q: want error %q, got %v", tc.in, tc.err, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		got := "["
		for i, m := range ms {
			if i > 0 {
				got += " "
			}
			got += m.String()
		}
		got += "]"
		if got != tc.want {
			t.Errorf("%q: want %s, got %s", tc.in, tc.want, got)
		}
	}
}

func TestDurations(t *testing.T) {
	cases := []struct {
		in   string
		want string // FormatDuration(parsed) or the error
	}{
		{"7d", "1w"}, {"90s", "1m30s"}, {"1h30m", "1h30m"}, {"365d", "1y"}, {"14d", "2w"}, {"1500ms", "1s500ms"},
		{"1d1w", `not a valid duration string: "1d1w"`}, {"1.5h", `unknown unit "." in duration "1.5h"`},
		{"", "empty duration string"}, {"0", "0s"}, {"1hs", `unknown unit "hs" in duration "1hs"`},
		{"1000000y", "duration out of range"},
	}
	for _, tc := range cases {
		d, err := ParseDuration(tc.in)
		got := ""
		if err != nil {
			got = err.Error()
		} else {
			got = FormatDuration(d)
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}
	api := []struct {
		in   string
		want string
	}{
		{"15", "15s"}, {"0.5", "500ms"}, {"1d", "1d"}, {"1h30m", "1h30m"}, {"abc", `cannot parse "abc" to a valid duration`}, {"1e30", `cannot parse "1e30" to a valid duration. It overflows int64`},
	}
	for _, tc := range api {
		d, err := ParseAPIDuration(tc.in)
		got := ""
		if err != nil {
			got = err.Error()
		} else {
			got = FormatDuration(d)
		}
		if got != tc.want {
			t.Errorf("ParseAPIDuration(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}
}
