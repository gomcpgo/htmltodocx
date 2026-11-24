package svg

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/vector"

	"github.com/gomcpgo/htmltodocx/internal/css"
)

// Rendering limits.
const (
	maxVisits    = 50000    // elements drawn, counting each <use> again
	maxUseDepth  = 8        // <use> inside <use>
	maxPixelWork = 64 << 20 // pixels covered by all fills and strokes together
	MaxRaster    = 1024     // largest rendered width or height, in pixels
)

// Image is a parsed drawing.
type Image struct {
	root *Node
	ids  map[string]*Node
}

// New prepares a drawing whose root is an <svg> element.
func New(root *Node) *Image {
	im := &Image{root: root, ids: map[string]*Node{}}
	var walk func(n *Node)
	walk = func(n *Node) {
		if id := n.Attr["id"]; id != "" {
			if _, dup := im.ids[id]; !dup {
				im.ids[id] = n
			}
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(root)
	return im
}

// Root returns the <svg> element.
func (im *Image) Root() *Node { return im.root }

// viewBox returns the root's viewBox, if it has a valid one.
func viewBox(n *Node) (vb [4]float64, ok bool) {
	sc := &pathScanner{s: n.attr("viewBox")}
	if !sc.numbers(vb[:]) || vb[2] <= 0 || vb[3] <= 0 {
		return vb, false
	}
	return vb, true
}

// length parses an SVG length in user units (pixels). Percentages are of
// ref.
func length(s string, ref float64) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if strings.HasSuffix(s, "%") {
		v, err := strconv.ParseFloat(strings.TrimSpace(s[:len(s)-1]), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return ref * v / 100, true
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return v, true
	}
	l, ok := css.ParseLength(s)
	if !ok {
		return 0, false
	}
	pt, ok := l.Points(12, 12) // 1em = 16px, the browser default
	if !ok {
		return 0, false
	}
	return pt / 0.75, true
}

// Size returns the drawing's own width and height in CSS pixels, from its
// width and height attributes, or from the viewBox when they are missing.
// Either may be zero when nothing says.
func (im *Image) Size() (w, h float64) {
	vb, hasVB := viewBox(im.root)
	w, okW := length(im.root.attr("width"), 0)
	h, okH := length(im.root.attr("height"), 0)
	if !okW || w <= 0 {
		w = 0
	}
	if !okH || h <= 0 {
		h = 0
	}
	if hasVB {
		switch {
		case w == 0 && h == 0:
			w, h = vb[2], vb[3]
		case w == 0:
			w = h * vb[2] / vb[3]
		case h == 0:
			h = w * vb[3] / vb[2]
		}
	}
	return w, h
}

// Empty reports whether the drawing has nothing to draw: no shapes, text or
// references, or a width or height of zero, as sprite sheets of symbols
// often have.
func (im *Image) Empty() bool {
	for _, k := range []string{"width", "height"} {
		if v, ok := im.root.Attr[k]; ok {
			if l, ok := length(v, 1); ok && l == 0 {
				return true
			}
		}
	}
	var draws func(n *Node) bool
	draws = func(n *Node) bool {
		switch n.Tag {
		case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "use":
			return true
		case "defs", "symbol", "clipPath", "mask", "pattern", "marker", "linearGradient", "radialGradient":
			return false
		}
		for _, k := range n.Kids {
			if draws(k) {
				return true
			}
		}
		return false
	}
	return !draws(im.root)
}

// Title returns the drawing's accessible name: its <title>, or aria-label.
func (im *Image) Title() string {
	for _, k := range im.root.Kids {
		if k.Tag == "title" {
			var b strings.Builder
			for _, t := range k.Kids {
				b.WriteString(t.Text)
			}
			if s := strings.Join(strings.Fields(b.String()), " "); s != "" {
				return s
			}
		}
	}
	return strings.TrimSpace(im.root.attr("aria-label"))
}

// state is the inherited paint state while drawing.
type state struct {
	fill, stroke     string
	fillOp, strokeOp float64
	strokeWidth      float64
	linecap          string
	color            string // RRGGBB, for currentColor
	opacity          float64
	visible          bool
}

type renderer struct {
	im     *Image
	dst    *image.RGBA
	z      vector.Rasterizer
	points int
	visits int
	pixels int
}

// Render draws the drawing into a w×h image, scaled to fit. currentColor is
// the colour that the keyword currentColor stands for (RRGGBB).
func (im *Image) Render(w, h int, currentColor string) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	r := &renderer{im: im, dst: dst, points: maxPoints, pixels: maxPixelWork}
	if currentColor == "" {
		currentColor = "000000"
	}
	st := state{fill: "black", stroke: "none", fillOp: 1, strokeOp: 1, strokeWidth: 1,
		linecap: "butt", color: currentColor, opacity: 1, visible: true}
	// The canvas is the root's viewport. Without a viewBox, user units are
	// pixels of the drawing's own size.
	m := identity
	if _, ok := viewBox(im.root); !ok {
		if iw, ih := im.Size(); iw > 0 && ih > 0 {
			m = matrix{float64(w) / iw, 0, 0, float64(h) / ih, 0, 0}
		}
	}
	r.viewport(im.root, m, float64(w), float64(h), st, 0)
	return dst
}

// viewport draws an <svg> or <symbol> whose viewport is w×h user units of m.
func (r *renderer) viewport(n *Node, m matrix, w, h float64, st state, useDepth int) {
	if vb, ok := viewBox(n); ok {
		m = m.mul(fitViewBox(vb, w, h, n.attr("preserveAspectRatio")))
	}
	st = r.style(n, st)
	if !st.visible {
		return
	}
	r.children(n, m, st, useDepth)
}

// fitViewBox maps a viewBox onto a w×h viewport.
func fitViewBox(vb [4]float64, w, h float64, par string) matrix {
	sx, sy := w/vb[2], h/vb[3]
	f := strings.Fields(par)
	if len(f) > 0 && f[0] == "none" {
		return matrix{sx, 0, 0, sy, -vb[0] * sx, -vb[1] * sy}
	}
	s := math.Min(sx, sy)
	if len(f) > 1 && f[1] == "slice" {
		s = math.Max(sx, sy)
	}
	align := "xMidYMid"
	if len(f) > 0 {
		align = f[0]
	}
	tx, ty := 0.0, 0.0
	switch {
	case strings.Contains(align, "xMid"):
		tx = (w - vb[2]*s) / 2
	case strings.Contains(align, "xMax"):
		tx = w - vb[2]*s
	}
	switch {
	case strings.Contains(align, "YMid"):
		ty = (h - vb[3]*s) / 2
	case strings.Contains(align, "YMax"):
		ty = h - vb[3]*s
	}
	return matrix{s, 0, 0, s, tx - vb[0]*s, ty - vb[1]*s}
}

func (r *renderer) children(n *Node, m matrix, st state, useDepth int) {
	for _, k := range n.Kids {
		if k.Tag != "" {
			r.node(k, m, st, useDepth)
		}
	}
}

// style returns the state inside n.
func (r *renderer) style(n *Node, st state) state {
	a := n.Attr
	if v, ok := a["color"]; ok {
		if c, ok := css.ParseColor(v); ok {
			st.color = c.Hex()
		}
	}
	if v, ok := a["fill"]; ok {
		st.fill = v
	}
	if v, ok := a["stroke"]; ok {
		st.stroke = v
	}
	opacity := func(k string, cur float64) float64 {
		v, ok := a[k]
		if !ok {
			return cur
		}
		v = strings.TrimSpace(v)
		pct := strings.HasSuffix(v, "%")
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		if err != nil || math.IsNaN(f) {
			return cur
		}
		if pct {
			f /= 100
		}
		return math.Max(0, math.Min(f, 1))
	}
	st.fillOp = opacity("fill-opacity", st.fillOp)
	st.strokeOp = opacity("stroke-opacity", st.strokeOp)
	// opacity is not inherited, but multiplying it down is close enough
	// without group compositing.
	st.opacity *= opacity("opacity", 1)
	if v, ok := a["stroke-width"]; ok {
		if w, ok := length(v, 0); ok && w >= 0 {
			st.strokeWidth = w
		}
	}
	if v, ok := a["stroke-linecap"]; ok {
		st.linecap = strings.TrimSpace(v)
	}
	if strings.TrimSpace(a["display"]) == "none" {
		st.visible = false
	}
	switch strings.TrimSpace(a["visibility"]) {
	case "hidden", "collapse":
		st.visible = false
	}
	return st
}

func (r *renderer) node(n *Node, m matrix, st state, useDepth int) {
	r.visits++
	if r.visits > maxVisits {
		return
	}
	if t, ok := n.Attr["transform"]; ok {
		m = m.mul(parseTransform(t))
	}
	switch n.Tag {
	case "g", "a", "switch":
		st = r.style(n, st)
		if st.visible {
			r.children(n, m, st, useDepth)
		}
		return
	case "svg":
		x, _ := length(n.attr("x"), 0)
		y, _ := length(n.attr("y"), 0)
		w, okW := length(n.attr("width"), 0)
		h, okH := length(n.attr("height"), 0)
		vb, hasVB := viewBox(n)
		if !okW || !okH {
			if !hasVB {
				return
			}
			w, h = vb[2], vb[3]
		}
		r.viewport(n, m.mul(matrix{1, 0, 0, 1, x, y}), w, h, st, useDepth)
		return
	case "use":
		r.use(n, m, st, useDepth)
		return
	}
	var subs []subpath
	scale := m.scale()
	attr := func(k string) float64 {
		v, _ := length(n.attr(k), 0)
		return v
	}
	switch n.Tag {
	case "path":
		subs = parsePathBudget(n.attr("d"), scale, &r.points)
	case "rect":
		subs = r.rect(attr("x"), attr("y"), attr("width"), attr("height"), n, scale)
	case "circle":
		rad := attr("r")
		subs = r.ellipse(attr("cx"), attr("cy"), rad, rad, scale)
	case "ellipse":
		subs = r.ellipse(attr("cx"), attr("cy"), attr("rx"), attr("ry"), scale)
	case "line":
		subs = []subpath{{pts: []point{{attr("x1"), attr("y1")}, {attr("x2"), attr("y2")}}}}
	case "polyline", "polygon":
		pts := parsePoints(n.attr("points"), &r.points)
		if len(pts) > 1 {
			subs = []subpath{{pts: pts, closed: n.Tag == "polygon"}}
		}
	default:
		// Text needs fonts, and the rest (defs, gradients, clip paths,
		// masks, images) is not drawn directly.
		return
	}
	st = r.style(n, st)
	if !st.visible || len(subs) == 0 {
		return
	}
	for i := range subs {
		for j := range subs[i].pts {
			p := &subs[i].pts[j]
			p.x, p.y = m.apply(p.x, p.y)
		}
	}
	if n.Tag != "line" { // a line has no inside
		if col, ok := r.paint(st.fill, st.color, st.fillOp*st.opacity); ok {
			r.fill(subs, col)
		}
	}
	if col, ok := r.paint(st.stroke, st.color, st.strokeOp*st.opacity); ok && st.strokeWidth > 0 {
		r.fill(strokeOutline(subs, st.strokeWidth*scale/2, st.linecap), col)
	}
}

func (r *renderer) rect(x, y, w, h float64, n *Node, scale float64) []subpath {
	if w <= 0 || h <= 0 {
		return nil
	}
	rx, okX := length(n.attr("rx"), 0)
	ry, okY := length(n.attr("ry"), 0)
	if !okX {
		rx = ry
	}
	if !okY {
		ry = rx
	}
	rx, ry = math.Max(0, math.Min(rx, w/2)), math.Max(0, math.Min(ry, h/2))
	b := &pathBuilder{scale: scale, budget: &r.points}
	if rx == 0 || ry == 0 {
		b.moveTo(point{x, y})
		b.lineTo(point{x + w, y})
		b.lineTo(point{x + w, y + h})
		b.lineTo(point{x, y + h})
	} else {
		b.moveTo(point{x + rx, y})
		b.lineTo(point{x + w - rx, y})
		b.ellipse(point{x + w - rx, y + ry}, rx, ry, 0, -math.Pi/2, math.Pi/2)
		b.lineTo(point{x + w, y + h - ry})
		b.ellipse(point{x + w - rx, y + h - ry}, rx, ry, 0, 0, math.Pi/2)
		b.lineTo(point{x + rx, y + h})
		b.ellipse(point{x + rx, y + h - ry}, rx, ry, 0, math.Pi/2, math.Pi/2)
		b.lineTo(point{x, y + ry})
		b.ellipse(point{x + rx, y + ry}, rx, ry, 0, math.Pi, math.Pi/2)
	}
	b.close()
	return b.subs
}

func (r *renderer) ellipse(cx, cy, rx, ry, scale float64) []subpath {
	if rx <= 0 || ry <= 0 {
		return nil
	}
	b := &pathBuilder{scale: scale, budget: &r.points}
	b.moveTo(point{cx + rx, cy})
	b.ellipse(point{cx, cy}, rx, ry, 0, 0, 2*math.Pi)
	b.close()
	return b.subs
}

// use draws the element a <use> refers to, which must be in this drawing.
func (r *renderer) use(n *Node, m matrix, st state, useDepth int) {
	href := strings.TrimSpace(n.attr("href"))
	if useDepth >= maxUseDepth || !strings.HasPrefix(href, "#") {
		return
	}
	target := r.im.ids[href[1:]]
	if target == nil {
		return
	}
	x, _ := length(n.attr("x"), 0)
	y, _ := length(n.attr("y"), 0)
	m = m.mul(matrix{1, 0, 0, 1, x, y})
	st = r.style(n, st)
	if !st.visible {
		return
	}
	if target.Tag == "symbol" || target.Tag == "svg" {
		w, okW := length(n.attr("width"), 0)
		h, okH := length(n.attr("height"), 0)
		vb, hasVB := viewBox(target)
		if !okW || !okH {
			if !hasVB {
				return
			}
			w, h = vb[2], vb[3]
		}
		r.visits++
		r.viewport(target, m, w, h, st, useDepth+1)
		return
	}
	r.node(target, m, st, useDepth+1)
}

// paint resolves a fill or stroke value to a colour. Gradients and patterns
// become the average of their stops, or of the fallback colour.
func (r *renderer) paint(v, current string, alpha float64) (color.NRGBA, bool) {
	v = strings.TrimSpace(v)
	if alpha <= 0 || v == "" || v == "none" {
		return color.NRGBA{}, false
	}
	var c css.Color
	switch {
	case strings.HasPrefix(v, "url("):
		end := strings.IndexByte(v, ')')
		if end < 0 {
			return color.NRGBA{}, false
		}
		id := strings.Trim(strings.TrimSpace(v[4:end]), `"'`)
		if g, ok := r.gradient(strings.TrimPrefix(id, "#"), 0); ok {
			c = g
			break
		}
		return r.paint(v[end+1:], current, alpha) // the fallback, if any
	case strings.EqualFold(v, "currentColor"):
		c, _ = css.HexColor(current)
	default:
		var ok bool
		if c, ok = css.ParseColor(v); !ok {
			return color.NRGBA{}, false
		}
	}
	a := c.A * alpha
	if a <= 0 {
		return color.NRGBA{}, false
	}
	return color.NRGBA{uint8(c.R), uint8(c.G), uint8(c.B), uint8(math.Round(a * 255))}, true
}

// gradient returns the average colour of a gradient's stops, following
// href to the gradient that holds them.
func (r *renderer) gradient(id string, depth int) (css.Color, bool) {
	g := r.im.ids[id]
	if g == nil || depth > 4 || (g.Tag != "linearGradient" && g.Tag != "radialGradient") {
		return css.Color{}, false
	}
	var stops []css.Color
	for _, k := range g.Kids {
		if k.Tag != "stop" {
			continue
		}
		c, ok := css.ParseColor(k.Attr["stop-color"])
		if !ok {
			c = css.Color{A: 1} // black
		}
		if v, ok := k.Attr["stop-opacity"]; ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && !math.IsNaN(f) {
				c.A *= math.Max(0, math.Min(f, 1))
			}
		}
		stops = append(stops, c)
	}
	if len(stops) == 0 {
		if href := strings.TrimSpace(g.Attr["href"]); strings.HasPrefix(href, "#") {
			return r.gradient(href[1:], depth+1)
		}
		return css.Color{}, false
	}
	return css.Average(stops)
}

