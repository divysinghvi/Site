package model

import (
	"bytes"

	"github.com/invopop/jsonschema"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func stringEnum(values ...string) *jsonschema.Schema {
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = v
	}
	return &jsonschema.Schema{Type: "string", Enum: enum}
}

// SpanStatus is the OTel-style status of a career span.
type SpanStatus string

// JSONSchema lists the span statuses.
func (SpanStatus) JSONSchema() *jsonschema.Schema { return stringEnum("ok", "error") }

// LinkKind is the kind of an outbound link of a span.
type LinkKind string

// JSONSchema lists the link kinds.
func (LinkKind) JSONSchema() *jsonschema.Schema {
	return stringEnum("postmortem", "repo", "pr", "pypi", "url")
}

// LogLevel is the level of a log line.
type LogLevel string

// JSONSchema lists the log levels.
func (LogLevel) JSONSchema() *jsonschema.Schema { return stringEnum("debug", "info", "warn", "error") }

// LogPrecision says what a log line's timestamp stands for.
type LogPrecision string

// JSONSchema lists the precisions.
func (LogPrecision) JSONSchema() *jsonschema.Schema { return stringEnum("day", "month", "year") }

// Severity is a postmortem severity.
type Severity string

// JSONSchema lists SEV1..SEV4.
func (Severity) JSONSchema() *jsonschema.Schema { return stringEnum("SEV1", "SEV2", "SEV3", "SEV4") }

// PostmortemStatus is the lifecycle state of a postmortem.
type PostmortemStatus string

// JSONSchema lists the postmortem statuses.
func (PostmortemStatus) JSONSchema() *jsonschema.Schema {
	return stringEnum("resolved", "monitoring", "open")
}

// PanelType is the visualisation of a panel.
type PanelType string

// JSONSchema lists the panel types.
func (PanelType) JSONSchema() *jsonschema.Schema {
	return stringEnum("timeseries", "stat", "gauge", "bargauge")
}

// ThresholdMode is absolute or percentage.
type ThresholdMode string

// JSONSchema lists the threshold modes.
func (ThresholdMode) JSONSchema() *jsonschema.Schema { return stringEnum("absolute", "percentage") }

// PaletteColor is a theme palette token used by thresholds.
type PaletteColor string

// JSONSchema lists the palette tokens.
func (PaletteColor) JSONSchema() *jsonschema.Schema {
	return stringEnum("green", "yellow", "red", "blue", "orange", "purple")
}

// SourceKind says where a panel's numbers come from.
type SourceKind string

// JSONSchema lists the source kinds.
func (SourceKind) JSONSchema() *jsonschema.Schema {
	return stringEnum("github", "pypi", "manual", "process", "content")
}

// TimeOption is a dashboard time-range preset.
type TimeOption string

// JSONSchema lists the presets.
func (TimeOption) JSONSchema() *jsonschema.Schema { return stringEnum("24h", "7d", "30d", "1y", "all") }

// ProbeMethod is the HTTP method of an uptime probe.
type ProbeMethod string

// JSONSchema lists GET and HEAD.
func (ProbeMethod) JSONSchema() *jsonschema.Schema { return stringEnum("GET", "HEAD") }

// PodStatus mimics kubectl's STATUS column.
type PodStatus string

// JSONSchema lists the pod statuses.
func (PodStatus) JSONSchema() *jsonschema.Schema {
	return stringEnum("Running", "Pending", "Completed", "CrashLoopBackOff")
}

// RestartsFrom says how a pod's RESTARTS column is computed.
type RestartsFrom string

// JSONSchema lists the restart sources.
func (RestartsFrom) JSONSchema() *jsonschema.Schema { return stringEnum("postmortems", "none") }

// ReadyStatus is the readiness state reported by /readyz.
type ReadyStatus string

// JSONSchema lists the readiness states.
func (ReadyStatus) JSONSchema() *jsonschema.Schema {
	return stringEnum("ok", "unavailable", "shutting_down")
}

// UptimeStatus is the current state of an uptime target.
type UptimeStatus string

// JSONSchema lists the uptime states.
func (UptimeStatus) JSONSchema() *jsonschema.Schema {
	return stringEnum("up", "down", "unconfigured", "unknown")
}

// PromErrorType is a Prometheus API errorType.
type PromErrorType string

// JSONSchema lists the Prometheus error types.
func (PromErrorType) JSONSchema() *jsonschema.Schema {
	return stringEnum("bad_data", "execution", "timeout", "internal", "unavailable", "not_found")
}
