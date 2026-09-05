// Package schemagen reflects the model structs into JSON Schema (draft 2020-12)
// documents. The same documents are written to schema/ by `divy schemagen`
// and compiled in memory by the content validator, so what the TypeScript
// types are generated from is exactly what the binary enforces.
package schemagen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	invopop "github.com/invopop/jsonschema"
	stj "github.com/santhosh-tekuri/jsonschema/v6"

	"divy.dev/internal/model"
)

// IDBase is the prefix of every schema $id.
const IDBase = "https://divy.dev/schema/"

// IndexName is the name of the union schema that TypeScript is generated from.
const IndexName = "index"

// Options tune generation.
type Options struct {
	// CommentsDir is the path of internal/model relative to the working
	// directory; Go doc comments become "description" fields. Empty or
	// missing: no descriptions.
	CommentsDir string
}

// Document is one generated schema file.
type Document struct {
	Name   string // file stem, e.g. "spans"
	Schema *invopop.Schema
	JSON   []byte // pretty-printed, newline-terminated
}

// FileName returns the schema file name for a root name.
func FileName(name string) string { return name + ".schema.json" }

// Generate reflects every root in model.SchemaRoots plus the index document.
func Generate(opts Options) ([]Document, error) {
	r := &invopop.Reflector{Anonymous: true, ExpandedStruct: false}
	if opts.CommentsDir != "" {
		if st, err := os.Stat(opts.CommentsDir); err == nil && st.IsDir() {
			if err := r.AddGoComments("divy.dev", filepath.ToSlash(filepath.Clean(opts.CommentsDir))); err != nil {
				return nil, fmt.Errorf("schemagen: go comments: %w", err)
			}
			// AddGoComments keys on base + dir; normalise to the import path.
			fixCommentKeys(r)
		}
	}
	var docs []Document
	index := &invopop.Schema{Version: invopop.Version, ID: invopop.ID(IDBase + FileName(IndexName)), Definitions: invopop.Definitions{}}
	for _, root := range model.SchemaRoots {
		s := r.Reflect(root.Type)
		s.Version = invopop.Version
		s.ID = invopop.ID(IDBase + FileName(root.Name))
		if root.Name == "api" && s.Ref != "" {
			// The api root is a container; expose only its $defs.
			if def, ok := s.Definitions[strings.TrimPrefix(s.Ref, "#/$defs/")]; ok {
				_ = def
			}
		}
		for name, def := range s.Definitions {
			if prev, ok := index.Definitions[name]; ok {
				a, _ := json.Marshal(prev)
				b, _ := json.Marshal(def)
				if !bytes.Equal(a, b) {
					return nil, fmt.Errorf("schemagen: $defs %q differs between roots", name)
				}
				continue
			}
			index.Definitions[name] = def
		}
		b, err := marshal(s)
		if err != nil {
			return nil, err
		}
		docs = append(docs, Document{Name: root.Name, Schema: s, JSON: b})
	}
	b, err := marshal(index)
	if err != nil {
		return nil, err
	}
	docs = append(docs, Document{Name: IndexName, Schema: index, JSON: b})
	return docs, nil
}

func fixCommentKeys(r *invopop.Reflector) {
	want := reflect.TypeOf(model.SpansFile{}).PkgPath() // divy.dev/internal/model
	fixed := map[string]string{}
	for k, v := range r.CommentMap {
		i := strings.LastIndex(k, ".")
		if i < 0 {
			continue
		}
		// keys look like <base>/<dir>.<Type>[.<Field>]
		j := strings.Index(k, ".")
		pkg := k[:j]
		rest := k[j:]
		if pkg != want {
			fixed[want+rest] = v
		} else {
			fixed[k] = v
		}
	}
	r.CommentMap = fixed
}

func marshal(s *invopop.Schema) ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Write writes every document into dir (created if missing) and returns the paths.
func Write(dir string, docs []Document) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var paths []string
	for _, d := range docs {
		p := filepath.Join(dir, FileName(d.Name))
		if err := os.WriteFile(p, d.JSON, 0o644); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// Check compares the documents with the files in dir and returns the names that differ or are missing.
func Check(dir string, docs []Document) ([]string, error) {
	var diff []string
	for _, d := range docs {
		p := filepath.Join(dir, FileName(d.Name))
		on, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				diff = append(diff, d.Name+" (missing)")
				continue
			}
			return nil, err
		}
		if !bytes.Equal(on, d.JSON) {
			diff = append(diff, d.Name)
		}
	}
	sort.Strings(diff)
	return diff, nil
}

// Compiled is a set of compiled validators keyed by root name.
type Compiled map[string]*stj.Schema

// Compile builds validators for every root document (formats asserted).
func Compile(docs []Document) (Compiled, error) {
	c := stj.NewCompiler()
	c.AssertFormat()
	out := Compiled{}
	for _, d := range docs {
		if d.Name == IndexName {
			continue
		}
		doc, err := stj.UnmarshalJSON(bytes.NewReader(d.JSON))
		if err != nil {
			return nil, fmt.Errorf("schemagen: %s: %w", d.Name, err)
		}
		id := IDBase + FileName(d.Name)
		if err := c.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("schemagen: %s: %w", d.Name, err)
		}
		sch, err := c.Compile(id)
		if err != nil {
			return nil, fmt.Errorf("schemagen: compile %s: %w", d.Name, err)
		}
		out[d.Name] = sch
	}
	return out, nil
}

// MustCompile generates and compiles the schemas without descriptions; it
// panics on failure (a programming error in the model structs).
func MustCompile() Compiled {
	docs, err := Generate(Options{})
	if err != nil {
		panic(err)
	}
	c, err := Compile(docs)
	if err != nil {
		panic(err)
	}
	return c
}
