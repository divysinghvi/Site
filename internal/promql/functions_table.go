package promql

// Function describes a supported function's signature.
type Function struct {
	Name       string
	ArgTypes   []ValueType
	Variadic   int // 0 = exact arity; n > 0 = the last ArgType may repeat up to n times (round)
	ReturnType ValueType
}

// Functions is the supported function table (docs/promql-subset.md).
var Functions = map[string]*Function{
	"abs":             {Name: "abs", ArgTypes: []ValueType{ValueTypeVector}, ReturnType: ValueTypeVector},
	"ceil":            {Name: "ceil", ArgTypes: []ValueType{ValueTypeVector}, ReturnType: ValueTypeVector},
	"floor":           {Name: "floor", ArgTypes: []ValueType{ValueTypeVector}, ReturnType: ValueTypeVector},
	"round":           {Name: "round", ArgTypes: []ValueType{ValueTypeVector, ValueTypeScalar}, Variadic: 1, ReturnType: ValueTypeVector},
	"clamp_min":       {Name: "clamp_min", ArgTypes: []ValueType{ValueTypeVector, ValueTypeScalar}, ReturnType: ValueTypeVector},
	"clamp_max":       {Name: "clamp_max", ArgTypes: []ValueType{ValueTypeVector, ValueTypeScalar}, ReturnType: ValueTypeVector},
	"rate":            {Name: "rate", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"increase":        {Name: "increase", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"irate":           {Name: "irate", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"delta":           {Name: "delta", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"sum_over_time":   {Name: "sum_over_time", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"avg_over_time":   {Name: "avg_over_time", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"min_over_time":   {Name: "min_over_time", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"max_over_time":   {Name: "max_over_time", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"count_over_time": {Name: "count_over_time", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"last_over_time":  {Name: "last_over_time", ArgTypes: []ValueType{ValueTypeMatrix}, ReturnType: ValueTypeVector},
	"time":            {Name: "time", ArgTypes: []ValueType{}, ReturnType: ValueTypeScalar},
	"vector":          {Name: "vector", ArgTypes: []ValueType{ValueTypeScalar}, ReturnType: ValueTypeVector},
	"scalar":          {Name: "scalar", ArgTypes: []ValueType{ValueTypeVector}, ReturnType: ValueTypeScalar},
}

// prometheusFunctions lists every other function Prometheus 3.14 knows; a
// call to one of them is `function "x" is not supported` rather than
// `unknown function with name "x"`.
var prometheusFunctions = map[string]bool{}

func init() {
	for _, n := range []string{
		"absent", "absent_over_time", "acos", "acosh", "asin", "asinh", "atan", "atanh", "changes", "clamp",
		"cos", "cosh", "days_in_month", "day_of_month", "day_of_week", "day_of_year", "deg", "end", "deriv",
		"exp", "first_over_time", "histogram_avg", "histogram_count", "histogram_sum", "histogram_stddev",
		"histogram_stdvar", "histogram_fraction", "histogram_quantile", "histogram_quantiles",
		"double_exponential_smoothing", "hour", "idelta", "info", "label_replace", "label_join", "max_of",
		"min_of", "ln", "log10", "log2", "mad_over_time", "ts_of_first_over_time", "ts_of_max_over_time",
		"ts_of_min_over_time", "ts_of_last_over_time", "minute", "month", "pi", "predict_linear",
		"present_over_time", "quantile_over_time", "rad", "range", "resets", "sgn", "sin", "sinh", "sort",
		"sort_desc", "sort_by_label", "sort_by_label_desc", "sqrt", "start", "start_timestamp", "step",
		"stddev_over_time", "stdvar_over_time", "tan", "tanh", "timestamp", "year",
	} {
		prometheusFunctions[n] = true
	}
}
