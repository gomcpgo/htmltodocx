package svg

import (
	"math"
	"strconv"
	"strings"
)

// maxPoints bounds the points one drawing may produce after curves are
// flattened, so a hostile path cannot use unbounded memory or time.
const maxPoints = 200000

type point struct{ x, y float64 }

// subpath is a flattened run of connected points.
type subpath struct {
	pts    []point
	closed bool
}

// pathBuilder collects flattened subpaths. scale is the number of device
// pixels per user unit, which sets how finely curves are flattened.
type pathBuilder struct {
	subs   []subpath
	scale  float64
	budget *int
}

func (b *pathBuilder) moveTo(p point) {
	if *b.budget <= 0 {
		return
	}
	*b.budget--
	b.subs = append(b.subs, subpath{pts: []point{p}})
}

func (b *pathBuilder) lineTo(p point) {
	if len(b.subs) == 0 || *b.budget <= 0 {
		return
	}
	*b.budget--
	s := &b.subs[len(b.subs)-1]
	s.pts = append(s.pts, p)
}

func (b *pathBuilder) close() {
	if len(b.subs) > 0 {
		b.subs[len(b.subs)-1].closed = true
	}
}

// steps is the number of line segments used for a curve whose control
// polygon is length long, in user units.
func (b *pathBuilder) steps(length float64) int {
	n := int(math.Ceil(math.Sqrt(length*b.scale) * 1.5))
	return max(4, min(n, 64))
}

func (b *pathBuilder) cubic(p0, p1, p2, p3 point) {
	n := b.steps(dist(p0, p1) + dist(p1, p2) + dist(p2, p3))
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		b.lineTo(point{
			u*u*u*p0.x + 3*u*u*t*p1.x + 3*u*t*t*p2.x + t*t*t*p3.x,
			u*u*u*p0.y + 3*u*u*t*p1.y + 3*u*t*t*p2.y + t*t*t*p3.y,
		})
	}
}

func (b *pathBuilder) quad(p0, p1, p2 point) {
	n := b.steps(dist(p0, p1) + dist(p1, p2))
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		b.lineTo(point{u*u*p0.x + 2*u*t*p1.x + t*t*p2.x, u*u*p0.y + 2*u*t*p1.y + t*t*p2.y})
	}
}

// ellipse adds the arc of an ellipse centred on c, from angle t0 through dt
// radians, rotated by phi.
func (b *pathBuilder) ellipse(c point, rx, ry, phi, t0, dt float64) {
	n := max(int(math.Ceil(math.Abs(dt)/(math.Pi/18))), b.steps(math.Abs(dt)*max(rx, ry)))
	n = min(n, 128)
	cos, sin := math.Cos(phi), math.Sin(phi)
	for i := 1; i <= n; i++ {
		t := t0 + dt*float64(i)/float64(n)
		x, y := rx*math.Cos(t), ry*math.Sin(t)
		b.lineTo(point{c.x + x*cos - y*sin, c.y + x*sin + y*cos})
	}
}

// arc adds an SVG elliptical arc from p0 to p, converting from endpoint to
// centre parameterisation as the SVG specification describes.
func (b *pathBuilder) arc(p0 point, rx, ry, angle float64, large, sweep bool, p point) {
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 || p0 == p {
		b.lineTo(p)
		return
	}
	phi := angle * math.Pi / 180
	cos, sin := math.Cos(phi), math.Sin(phi)
	dx, dy := (p0.x-p.x)/2, (p0.y-p.y)/2
	x1 := cos*dx + sin*dy
	y1 := -sin*dx + cos*dy
	if l := x1*x1/(rx*rx) + y1*y1/(ry*ry); l > 1 {
		s := math.Sqrt(l)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1*y1 - ry*ry*x1*x1
	den := rx*rx*y1*y1 + ry*ry*x1*x1
	k := 0.0
	if den > 0 && num > 0 {
		k = math.Sqrt(num / den)
	}
	if large == sweep {
		k = -k
	}
	cx1, cy1 := k*rx*y1/ry, -k*ry*x1/rx
	c := point{cos*cx1 - sin*cy1 + (p0.x+p.x)/2, sin*cx1 + cos*cy1 + (p0.y+p.y)/2}
	t0 := math.Atan2((y1-cy1)/ry, (x1-cx1)/rx)
	t1 := math.Atan2((-y1-cy1)/ry, (-x1-cx1)/rx)
	dt := t1 - t0
	if sweep && dt < 0 {
		dt += 2 * math.Pi
	} else if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	}
	b.ellipse(c, rx, ry, phi, t0, dt)
	// End exactly on the endpoint, whatever rounding did.
	s := &b.subs[len(b.subs)-1]
	if n := len(s.pts); n > 0 {
		s.pts[n-1] = p
	}
}

func dist(a, b point) float64 { return math.Hypot(a.x-b.x, a.y-b.y) }

