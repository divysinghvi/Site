package server

import (
	"os"
	"testing"

	"divy.dev/internal/content"
	"divy.dev/internal/promql"
)

// TestContentExpressionsParse runs every panel target and alert expression of
// the real content/ tree (and of the test fixture) through the PromQL parser.
func TestContentExpressionsParse(t *testing.T) {
	for _, dir := range []string{"../content/testdata/valid", "../../content"} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		c, err := content.Load(dir, content.Options{Now: frozen, SiteOrigin: "https://example.vercel.app"})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, p := range c.Panels.Panels {
			for _, tg := range p.Targets {
				n++
				if _, err := promql.ParseExpr(tg.Expr); err != nil {
					t.Errorf("%s panel %s target %s: %v", dir, p.ID, tg.RefID, err)
				}
			}
		}
		for _, g := range c.Alerts.Groups {
			for _, r := range g.Rules {
				n++
				if _, err := promql.ParseExpr(r.Expr); err != nil {
					t.Errorf("%s alert %s: %v", dir, r.Alert, err)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no expressions found", dir)
		}
		// the validator itself must have accepted them (rule panels.expr)
		for _, f := range c.Report.Errors {
			if f.Rule == "panels.expr" {
				t.Errorf("%s: validator rejected an expression: %s", dir, f.Message)
			}
		}
	}
}
