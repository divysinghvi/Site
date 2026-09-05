package schemagen

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateAndCompile(t *testing.T) {
	docs, err := Generate(Options{CommentsDir: "../model"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range docs {
		names[d.Name] = true
		var v map[string]any
		if err := json.Unmarshal(d.JSON, &v); err != nil {
			t.Fatalf("%s: invalid json: %v", d.Name, err)
		}
		if v["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s: $schema = %v", d.Name, v["$schema"])
		}
		if !strings.HasSuffix(v["$id"].(string), d.Name+".schema.json") {
			t.Errorf("%s: $id = %v", d.Name, v["$id"])
		}
	}
	for _, want := range []string{"spans", "logs", "postmortem", "panels", "alerts", "uptime", "manual_metrics", "profile", "api", "index"} {
		if !names[want] {
			t.Errorf("missing document %s", want)
		}
	}
	if _, err := Compile(docs); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, err := Generate(Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if string(a[i].JSON) != string(b[i].JSON) {
			t.Errorf("%s: output differs between runs", a[i].Name)
		}
	}
}