// fill paints polygons with the nonzero rule, rasterizing only their
// bounding box.
func (r *renderer) fill(polys []subpath, col color.NRGBA) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, s := range polys {
		for _, p := range s.pts {
			minX, minY = math.Min(minX, p.x), math.Min(minY, p.y)
			maxX, maxY = math.Max(maxX, p.x), math.Max(maxY, p.y)
		}
	}
	if math.IsInf(minX, 0) || math.IsNaN(minX+minY+maxX+maxY) {
		return
	}
	rect := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY))).
		Intersect(r.dst.Bounds())
	if rect.Empty() {
		return
	}
	r.pixels -= rect.Dx() * rect.Dy()
	if r.pixels < 0 {
		return
	}
	ox, oy := float64(rect.Min.X), float64(rect.Min.Y)
	r.z.Reset(rect.Dx(), rect.Dy())
	r.z.DrawOp = draw.Over
	for _, s := range polys {
		if len(s.pts) < 3 {
			continue
		}
		r.z.MoveTo(clampF(s.pts[0].x-ox), clampF(s.pts[0].y-oy))
		for _, p := range s.pts[1:] {
			r.z.LineTo(clampF(p.x-ox), clampF(p.y-oy))
		}
		r.z.ClosePath()
	}
	r.z.Draw(r.dst, rect, image.NewUniform(col), image.Point{})
}

