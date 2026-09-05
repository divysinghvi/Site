package content

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"divy.dev/internal/model"
)

// Level is the severity of a finding.
type Level string

// Finding levels.
const (
	LevelError Level = "error"
	LevelWarn  Level = "warn"
)

// Finding is one validation result.
type Finding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Level   Level  `json:"-"`
	Rule    string `json:"rule"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Report collects the findings and the TODO inventory of one Load.
type Report struct {
	Files    int
	Errors   []Finding
	Warnings []Finding
	Todos    []model.TodoItem
}

func (r *Report) add(level Level, file string, line, col int, rule, path, msg string) {
	f := Finding{File: file, Line: line, Col: col, Level: level, Rule: rule, Path: path, Message: msg}
	if level == LevelError {
		r.Errors = append(r.Errors, f)
	} else {
		r.Warnings = append(r.Warnings, f)
	}
}

func (r *Report) errorf(file string, line, col int, rule, path, format string, args ...any) {
	r.add(LevelError, file, line, col, rule, path, fmt.Sprintf(format, args...))
}

func (r *Report) warnf(file string, line, col int, rule, path, format string, args ...any) {
	r.add(LevelWarn, file, line, col, rule, path, fmt.Sprintf(format, args...))
}

// HasErrors reports whether the report fails; with strict, warnings fail too.
func (r *Report) HasErrors(strict bool) bool {
	return len(r.Errors) > 0 || (strict && len(r.Warnings) > 0)
}

// Sort orders findings by file order, then line, then column.
func (r *Report) Sort() {
	less := func(fs []Finding) func(i, j int) bool {
		return func(i, j int) bool {
			if fs[i].File != fs[j].File {
				return fileOrder(fs[i].File) < fileOrder(fs[j].File)
			}
			if fs[i].Line != fs[j].Line {
				return fs[i].Line < fs[j].Line
			}
			return fs[i].Col < fs[j].Col
		}
	}
	sort.SliceStable(r.Errors, less(r.Errors))
	sort.SliceStable(r.Warnings, less(r.Warnings))
}

func fileOrder(file string) string {
	base := file
	if i := strings.LastIndex(file, "/"); i >= 0 {
		base = file[i+1:]
	}
	for i, name := range fileNames {
		if strings.HasPrefix(base, name) || (name == "postmortems" && strings.Contains(file, "postmortems/")) {
			return fmt.Sprintf("%02d %s", i, file)
		}
	}
	return "99 " + file
}

// Write prints the human format: one finding per line, then a summary.
func (r *Report) Write(w io.Writer, strict bool) {
	r.Sort()
	all := append([]Finding{}, r.Errors...)
	all = append(all, r.Warnings...)
	for _, f := range all {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			if f.Col > 0 {
				loc = fmt.Sprintf("%s:%d", loc, f.Col)
			}
		}
		fmt.Fprintf(w, "%-40s %-5s %-32s %s\n", loc, f.Level, f.Rule, f.Message)
	}
	verdict := "OK"
	if r.HasErrors(strict) {
		verdict = "FAIL"
	}
	fmt.Fprintf(w, "validate: %d files, %d errors, %d warnings, %d TODO(divy) — %s\n",
		r.Files, len(r.Errors), len(r.Warnings), len(r.Todos), verdict)
}

// WriteTodos prints the TODO inventory, one per line.
func (r *Report) WriteTodos(w io.Writer) {
	for _, t := range r.Todos {
		fmt.Fprintf(w, "%s:%d:%d  %s  %s  %s\n", t.File, t.Line, t.Col, t.Path, t.Context, t.Text)
	}
	fmt.Fprintf(w, "%d TODO(divy)\n", len(r.Todos))
}

// JSON renders the --json shape.
func (r *Report) JSON(strict bool) []byte {
	r.Sort()
	type todos struct {
		Count int              `json:"count"`
		Items []model.TodoItem `json:"items"`
	}
	out := struct {
		OK       bool      `json:"ok"`
		Files    int       `json:"files"`
		Errors   []Finding `json:"errors"`
		Warnings []Finding `json:"warnings"`
		Todos    todos     `json:"todos"`
	}{OK: !r.HasErrors(strict), Files: r.Files, Errors: nonNil(r.Errors), Warnings: nonNil(r.Warnings), Todos: todos{Count: len(r.Todos), Items: nonNilTodos(r.Todos)}}
	b, _ := json.MarshalIndent(out, "", " ")
	return append(b, '\n')
}

func nonNil(f []Finding) []Finding {
	if f == nil {
		return []Finding{}
	}
	return f
}

func nonNilTodos(t []model.TodoItem) []model.TodoItem {
	if t == nil {
		return []model.TodoItem{}
	}
	return t
}