// pathScanner reads the numbers and flags of path data and point lists.
type pathScanner struct {
	s string
	i int
}

func (sc *pathScanner) skipSep() {
	for sc.i < len(sc.s) {
		switch sc.s[sc.i] {
		case ' ', '\t', '\n', '\r', '\f', ',':
			sc.i++
		default:
			return
		}
	}
}

// number reads one number. "1.5.5" is two numbers and "-1-1" too.
func (sc *pathScanner) number() (float64, bool) {
	sc.skipSep()
	start := sc.i
	if sc.i < len(sc.s) && (sc.s[sc.i] == '+' || sc.s[sc.i] == '-') {
		sc.i++
	}
	digits, dot := false, false
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		if c >= '0' && c <= '9' {
			digits = true
		} else if c == '.' && !dot {
			dot = true
		} else {
			break
		}
		sc.i++
	}
	if digits && sc.i < len(sc.s) && (sc.s[sc.i] == 'e' || sc.s[sc.i] == 'E') {
		j := sc.i + 1
		if j < len(sc.s) && (sc.s[j] == '+' || sc.s[j] == '-') {
			j++
		}
		if j < len(sc.s) && sc.s[j] >= '0' && sc.s[j] <= '9' {
			for j < len(sc.s) && sc.s[j] >= '0' && sc.s[j] <= '9' {
				j++
			}
			sc.i = j
		}
	}
	if !digits {
		sc.i = start
		return 0, false
	}
	v, err := strconv.ParseFloat(sc.s[start:sc.i], 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

// flag reads an arc flag, a single 0 or 1 that needs no separator.
func (sc *pathScanner) flag() (bool, bool) {
	sc.skipSep()
	if sc.i < len(sc.s) && (sc.s[sc.i] == '0' || sc.s[sc.i] == '1') {
		sc.i++
		return sc.s[sc.i-1] == '1', true
	}
	return false, false
}

func (sc *pathScanner) numbers(out []float64) bool {
	for i := range out {
		v, ok := sc.number()
		if !ok {
			return false
		}
		out[i] = v
	}
	return true
}

// parsePath flattens SVG path data. Like a browser it draws everything up to
// the first error.
func parsePath(d string, scale float64) []subpath {
	budget := maxPoints
	return parsePathBudget(d, scale, &budget)
}

func parsePathBudget(d string, scale float64, budget *int) []subpath {
	b := &pathBuilder{scale: scale, budget: budget}
	sc := &pathScanner{s: d}
	var cur, start, ctrl point // ctrl: last control point, for S and T
	var cmd, prev byte
	var a [7]float64
	for {
		sc.skipSep()
		if sc.i >= len(sc.s) || *budget <= 0 {
			break
		}
		if c := sc.s[sc.i]; strings.IndexByte("MmLlHhVvCcSsQqTtAaZz", c) >= 0 {
			cmd = c
			sc.i++
		} else if cmd == 0 || cmd == 'Z' || cmd == 'z' {
			break
		}
		if len(b.subs) == 0 && cmd != 'M' && cmd != 'm' {
			break
		}
		rel := cmd >= 'a'
		off := func(x, y float64) point {
			if rel {
				return point{cur.x + x, cur.y + y}
			}
			return point{x, y}
		}
		switch cmd {
		case 'Z', 'z':
			b.close()
			cur = start
			b.moveTo(cur) // a following command starts here
		case 'M', 'm':
			if !sc.numbers(a[:2]) {
				goto done
			}
			if n := len(b.subs); n > 0 && len(b.subs[n-1].pts) == 1 && !b.subs[n-1].closed {
				b.subs = b.subs[:n-1] // a lone point left by Z or M
			}
			cur = off(a[0], a[1])
			start = cur
			b.moveTo(cur)
			// Further pairs are implicit line-tos.
			if cmd == 'M' {
				cmd = 'L'
			} else {
				cmd = 'l'
			}
		case 'L', 'l':
			if !sc.numbers(a[:2]) {
				goto done
			}
			cur = off(a[0], a[1])
			b.lineTo(cur)
		case 'H', 'h':
			if !sc.numbers(a[:1]) {
				goto done
			}
			if rel {
				cur.x += a[0]
			} else {
				cur.x = a[0]
			}
			b.lineTo(cur)
		case 'V', 'v':
			if !sc.numbers(a[:1]) {
				goto done
			}
			if rel {
				cur.y += a[0]
			} else {
				cur.y = a[0]
			}
			b.lineTo(cur)
		case 'C', 'c':
			if !sc.numbers(a[:6]) {
				goto done
			}
			p1, p2, p := off(a[0], a[1]), off(a[2], a[3]), off(a[4], a[5])
			b.cubic(cur, p1, p2, p)
			ctrl, cur = p2, p
		case 'S', 's':
			if !sc.numbers(a[:4]) {
				goto done
			}
			p1 := cur
			if prev == 'C' || prev == 'S' {
				p1 = point{2*cur.x - ctrl.x, 2*cur.y - ctrl.y}
			}
			p2, p := off(a[0], a[1]), off(a[2], a[3])
			b.cubic(cur, p1, p2, p)
			ctrl, cur = p2, p
		case 'Q', 'q':
			if !sc.numbers(a[:4]) {
				goto done
			}
			p1, p := off(a[0], a[1]), off(a[2], a[3])
			b.quad(cur, p1, p)
			ctrl, cur = p1, p
		case 'T', 't':
			if !sc.numbers(a[:2]) {
				goto done
			}
			p1 := cur
			if prev == 'Q' || prev == 'T' {
				p1 = point{2*cur.x - ctrl.x, 2*cur.y - ctrl.y}
			}
			p := off(a[0], a[1])
			b.quad(cur, p1, p)
			ctrl, cur = p1, p
		case 'A', 'a':
			if !sc.numbers(a[:3]) {
				goto done
			}
			large, ok1 := sc.flag()
			sweep, ok2 := sc.flag()
			if !ok1 || !ok2 || !sc.numbers(a[3:5]) {
				goto done
			}
			p := off(a[3], a[4])
			b.arc(cur, a[0], a[1], a[2], large, sweep, p)
			cur = p
		}
		prev = cmd &^ 0x20 // upper case
	}
done:
	// Drop lone points: a moveto with nothing after it draws nothing.
	out := b.subs[:0]
	for _, s := range b.subs {
		if len(s.pts) > 1 {
			out = append(out, s)
		}
	}
	return out
}

// parsePoints reads the points attribute of polyline and polygon.
func parsePoints(s string, budget *int) []point {
	sc := &pathScanner{s: s}
	var pts []point
	for *budget > 0 {
		x, ok1 := sc.number()
		y, ok2 := sc.number()
		if !ok1 || !ok2 {
			break
		}
		*budget--
		pts = append(pts, point{x, y})
	}
	return pts
}

// matrix is an affine transform: x' = a*x + c*y + e, y' = b*x + d*y + f.
type matrix struct{ a, b, c, d, e, f float64 }

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) apply(x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

// mul returns the transform that applies n first, then m.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m.a*n.a + m.c*n.b, m.b*n.a + m.d*n.b,
		m.a*n.c + m.c*n.d, m.b*n.c + m.d*n.d,
		m.a*n.e + m.c*n.f + m.e, m.b*n.e + m.d*n.f + m.f,
	}
}