// clampF keeps coordinates far outside the canvas from overflowing float32
// arithmetic in the rasterizer.
func clampF(v float64) float32 {
	return float32(math.Max(-1e6, math.Min(v, 1e6)))
}

// strokeOutline returns polygons covering a stroke of half-width hw along
// the subpaths, in device space. All polygons wind the same way, so the
// nonzero rule paints their union once. Joins are round, which is close to
// miter for the small angles of most drawings and never spikes.
func strokeOutline(subs []subpath, hw float64, linecap string) []subpath {
	hw = math.Max(hw, 0.35) // hairlines stay visible
	var out []subpath
	for _, s := range subs {
		pts := s.pts
		if s.closed && len(pts) > 1 && pts[0] != pts[len(pts)-1] {
			pts = append(pts[:len(pts):len(pts)], pts[0])
		}
		for i := 0; i+1 < len(pts); i++ {
			a, b := pts[i], pts[i+1]
			dx, dy := b.x-a.x, b.y-a.y
			l := math.Hypot(dx, dy)
			if l == 0 {
				continue
			}
			ux, uy := dx/l, dy/l
			if !s.closed && linecap == "square" {
				if i == 0 {
					a = point{a.x - ux*hw, a.y - uy*hw}
				}
				if i+2 == len(pts) {
					b = point{b.x + ux*hw, b.y + uy*hw}
				}
			}
			nx, ny := -uy*hw, ux*hw
			out = append(out, subpath{pts: []point{
				{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny},
			}})
		}
		for i, p := range pts {
			end := i == 0 || i == len(pts)-1
			if end && !s.closed && linecap != "round" {
				continue
			}
			out = append(out, disc(p, hw))
		}
	}
	return out
}

// disc is a circle polygon wound the same way as the stroke quads.
func disc(c point, r float64) subpath {
	n := max(8, min(int(r*2), 32))
	pts := make([]point, n)
	for i := range pts {
		t := -2 * math.Pi * float64(i) / float64(n)
		pts[i] = point{c.x + r*math.Cos(t), c.y + r*math.Sin(t)}
	}
	return subpath{pts: pts, closed: true}
}
