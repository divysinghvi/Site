package model

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
	orderedmap "github.com/pb33f/ordered-map/v2"
)

// ---- Prometheus HTTP API (/api/v1/*) ----

// PromResultType is the resultType of a query response.
type PromResultType string

// JSONSchema lists the four result types.
func (PromResultType) JSONSchema() *jsonschema.Schema {
	return stringEnum("vector", "matrix", "scalar", "string")
}

// PromResult is the `result` of a query: a vector, a matrix, or a scalar /
// string sample pair, discriminated by the sibling resultType.
type PromResult json.RawMessage

// MarshalJSON writes the raw value ("[]" when unset).
func (r PromResult) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("[]"), nil
	}
	return json.RawMessage(r).MarshalJSON()
}

// UnmarshalJSON keeps the raw value.
func (r *PromResult) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

// orderedProps builds a properties map from name/schema pairs.
func orderedProps(pairs ...any) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
	props := jsonschema.NewProperties()
	for i := 0; i+1 < len(pairs); i += 2 {
		props.Set(pairs[i].(string), pairs[i+1].(*jsonschema.Schema))
	}
	return props
}

func samplePairSchema() *jsonschema.Schema {
	two := uint64(2)
	return &jsonschema.Schema{
		Type:     "array",
		Items:    &jsonschema.Schema{Extras: map[string]any{"type": []string{"number", "string"}}},
		MinItems: &two,
		MaxItems: &two,
	}
}

// JSONSchema declares the oneOf of vector, matrix and sample pair (contract §K.2.1).
func (PromResult) JSONSchema() *jsonschema.Schema {
	labels := &jsonschema.Schema{Type: "object", AdditionalProperties: &jsonschema.Schema{Type: "string"}}
	vector := &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{
		Type:       "object",
		Properties: orderedProps("metric", labels, "value", samplePairSchema()),
		Required:   []string{"metric", "value"},
	}}
	matrix := &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{
		Type:       "object",
		Properties: orderedProps("metric", labels, "values", &jsonschema.Schema{Type: "array", Items: samplePairSchema()}),
		Required:   []string{"metric", "values"},
	}}
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{vector, matrix, samplePairSchema()}}
}

// PromQueryData is the data of /api/v1/query and /api/v1/query_range.
type PromQueryData struct {
	ResultType PromResultType `json:"resultType"`
	Result     PromResult     `json:"result"`
}

// PromQueryResult is the success envelope of /api/v1/query and /api/v1/query_range.
type PromQueryResult struct {
	Status   string        `json:"status"`
	Data     PromQueryData `json:"data"`
	Warnings []string      `json:"warnings,omitempty"`
}

// PromSeriesResult is the body of /api/v1/series: one label set per series, including __name__.
type PromSeriesResult struct {
	Status   string              `json:"status"`
	Data     []map[string]string `json:"data"`
	Warnings []string            `json:"warnings,omitempty"`
}

// PromLabelsResult is the body of /api/v1/labels and /api/v1/label/{name}/values.
type PromLabelsResult struct {
	Status   string   `json:"status"`
	Data     []string `json:"data"`
	Warnings []string `json:"warnings,omitempty"`
}

// PromMetadata describes one metric family.
type PromMetadata struct {
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}

// PromMetadataResult is the body of /api/v1/metadata.
type PromMetadataResult struct {
	Status string                    `json:"status"`
	Data   map[string][]PromMetadata `json:"data"`
}

// PromBuildInfo is the data of /api/v1/status/buildinfo (Prometheus' PrometheusVersion shape).
type PromBuildInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Branch    string `json:"branch"`
	BuildUser string `json:"buildUser"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}

// PromBuildInfoResult is the body of /api/v1/status/buildinfo.
type PromBuildInfoResult struct {
	Status string        `json:"status"`
	Data   PromBuildInfo `json:"data"`
}

// PromAlert is an active alert (the server never evaluates rules, so none exist).
type PromAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    string            `json:"activeAt,omitempty"`
	Value       string            `json:"value"`
}

// PromAlertingRule is one alerting rule in Prometheus API shape.
type PromAlertingRule struct {
	// State is always inactive: rules are evaluated by the browser, not the server.
	State          string            `json:"state"`
	Name           string            `json:"name"`
	Query          string            `json:"query"`
	Duration       float64           `json:"duration"`
	KeepFiringFor  float64           `json:"keepFiringFor"`
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	Alerts         []PromAlert       `json:"alerts"`
	Health         string            `json:"health"`
	EvaluationTime float64           `json:"evaluationTime"`
	LastEvaluation string            `json:"lastEvaluation"`
	Type           string            `json:"type"`
}

// PromRuleGroup is one rule group.
type PromRuleGroup struct {
	Name           string             `json:"name"`
	File           string             `json:"file"`
	Rules          []PromAlertingRule `json:"rules"`
	Interval       float64            `json:"interval"`
	Limit          int                `json:"limit"`
	EvaluationTime float64            `json:"evaluationTime"`
	LastEvaluation string             `json:"lastEvaluation"`
}

// PromRuleGroups is the data of /api/v1/rules.
type PromRuleGroups struct {
	Groups []PromRuleGroup `json:"groups"`
}

// PromRulesResult is the body of /api/v1/rules.
type PromRulesResult struct {
	Status string         `json:"status"`
	Data   PromRuleGroups `json:"data"`
}

// PromAlerts is the data of /api/v1/alerts.
type PromAlerts struct {
	Alerts []PromAlert `json:"alerts"`
}

// PromAlertsResult is the body of /api/v1/alerts (always empty).
type PromAlertsResult struct {
	Status string     `json:"status"`
	Data   PromAlerts `json:"data"`
}

// PromExemplarsResult is the body of /api/v1/query_exemplars (always an empty list).
type PromExemplarsResult struct {
	Status string            `json:"status"`
	Data   []json.RawMessage `json:"data"`
}
