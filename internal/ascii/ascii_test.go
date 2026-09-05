package ascii

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"divy.dev/internal/content"
)

var frozen = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

func load(t *testing.T) *content.Content {
	t.Helper()
	c, err := content.Load("../content/testdata/valid", content.Options{Now: frozen})
	if err != nil {
		t.Fatal(err)
	}
	if c.Report.HasErrors(false) {
		var sb strings.Builder
		c.Report.Write(&sb, false)
		t.Fatal(sb.String())
	}
	return c
}

func TestRenderGolden(t *testing.T) {
	c := load(t)
	got := Render(c, Options{Width: 80, Now: frozen, Origin: "https://example.vercel.app"})
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile("testdata/ascii.golden", []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/ascii.golden")
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1)", err)
	}
	if got != string(want) {
		t.Errorf("render differs from golden:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderRules(t *testing.T) {
	c := load(t)
	out := Render(c, Options{Width: 80, Now: frozen})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "divy.career · "+content.TraceID+" · 2023-01-01 → now · 28 spans") {
		t.Errorf("bad first line: %q", lines[0])
	}
	for i, ln := range lines {
		if n := utf8.RuneCountInString(ln); n > 80 {
			t.Errorf("line %d is %d runes: %q", i, n, ln)
		}
	}
	var root string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "divy       divy.career") {
			root = ln
		}
	}
	if root == "" || !strings.HasSuffix(root, "┄┄┄┄") || !strings.Contains(root, "▓▓▓▓") {
		t.Errorf("root row wrong: %q", root)
	}
	found := false
	for _, ln := range lines {
		if strings.Contains(ln, "gradr.inc-002 [ERR]") {
			found = true
			if !strings.Contains(ln, "░") || strings.Contains(ln, "▓") {
				t.Errorf("inc-002 row should be hatched: %q", ln)
			}
		}
	}
	if !found {
		t.Error("gradr.inc-002 row missing")
	}
	if ClampWidth(10) != 60 || ClampWidth(500) != 200 || ClampWidth(0) != 80 || ClampWidth(120) != 120 {
		t.Error("ClampWidth")
	}
	wide := Render(c, Options{Width: 120, Now: frozen})
	for i, ln := range strings.Split(wide, "\n") {
		if n := utf8.RuneCountInString(ln); n > 120 {
			t.Errorf("wide line %d is %d runes", i, n)
		}
	}
}
