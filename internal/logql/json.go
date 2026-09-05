package logql

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Labels the pipeline adds when a stage fails on an entry (Loki's names).
const (
	ErrorLabel        = "__error__"
	ErrorDetailsLabel = "__error_details__"
	ErrJSONParser     = "JSONParserErr"
	ErrLabelFilter    = "LabelFilterErr"
	extractedSuffix   = "_extracted"
)

// sanitizeKey maps a JSON key to a label name: every byte outside
// [a-zA-Z0-9_] becomes `_` and a leading digit gets `_` prefixed.
func sanitizeKey(k string) string {
	if k == "" {
		return ""
	}
	var b strings.Builder
	if k[0] >= '0' && k[0] <= '9' {
		b.WriteByte('_')
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if isIdentByte(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// extractJSON parses the raw line and adds one label per scalar field to cur
// (nested objects flattened with `_`, arrays and nulls skipped). A key the
// stream already carries is skipped when the values agree and emitted as
// <key>_extracted otherwise. Invalid JSON keeps the entry and adds
// __error__="JSONParserErr" with the decoder's message in __error_details__.
func extractJSON(stream, cur Labels, line string) Labels {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	emit := func(key, value string) {
		if key == "" || key == ErrorLabel || key == ErrorDetailsLabel {
			return
		}
		if sv, ok := stream.Get(key); ok {
			if sv == value {
				return
			}
			key += extractedSuffix
		}
		cur = cur.With(key, value)
	}
	tok, err := dec.Token()
	if err == nil {
		if d, ok := tok.(json.Delim); !ok || d != '{' {
			err = errors.New("line is not a JSON object")
		} else {
			err = walkObject(dec, "", emit)
		}
	}
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		cur = cur.With(ErrorLabel, ErrJSONParser).With(ErrorDetailsLabel, err.Error())
	}
	return cur
}

// walkObject reads the members of an object whose `{` was consumed.
func walkObject(dec *json.Decoder, prefix string, emit func(key, value string)) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '}' {
				return nil
			}
			return errors.New("unexpected " + string(d) + " in object")
		}
		rawKey, _ := tok.(string)
		key := sanitizeKey(rawKey)
		if prefix != "" && key != "" {
			key = prefix + "_" + key
		} else if key == "" {
			key = prefix
		}
		val, err := dec.Token()
		if err != nil {
			return err
		}
		switch v := val.(type) {
		case json.Delim:
			switch v {
			case '{':
				if err := walkObject(dec, key, emit); err != nil {
					return err
				}
			case '[':
				if err := skipValue(dec); err != nil {
					return err
				}
			default:
				return errors.New("unexpected " + string(v) + " in object")
			}
		case string:
			emit(key, v)
		case json.Number:
			emit(key, v.String())
		case bool:
			if v {
				emit(key, "true")
			} else {
				emit(key, "false")
			}
		case nil:
			// null → skipped
		}
	}
}

// skipValue consumes the rest of an array whose `[` was consumed.
func skipValue(dec *json.Decoder) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '[', '{':
				depth++
			case ']', '}':
				depth--
			}
		}
	}
	return nil
}
