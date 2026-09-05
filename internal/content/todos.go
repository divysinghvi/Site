package content

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"divy.dev/internal/model"
)

// TodoMarker is the literal every unknown fact carries.
const TodoMarker = "TODO(divy)"

var contextKeys = []string{"id", "alert", "metric", "name"}

// collectYAMLTodos inventories TODO(divy) markers in values and comments of a YAML document.
func (l *loader) collectYAMLTodos(doc *yamlDoc) {
	var walk func(n *yaml.Node, path string, ctx string)
	comment := func(n *yaml.Node, path, ctx string) {
		for _, cm := range []string{n.HeadComment, n.LineComment, n.FootComment} {
			if !strings.Contains(cm, TodoMarker) {
				continue
			}
			for _, ln := range strings.Split(cm, "\n") {
				if !strings.Contains(ln, TodoMarker) {
					continue
				}
				txt := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "#"))
				l.todos = append(l.todos, model.TodoItem{File: doc.file, Line: n.Line, Col: n.Column, Path: path, Context: ctx + " (comment)", Text: txt})
			}
		}
	}
	walk = func(n *yaml.Node, path string, ctx string) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.DocumentNode:
			comment(n, path, ctx)
			for _, ch := range n.Content {
				walk(ch, path, ctx)
			}
		case yaml.MappingNode:
			comment(n, path, ctx)
			for _, k := range contextKeys {
				if v := mapValue(n, k); v != nil && v.Kind == yaml.ScalarNode {
					ctx = v.Value
					break
				}
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				p := path + "." + k.Value
				comment(k, p, ctx)
				walk(v, p, ctx)
			}
		case yaml.SequenceNode:
			comment(n, path, ctx)
			for i, ch := range n.Content {
				walk(ch, path+"["+strconv.Itoa(i)+"]", ctx)
			}
		case yaml.ScalarNode:
			comment(n, path, ctx)
			if strings.Contains(n.Value, TodoMarker) {
				l.todos = append(l.todos, model.TodoItem{File: doc.file, Line: n.Line, Col: n.Column, Path: path, Context: ctx, Text: strings.TrimSpace(n.Value)})
			}
		case yaml.AliasNode:
			comment(n, path, ctx)
		}
	}
	walk(doc.root, "$", "")
}

// collectLogTodos inventories TODO(divy) values of one NDJSON line.
func (l *loader) collectLogTodos(file string, fileLn int, raw []byte, line model.LogLine) {
	if !strings.Contains(string(raw), TodoMarker) {
		return
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, ok := obj[k].(string)
		if !ok || !strings.Contains(s, TodoMarker) {
			continue
		}
		enc, _ := json.Marshal(s)
		col := strings.Index(string(raw), string(enc))
		if col < 0 {
			col = 0
		}
		l.todos = append(l.todos, model.TodoItem{File: file, Line: fileLn, Col: col + 2, Path: "$." + k, Context: line.Msg, Text: s})
	}
}

// collectMarkdownTodos inventories TODO(divy) markers in a postmortem body.
func (l *loader) collectMarkdownTodos(file, id, body string, firstLine int) {
	heading := ""
	for i, ln := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "#") {
			heading = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
		rest := ln
		offset := 0
		for {
			idx := strings.Index(rest, TodoMarker)
			if idx < 0 {
				break
			}
			txt := rest[idx:]
			if cut := strings.Index(txt, " |"); cut > 0 {
				txt = txt[:cut]
			}
			txt = strings.TrimSpace(txt)
			l.todos = append(l.todos, model.TodoItem{File: file, Line: firstLine + i, Col: offset + idx + 1, Path: "", Context: id + " › " + heading, Text: txt})
			rest = rest[idx+len(TodoMarker):]
			offset += idx + len(TodoMarker)
		}
	}
}

// TodosView renders the inventory in the /api/content/todos shape.
func (c *Content) TodosView(generatedAt string) model.ContentTodos {
	byFile := map[string]int{}
	for _, t := range c.Todos {
		byFile[t.File]++
	}
	items := c.Todos
	if items == nil {
		items = []model.TodoItem{}
	}
	return model.ContentTodos{GeneratedAt: generatedAt, Count: len(c.Todos), ByFile: byFile, Items: items}
}
