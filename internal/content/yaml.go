package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	stj "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// yamlDoc is one parsed YAML file: the node tree (for line numbers, comments
// and the TODO inventory) and the JSON-typed value (for schema validation and
// struct decoding).
type yamlDoc struct {
	file string
	raw  []byte
	root *yaml.Node
	json []byte
}

func parseYAML(rel string, raw []byte) (*yamlDoc, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var v any
	if root.Kind != 0 {
		if err := root.Decode(&v); err != nil {
			return nil, err
		}
	}
	v = normalize(v)
	if v == nil {
		return nil, fmt.Errorf("empty document")
	}
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &yamlDoc{file: rel, raw: raw, root: &root, json: j}, nil
}

// normalize converts yaml.v3's map[string]any / map[any]any trees into JSON-marshalable values.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalize(val)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []any:
		for i, val := range t {
			t[i] = normalize(val)
		}
		return t
	}
	return v
}

// decodeStrict decodes JSON into a struct rejecting unknown keys.
func decodeStrict(j []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// validateJSON runs a compiled schema over JSON bytes and returns leaf errors
// as (json pointer tokens, message) pairs.
func validateJSON(sch *stj.Schema, j []byte) ([]schemaErr, error) {
	inst, err := stj.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		return nil, err
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil, nil
	}
	ve, ok := err.(*stj.ValidationError)
	if !ok {
		return nil, err
	}
	var out []schemaErr
	seen := map[string]bool{}
	var walk func(u stj.OutputUnit)
	walk = func(u stj.OutputUnit) {
		if u.Error != nil && len(u.Errors) == 0 {
			msg := u.Error.String()
			key := u.InstanceLocation + "|" + msg
			if !seen[key] && !isAggregateMsg(msg) {
				seen[key] = true
				out = append(out, schemaErr{ptr: splitPointer(u.InstanceLocation), msg: msg})
			}
		}
		for _, c := range u.Errors {
			walk(c)
		}
	}
	walk(*ve.DetailedOutput())
	if len(out) == 0 {
		out = append(out, schemaErr{msg: ve.Error()})
	}
	return out, nil
}

func isAggregateMsg(msg string) bool {
	for _, p := range []string{"anyOf failed", "oneOf failed", "allOf failed", "validation failed", "if failed", "then failed", "else failed"} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

type schemaErr struct {
	ptr []string
	msg string
}

func splitPointer(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		s = strings.ReplaceAll(s, "~1", "/")
		parts[i] = strings.ReplaceAll(s, "~0", "~")
	}
	return parts
}

// jsonPath renders pointer tokens as $.a.b[0].
func jsonPath(tokens []string) string {
	var sb strings.Builder
	sb.WriteString("$")
	for _, t := range tokens {
		if _, err := strconv.Atoi(t); err == nil {
			sb.WriteString("[" + t + "]")
		} else {
			sb.WriteString("." + t)
		}
	}
	return sb.String()
}

// docNode returns the value node of a document root.
func docNode(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// locate walks the node tree along JSON pointer tokens and returns the node reached (or the deepest one found).
func locate(root *yaml.Node, tokens []string) *yaml.Node {
	n := docNode(root)
	for _, tok := range tokens {
		if n == nil {
			return nil
		}
		switch n.Kind {
		case yaml.MappingNode:
			var next *yaml.Node
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == tok {
					next = n.Content[i+1]
					break
				}
			}
			if next == nil {
				return n
			}
			n = next
		case yaml.SequenceNode:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(n.Content) {
				return n
			}
			n = n.Content[i]
		default:
			return n
		}
	}
	return n
}

// mapValue returns the value node for key in a mapping node.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

var dateKeys = map[string]bool{"start": true, "end": true, "ts": true, "date": true, "updated_at": true}

// checkDatesQuoted implements rule yaml.date-quoted: date fields must be strings.
func (l *loader) checkDatesQuoted(doc *yamlDoc) {
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if dateKeys[k.Value] && v.Kind == yaml.ScalarNode && (v.Tag == "!!int" || v.Tag == "!!float" || v.Tag == "!!timestamp") {
					l.c.Report.errorf(doc.file, v.Line, v.Column, "yaml.date-quoted", "", "quote the date: %s: %s", k.Value, v.Value)
				}
			}
		}
		for _, ch := range n.Content {
			walk(ch)
		}
	}
	walk(doc.root)
}
