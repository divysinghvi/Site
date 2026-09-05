// Package og renders the Open Graph images: one per postmortem
// (/og/postmortems/{id}.png) and the site default (/og/default.png),
// 1200×630 PNGs drawn with fogleman/gg and the Go fonts embedded in
// golang.org/x/image/font/gofont — no font files, no fontconfig, so the
// renderer works in a distroless container and on Vercel alike.
package og

import (
	"bytes"
	"fmt"
	"image/color"
	"strings"
	"sync"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// Image geometry (LogQL §L.6.7).
const (
	Width  = 1200
	Height = 630
	margin = 60
	inner  = Width - 2*margin
)

// Palette (brief §5, Grafana 11-inspired).
const (
	colorBG     = "#0b0c0e"
	colorPanel  = "#181b1f"
	colorMuted  = "#8e8e8e"
	colorText   = "#ffffff"
	colorBody   = "#c7d0d9"
	colorGreen  = "#73bf69"
	colorSEV1   = "#f2495c"
	colorSEV2   = "#ff9830"
	colorSEV3   = "#f2cc0c"
	colorSEV4   = "#5794f2"
	colorFallbk = "#8e8e8e"
)

// SeverityColor maps SEV1..SEV4 to the badge colour (unknown → grey).
func SeverityColor(sev string) string {
	switch strings.ToUpper(strings.TrimSpace(sev)) {
	case "SEV1":
		return colorSEV1
	case "SEV2":
		return colorSEV2
	case "SEV3":
		return colorSEV3
	case "SEV4":
		return colorSEV4
	}
	return colorFallbk
}

// Service is one service dot in the footer.
type Service struct {
	Name  string
	Color string
}

// Postmortem is the input of RenderPostmortem (values verbatim from the
// frontmatter: dates and durations may be TODO(divy) and are drawn as such).
type Postmortem struct {
	ID       string
	Title    string
	Severity string
	Date     string
	Duration string
	Summary  string
	Services []Service
	// Host is the site host shown top-left ("<host> / postmortems").
	Host string
}

// Default is the input of RenderDefault.
type Default struct {
	Name    string
	Tagline string
	Host    string
	// Colors is the service palette drawn as a 10-segment strip (cycled when shorter).
	Colors []string
}

var (
	fontOnce sync.Once
	fontErr  error
	bold     *truetype.Font
	regular  *truetype.Font
	mono     *truetype.Font
	faceMu   sync.Mutex
	faces    = map[string]font.Face{}
)

func loadFonts() error {
	fontOnce.Do(func() {
		if bold, fontErr = truetype.Parse(gobold.TTF); fontErr != nil {
			return
		}
		if regular, fontErr = truetype.Parse(goregular.TTF); fontErr != nil {
			return
		}
		mono, fontErr = truetype.Parse(gomono.TTF)
	})
	return fontErr
}

// face returns a cached font.Face; sizes are pixels (72 DPI).
func face(f *truetype.Font, kind string, px float64) font.Face {
	key := fmt.Sprintf("%s:%g", kind, px)
	faceMu.Lock()
	defer faceMu.Unlock()
	if ff, ok := faces[key]; ok {
		return ff
	}
	ff := truetype.NewFace(f, &truetype.Options{Size: px, DPI: 72, Hinting: font.HintingFull})
	faces[key] = ff
	return ff
}

func setFont(dc *gg.Context, f *truetype.Font, kind string, px float64) {
	dc.SetFontFace(face(f, kind, px))
}

// wrapLines word-wraps s to width and keeps at most max lines, ending the
// last kept line with an ellipsis when text was cut.
func wrapLines(dc *gg.Context, s string, width float64, max int) []string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return nil
	}
	lines := dc.WordWrap(s, width)
	if len(lines) <= max {
		return lines
	}
	lines = lines[:max]
	last := lines[max-1]
	for len(last) > 0 {
		w, _ := dc.MeasureString(last + "…")
		if w <= width {
			break
		}
		if i := strings.LastIndex(last, " "); i > 0 {
			last = last[:i]
		} else {
			last = last[:len(last)-1]
		}
	}
	lines[max-1] = last + "…"
	return lines
}

