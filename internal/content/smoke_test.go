package content

import (
	"os"
	"testing"
	"time"
)

func TestSmoke(t *testing.T) {
	c, err := Load("testdata/valid", Options{Now: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	c.Report.Write(os.Stdout, false)
	t.Logf("nodes=%d logs=%d pms=%d todos=%d hash=%s", len(c.nodes), len(c.Logs), len(c.Postmortems), len(c.Todos), c.Hash[:8])
	for _, td := range c.Todos[:5] {
		t.Logf("%+v", td)
	}
	pm := c.Postmortems[0]
	t.Logf("sections=%v toc=%v", pm.Sections, pm.TOC)
	t.Logf("html=%s", pm.HTML[:300])
}
