package model

import "bytes"

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// enumSchema builds a string enum schema.
func enumSchema(values ...string) func() *schemaT {
	return func() *schemaT { return stringEnum(values...) }
}
