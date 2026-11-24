package svg

import (
	"image"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// parseInline parses markup the way an HTML page carries it and returns the
// drawing of its first <svg>.
func parseInline(t testing.TB, markup string) *Image {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<body>" + markup))
	if err != nil {
		t.Fatal(err)
	}
	var svgNode *html.Node
	var find func(n *html.Node)
	find = func(n *html.Node) {
		if svgNode == nil && n.Type == html.ElementNode && n.Data == "svg" {
			svgNode = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if svgNode == nil {
		t.Fatalf("no svg in %s", markup)
	}
	root, err := FromHTML(svgNode, nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(root)
}

// px returns the colour at (x, y) as RRGGBBAA, un-premultiplied.
func px(img *image.RGBA, x, y int) (r, g, b, a uint8) {
	c := img.RGBAAt(x, y)
	if c.A == 0 {
		return 0, 0, 0, 0
	}
	un := func(v uint8) uint8 { return uint8(min(255, int(v)*255/int(c.A))) }
	return un(c.R), un(c.G), un(c.B), c.A
}

func TestRenderShapes(t *testing.T) {
	type probe struct {
		x, y       int
		r, g, b, a uint8
	}
	tests := []struct {
		name   string
		svg    string
		w, h   int
		color  string
		probes []probe
	}{
		{
			name: "filled rectangle",
			svg:  `<svg width="10" height="10"><rect width="10" height="10" fill="red"/></svg>`,
			w:    10, h: 10,
			probes: []probe{{5, 5, 255, 0, 0, 255}, {0, 0, 255, 0, 0, 255}},
		},
		{
			name: "viewBox scales the drawing",
			svg:  `<svg viewBox="0 0 10 10"><rect x="5" width="5" height="10" fill="#00f"/></svg>`,
			w:    20, h: 20,
			probes: []probe{{15, 10, 0, 0, 255, 255}, {5, 10, 0, 0, 0, 0}},
		},
		{
			name: "aspect ratio is kept and centred",
			svg:  `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="#0f0"/></svg>`,
			w:    20, h: 10,
			probes: []probe{{10, 5, 0, 255, 0, 255}, {2, 5, 0, 0, 0, 0}, {18, 5, 0, 0, 0, 0}},
		},
		{
			name: "stroked polyline without fill",
			svg: `<svg viewBox="0 0 20 20" fill="none"><polyline points="2,18 10,2 18,18" stroke="#000" stroke-width="2" ` +
				`stroke-linecap="round" stroke-linejoin="round"/></svg>`,
			w: 20, h: 20,
			probes: []probe{{10, 3, 0, 0, 0, 255}, {5, 10, 0, 0, 0, 255}, {10, 14, 0, 0, 0, 0}},
		},
		{
			// Overlapping parts of one stroke are painted once.
			name: "translucent stroke is even across a joint",
			svg:  `<svg viewBox="0 0 20 20"><path d="M2 10 L18 10 L2 11" fill="none" stroke="#000" stroke-opacity="0.5" stroke-width="4"/></svg>`,
			w:    20, h: 20,
			probes: []probe{{10, 10, 0, 0, 0, 128}, {17, 10, 0, 0, 0, 128}},
		},
		{
			name: "currentColor",
			svg:  `<svg viewBox="0 0 10 10"><path fill="currentColor" d="M0 0h10v10H0z"/></svg>`,
			w:    10, h: 10, color: "00FF00",
			probes: []probe{{5, 5, 0, 255, 0, 255}},
		},
		{
			name: "color attribute sets currentColor",
			svg:  `<svg viewBox="0 0 10 10" color="#00f"><rect width="10" height="10" fill="currentColor"/></svg>`,
			w:    10, h: 10, color: "00FF00",
			probes: []probe{{5, 5, 0, 0, 255, 255}},
		},
		{
			name: "gradient becomes the average of its stops",
			svg: `<svg viewBox="0 0 10 10"><defs><linearGradient id="g"><stop offset="0" stop-color="#000"/><stop offset="1" stop-color="#fff"/></linearGradient>` +
				`<linearGradient id="h" href="#g"/></defs><rect width="10" height="10" fill="url(#h)"/></svg>`,
			w: 10, h: 10,
			probes: []probe{{5, 5, 128, 128, 128, 255}},
		},
		{
			name: "missing gradient uses the fallback",
			svg:  `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="url(#nope) #f00"/></svg>`,
			w:    10, h: 10,
			probes: []probe{{5, 5, 255, 0, 0, 255}},
		},
		{
			// Contours wound in opposite directions leave a hole.
			name: "nonzero fill rule",
			svg:  `<svg viewBox="0 0 10 10"><path d="M0 0H10V10H0Z M3 3V7H7V3Z"/></svg>`,
			w:    10, h: 10,
			probes: []probe{{1, 1, 0, 0, 0, 255}, {5, 5, 0, 0, 0, 0}},
		},
		{
			name: "circle, ellipse and transforms",
			svg:  `<svg viewBox="0 0 20 20"><g transform="translate(10 10)"><circle r="3" fill="#f00"/></g><ellipse cx="4" cy="16" rx="3" ry="2" fill="#00f"/></svg>`,
			w:    20, h: 20,
			probes: []probe{{10, 10, 255, 0, 0, 255}, {4, 16, 0, 0, 255, 255}, {13, 10, 0, 0, 0, 0}},
		},
		{
			name: "rounded rectangle corners are cut",
			svg:  `<svg viewBox="0 0 10 10"><rect width="10" height="10" rx="4" fill="#000"/></svg>`,
			w:    10, h: 10,
			probes: []probe{{0, 0, 0, 0, 0, 0}, {5, 5, 0, 0, 0, 255}},
		},
		{
			name: "hidden elements and defs are not drawn",
			svg: `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="#f00" display="none"/><g style="visibility:hidden"><rect width="10" height="10"/></g>` +
				`<defs><rect width="10" height="10"/></defs></svg>`,
			w: 10, h: 10,
			probes: []probe{{5, 5, 0, 0, 0, 0}},
		},
		{
			name: "use of a symbol",
			svg:  `<svg viewBox="0 0 20 10"><symbol id="s" viewBox="0 0 1 1"><rect width="1" height="1" fill="#f00"/></symbol><use href="#s" x="10" width="10" height="10"/></svg>`,
			w:    20, h: 10,
			probes: []probe{{15, 5, 255, 0, 0, 255}, {5, 5, 0, 0, 0, 0}},
		},
		{
			name: "style attribute beats presentation attribute",
			svg:  `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="#f00" style="fill:#00f"/></svg>`,
			w:    10, h: 10,
			probes: []probe{{5, 5, 0, 0, 255, 255}},
		},
		{
			name: "group opacity",
			svg:  `<svg viewBox="0 0 10 10"><g opacity="0.5"><rect width="10" height="10" fill="#000"/></g></svg>`,
			w:    10, h: 10,
			probes: []probe{{5, 5, 0, 0, 0, 128}},
		},
	}
	for _, tt := range tests {
		img := parseInline(t, tt.svg).Render(tt.w, tt.h, tt.color)
		for _, p := range tt.probes {
			r, g, b, a := px(img, p.x, p.y)
			near := func(x, y uint8) bool { return int(x)-int(y) <= 3 && int(y)-int(x) <= 3 }
			if !near(r, p.r) || !near(g, p.g) || !near(b, p.b) || !near(a, p.a) {
				t.Errorf("%s: pixel (%d,%d) = %d,%d,%d,%d, want %d,%d,%d,%d",
					tt.name, p.x, p.y, r, g, b, a, p.r, p.g, p.b, p.a)
			}
		}
	}
}

func TestSizeAndTitle(t *testing.T) {
	tests := []struct {
		svg   string
		w, h  float64
		title string
	}{
		{`<svg width="24" height="12"></svg>`, 24, 12, ""},
		{`<svg width="24px" height="1in"></svg>`, 24, 96, ""},
		{`<svg viewBox="0 0 68 24"></svg>`, 68, 24, ""},
		{`<svg width="34" viewBox="0 0 68 24"></svg>`, 34, 12, ""},
		{`<svg width="100%" viewBox="0 0 10 5"></svg>`, 10, 5, ""},
		{`<svg></svg>`, 0, 0, ""},
		{`<svg aria-label=" Revenue chart "><title>
			Quarterly  revenue </title></svg>`, 0, 0, "Quarterly revenue"},
		{`<svg aria-label=" Revenue chart "></svg>`, 0, 0, "Revenue chart"},
	}
	for _, tt := range tests {
		im := parseInline(t, tt.svg)
		w, h := im.Size()
		if w != tt.w || h != tt.h || im.Title() != tt.title {
			t.Errorf("%s: size %vx%v title %q, want %vx%v %q", tt.svg, w, h, im.Title(), tt.w, tt.h, tt.title)
		}
	}
}

func TestParseXML(t *testing.T) {
	data := `<?xml version="1.0"?>
<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:inkscape="http://www.inkscape.org/namespaces/inkscape" width="10" height="10">
  <inkscape:metadata><rect width="10" height="10" fill="#0f0"/></inkscape:metadata>
  <defs><rect id="r" width="10" height="10" style="fill:#f00"/></defs>
  <use xlink:href="#r"/>
  <text>&amp; &nbsp;</text>
</svg>`
	root, err := ParseXML([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	im := New(root)
	img := im.Render(10, 10, "")
	if r, g, _, a := px(img, 5, 5); r != 255 || g != 0 || a != 255 {
		t.Errorf("pixel = %d,%d,a%d; want red through <use>, and the foreign element skipped", r, g, a)
	}
	if !Looks([]byte(data)) || Looks([]byte("\x89PNG\r\n")) {
		t.Error("Looks misjudged the data")
	}
	if _, err := ParseXML([]byte("<html><body>no drawing</body></html>")); err == nil {
		t.Error("a file without <svg> should be an error")
	}
}

// Hostile drawings finish quickly.
func TestRenderLimits(t *testing.T) {
	cases := map[string]string{
		"rects": `<svg viewBox="0 0 1000 1000">` + strings.Repeat(`<rect width="1000" height="1000"/>`, 15000) + `</svg>`,
		"usebomb": `<svg viewBox="0 0 10 10"><g id="a0"><rect width="10" height="10"/></g>` + func() string {
			var b strings.Builder
			for i := 1; i < 9; i++ {
				b.WriteString(`<g id="a` + string(rune('0'+i)) + `">`)
				for j := 0; j < 10; j++ {
					b.WriteString(`<use href="#a` + string(rune('0'+i-1)) + `"/>`)
				}
				b.WriteString(`</g>`)
			}
			return b.String()
		}() + `</svg>`,
		"selfuse": `<svg viewBox="0 0 10 10"><g id="x"><use href="#x"/><rect width="1" height="1"/></g></svg>`,
		"curves":  `<svg viewBox="0 0 10 10"><path d="M0 0` + strings.Repeat(" C1 9 9 1 10 10", 100000) + `" stroke="#000"/></svg>`,
		"huge":    `<svg viewBox="0 0 10 10"><rect x="-1e300" y="-1e300" width="1e308" height="1e308"/><circle r="1e300"/></svg>`,
	}
	for name, svg := range cases {
		start := time.Now()
		im := parseInline(t, svg)
		im.Render(MaxRaster, MaxRaster, "")
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s took %v", name, d)
		}
	}
}

func TestEmpty(t *testing.T) {
	tests := map[string]bool{
		`<svg></svg>`: true,
		`<svg width="0" height="0"><symbol id="i"><path d="M0 0h1"/></symbol></svg>`: true,
		`<svg><defs><path id="p" d="M0 0h1"/></defs><title>t</title></svg>`:          true,
		`<svg><g><circle r="1"/></g></svg>`:                                          false,
		`<svg><use href="#i"/></svg>`:                                                false,
		`<svg width="24"><text>Hi</text></svg>`:                                      false,
	}
	for src, want := range tests {
		if got := parseInline(t, src).Empty(); got != want {
			t.Errorf("Empty(%s) = %v, want %v", src, got, want)
		}
	}
}

// FuzzSVG checks that no path data or file makes parsing, rendering or
// encoding panic, and that the encoded copy is well formed.
func FuzzSVG(f *testing.F) {
	f.Add(`<svg viewBox="0 0 10 10"><path d="M0 0a5 5 0 0110 0z" stroke="#000"/></svg>`)
	f.Add(`<svg><use href="#a"/><g id="a"><use href="#a"/></g><rect rx="-1" width="1e9" height="nan"/></svg>`)
	f.Add(`<svg width="10" height="10"><polyline points="1 2 3"/><text>&amp;</text></svg>`)
	f.Fuzz(func(t *testing.T, s string) {
		if root, err := ParseXML([]byte(s)); err == nil {
			im := New(root)
			im.Render(16, 16, "")
			wellFormedXML(t, string(im.Encode(16, 16, "000000")))
		}
		parsePath(s, 1)
		parseTransform(s)
	})
}