// RenderPostmortem draws the postmortem card and returns the PNG bytes.
func RenderPostmortem(pm Postmortem) ([]byte, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	dc := gg.NewContext(Width, Height)
	dc.SetHexColor(colorBG)
	dc.Clear()
	sev := SeverityColor(pm.Severity)

	// header: "<host> / postmortems" and the severity badge
	setFont(dc, mono, "mono", 28)
	dc.SetHexColor(colorMuted)
	host := pm.Host
	if host == "" {
		host = "divy.dev"
	}
	dc.DrawStringAnchored(host+" / postmortems", margin, 78, 0, 0.5)
	if pm.Severity != "" {
		setFont(dc, mono, "mono", 40)
		bw, bh := dc.MeasureString(pm.Severity)
		padX, padY := 22.0, 10.0
		x := float64(Width-margin) - bw - 2*padX
		y := 78 - bh/2 - padY
		dc.SetHexColor(sev)
		dc.DrawRoundedRectangle(x, y, bw+2*padX, bh+2*padY, 10)
		dc.Fill()
		dc.SetHexColor(colorBG)
		dc.DrawStringAnchored(pm.Severity, x+padX+bw/2, 78, 0.5, 0.5)
	}

	// title
	setFont(dc, bold, "bold", 56)
	dc.SetHexColor(colorText)
	y := 150.0
	for _, line := range wrapLines(dc, pm.Title, inner, 2) {
		dc.DrawStringAnchored(line, margin, y+28, 0, 0.5)
		y += 68
	}
	y += 18

	// summary
	setFont(dc, regular, "regular", 30)
	dc.SetHexColor(colorBody)
	for _, line := range wrapLines(dc, pm.Summary, inner, 3) {
		dc.DrawStringAnchored(line, margin, y+15, 0, 0.5)
		y += 42
	}

	// footer: id · date · duration · services
	setFont(dc, mono, "mono", 26)
	fy := 552.0
	parts := []string{pm.ID}
	if pm.Date != "" {
		parts = append(parts, pm.Date)
	}
	if pm.Duration != "" {
		parts = append(parts, pm.Duration)
	}
	footer := strings.Join(parts, " · ")
	dc.SetHexColor(colorMuted)
	dc.DrawStringAnchored(footer, margin, fy, 0, 0.5)
	fw, _ := dc.MeasureString(footer)
	x := margin + fw
	for _, svc := range pm.Services {
		x += 22
		c := svc.Color
		if c == "" {
			c = colorFallbk
		}
		dc.SetHexColor(c)
		dc.DrawCircle(x+7, fy, 7)
		dc.Fill()
		x += 22
		dc.SetHexColor(colorBody)
		dc.DrawStringAnchored(svc.Name, x, fy, 0, 0.5)
		w, _ := dc.MeasureString(svc.Name)
		x += w
		if x > Width-margin-80 {
			break
		}
	}

	// severity bar
	dc.SetHexColor(sev)
	dc.DrawRectangle(0, Height-6, Width, 6)
	dc.Fill()
	return encode(dc)
}

// RenderDefault draws the site card: name, tagline (a TODO(divy) tagline is
// drawn literally — nothing is invented), host and a strip of the service colours.
func RenderDefault(d Default) ([]byte, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	dc := gg.NewContext(Width, Height)
	dc.SetHexColor(colorBG)
	dc.Clear()

	// panel-coloured band behind the name
	dc.SetHexColor(colorPanel)
	dc.DrawRoundedRectangle(margin, 120, inner, 330, 16)
	dc.Fill()

	name := d.Name
	if name == "" {
		name = "divy.dev"
	}
	setFont(dc, bold, "bold", 72)
	dc.SetHexColor(colorText)
	dc.DrawStringAnchored(name, margin+48, 205, 0, 0.5)

	setFont(dc, regular, "regular", 36)
	dc.SetHexColor(colorBody)
	y := 275.0
	for _, line := range wrapLines(dc, d.Tagline, inner-96, 2) {
		dc.DrawStringAnchored(line, margin+48, y, 0, 0.5)
		y += 48
	}

	setFont(dc, mono, "mono", 28)
	dc.SetHexColor(colorGreen)
	host := d.Host
	if host == "" {
		host = "divy.dev"
	}
	dc.DrawStringAnchored(host, margin+48, 392, 0, 0.5)
	dc.SetHexColor(colorMuted)
	dc.DrawStringAnchored("  · metrics, logs, traces, uptime, alerts, postmortems", margin+48+measure(dc, host), 392, 0, 0.5)

	// 10-segment strip of the service colours
	colors := d.Colors
	if len(colors) == 0 {
		colors = []string{colorGreen, colorSEV4, colorSEV2, colorSEV3, colorSEV1}
	}
	segW := float64(inner) / 10
	for i := 0; i < 10; i++ {
		dc.SetHexColor(colors[i%len(colors)])
		dc.DrawRectangle(margin+float64(i)*segW+2, 500, segW-4, 14)
		dc.Fill()
	}
	dc.SetHexColor(colorMuted)
	setFont(dc, mono, "mono", 22)
	dc.DrawStringAnchored("curl -H 'Accept: text/plain' https://"+host+"/", margin, 560, 0, 0.5)
	return encode(dc)
}

func measure(dc *gg.Context, s string) float64 {
	w, _ := dc.MeasureString(s)
	return w
}

func encode(dc *gg.Context) ([]byte, error) {
	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// hexColor is exported for tests that assert the palette.
func hexColor(s string) color.Color {
	var r, g, b uint8
	_, _ = fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &r, &g, &b)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}