// scale is the average factor by which m scales lengths.
func (m matrix) scale() float64 { return math.Sqrt(math.Abs(m.a*m.d - m.b*m.c)) }

// parseTransform parses a transform list. An invalid list is ignored as a
// whole, as the specification says.
func parseTransform(s string) matrix {
	m := identity
	sc := &pathScanner{s: s}
	for {
		sc.skipSep()
		if sc.i >= len(s) {
			return m
		}
		j := sc.i
		for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
			j++
		}
		name := s[sc.i:j]
		sc.i = j
		sc.skipSep()
		if sc.i >= len(s) || s[sc.i] != '(' {
			return identity
		}
		sc.i++
		var args []float64
		for len(args) < 6 {
			v, ok := sc.number()
			if !ok {
				break
			}
			args = append(args, v)
		}
		sc.skipSep()
		if sc.i >= len(s) || s[sc.i] != ')' {
			return identity
		}
		sc.i++
		var t matrix
		n := len(args)
		switch {
		case name == "matrix" && n == 6:
			t = matrix{args[0], args[1], args[2], args[3], args[4], args[5]}
		case name == "translate" && (n == 1 || n == 2):
			t = identity
			t.e = args[0]
			if n == 2 {
				t.f = args[1]
			}
		case name == "scale" && (n == 1 || n == 2):
			t = identity
			t.a, t.d = args[0], args[0]
			if n == 2 {
				t.d = args[1]
			}
		case name == "rotate" && (n == 1 || n == 3):
			r := args[0] * math.Pi / 180
			cos, sin := math.Cos(r), math.Sin(r)
			t = matrix{cos, sin, -sin, cos, 0, 0}
			if n == 3 {
				cx, cy := args[1], args[2]
				t = matrix{1, 0, 0, 1, cx, cy}.mul(t).mul(matrix{1, 0, 0, 1, -cx, -cy})
			}
		case name == "skewX" && n == 1:
			t = matrix{1, 0, math.Tan(args[0] * math.Pi / 180), 1, 0, 0}
		case name == "skewY" && n == 1:
			t = matrix{1, math.Tan(args[0] * math.Pi / 180), 0, 1, 0, 0}
		default:
			return identity
		}
		m = m.mul(t)
	}
}
