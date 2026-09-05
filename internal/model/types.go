// Package model holds the Go structs that are the single source of truth for
// every content file under content/ and for every JSON body the API serves.
// divy schemagen reflects them into schema/*.schema.json; the same schemas
// validate the content files and generate the TypeScript types.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/invopop/jsonschema"
)

// DatePattern is the anchored grammar shared by every date field: YYYY, YYYY-MM,
// YYYY-MM-DD or a TODO(divy) marker with an optional note.
const DatePattern = `^(\d{4}(-(0[1-9]|1[0-2])(-(0[1-9]|[12]\d|3[01]))?)?|TODO\(divy\)(: .+)?)$`

// TodoPattern matches a TODO(divy) marker with an optional ": note" suffix.
const TodoPattern = `^TODO\(divy\)(: .+)?$`

// SpanIDPattern is the dotted lowercase kebab grammar of span ids (at least two segments).
const SpanIDPattern = `^[a-z0-9]+(-[a-z0-9]+)*(\.[a-z0-9]+(-[a-z0-9]+)*)+$`

// KebabPattern is the grammar of service ids, panel ids, uptime target ids and pod names.
const KebabPattern = `^[a-z0-9]+(-[a-z0-9]+)*$`

// LabelNamePattern is the Prometheus/Loki label-name grammar used for free-form log fields.
const LabelNamePattern = `^[a-zA-Z_][a-zA-Z0-9_]*$`

// PostmortemIDPattern matches INC-NNN.
const PostmortemIDPattern = `^INC-[0-9]{3}$`

// AlertNamePattern matches Prometheus alert names as the content rules restrict them.
const AlertNamePattern = `^[A-Z][A-Za-z0-9]*$`

// DurationPattern is a Prometheus duration (15s, 1h30m, 7d) or TODO(divy).
const DurationPattern = `^(([0-9]+)(ms|s|m|h|d|w|y))+$|^TODO\(divy\)(: .+)?$`

// DateOrTodo is a date string with implied precision (YYYY, YYYY-MM, YYYY-MM-DD) or a TODO(divy) marker.
type DateOrTodo string

// JSONSchema emits the combined date-or-TODO pattern.
func (DateOrTodo) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: DatePattern}
}

// IsTodo reports whether the value is a TODO(divy) marker.
func (d DateOrTodo) IsTodo() bool { return IsTodo(string(d)) }

// IsTodo reports whether s is a TODO(divy) marker (optionally with a note).
func IsTodo(s string) bool {
	const m = "TODO(divy)"
	if len(s) < len(m) || s[:len(m)] != m {
		return false
	}
	return len(s) == len(m) || (len(s) >= len(m)+2 && s[len(m):len(m)+2] == ": ")
}

// Scalar is a JSON scalar: string, number or boolean. Used for span event
// attributes and free-form log fields.
type Scalar struct {
	Value any
}

// JSONSchema declares the three accepted JSON types.
func (Scalar) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Extras: map[string]any{"type": []string{"string", "number", "boolean"}}}
}

// MarshalJSON writes the wrapped value.
func (s Scalar) MarshalJSON() ([]byte, error) { return json.Marshal(s.Value) }

// UnmarshalJSON accepts a string, number or boolean and rejects everything else.
func (s *Scalar) UnmarshalJSON(b []byte) error {
	v, err := decodeScalar(b)
	if err != nil {
		return err
	}
	s.Value = v
	return nil
}

func decodeScalar(b []byte) (any, error) {
	dec := json.NewDecoder(bytesReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	switch t := v.(type) {
	case string, bool:
		return t, nil
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		f, err := t.Float64()
		if err != nil {
			return nil, err
		}
		return f, nil
	case nil:
		return nil, errors.New("scalar must not be null")
	default:
		return nil, fmt.Errorf("scalar must be a string, number or boolean, got %T", v)
	}
}

// TagValue is a span tag value: a scalar or a list of strings.
type TagValue struct {
	Value any // string | int64 | float64 | bool | []string
}

// JSONSchema declares scalar-or-string-list.
func (TagValue) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
		{Extras: map[string]any{"type": []string{"string", "number", "boolean"}}},
		{Type: "array", Items: &jsonschema.Schema{Type: "string"}},
	}}
}

// MarshalJSON writes the wrapped value.
func (t TagValue) MarshalJSON() ([]byte, error) { return json.Marshal(t.Value) }

// UnmarshalJSON accepts a scalar or an array of strings.
func (t *TagValue) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		var list []string
		if err := json.Unmarshal(b, &list); err != nil {
			return fmt.Errorf("tag list must contain only strings: %w", err)
		}
		t.Value = list
		return nil
	}
	v, err := decodeScalar(b)
	if err != nil {
		return err
	}
	t.Value = v
	return nil
}

// Strings returns the value as a list of strings (a scalar becomes a one-element list).
func (t TagValue) Strings() []string {
	switch v := t.Value.(type) {
	case []string:
		return v
	case string:
		return []string{v}
	case nil:
		return nil
	default:
		return []string{fmt.Sprint(v)}
	}
}

// IntOrList is an integer or a list of integers (uptime expected_status). It
// always marshals as a list.
type IntOrList []int

// JSONSchema declares integer-or-integer-list.
func (IntOrList) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
		{Type: "integer"},
		{Type: "array", Items: &jsonschema.Schema{Type: "integer"}, MinItems: uintPtr(1)},
	}}
}

// UnmarshalJSON accepts 200 or [200, 301].
func (l *IntOrList) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		var list []int
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		*l = list
		return nil
	}
	n, err := strconv.Atoi(string(b))
	if err != nil {
		return fmt.Errorf("expected an integer or a list of integers: %w", err)
	}
	*l = IntOrList{n}
	return nil
}

func uintPtr(n uint64) *uint64 { return &n }
