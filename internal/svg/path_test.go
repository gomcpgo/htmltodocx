package svg

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// flat renders flattened subpaths as "x,y x,y | x,y ..." with one decimal.
func flat(subs []subpath) string {
	var parts []string
	for _, s := range subs {
		var pts []string
		for _, p := range s.pts {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", p.x, p.y))
		}
		if s.closed {
			pts = append(pts, "Z")
		}
		parts = append(parts, strings.Join(pts, " "))
	}
	return strings.Join(parts, " | ")
}

func TestParsePathLines(t *testing.T) {
	tests := []struct{ d, want string }{
		{"M0 0 L10 0 L10 10 Z", "0.0,0.0 10.0,0.0 10.0,10.0 Z"},
		{"M0,0 10,0 10,10z", "0.0,0.0 10.0,0.0 10.0,10.0 Z"},
		{"m1 1 l2 0 0 2 h-2 v-2", "1.0,1.0 3.0,1.0 3.0,3.0 1.0,3.0 1.0,1.0"},
		{"M0 0H5V5H0z M10 10 h1", "0.0,0.0 5.0,0.0 5.0,5.0 0.0,5.0 Z | 10.0,10.0 11.0,10.0"},
		// Numbers run together the way minifiers write them.
		{"M1.5.5L-1-1", "1.5,0.5 -1.0,-1.0"},
		{"M1e1 2E0l1e-1,0", "10.0,2.0 10.1,2.0"},
		// After Z the current point is the start of the subpath.
		{"M2 2 l2 0 z l0 2", "2.0,2.0 4.0,2.0 Z | 2.0,2.0 2.0,4.0"},
		// Errors keep what was parsed before them.
		{"M0 0 L5 5 X 1 1", "0.0,0.0 5.0,5.0"},
		{"L5 5", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := flat(parsePath(tt.d, 1)); got != tt.want {
			t.Errorf("parsePath(%q)\n got %s\nwant %s", tt.d, got, tt.want)
		}
	}
}

func endpoints(subs []subpath) string {
	var out []string
	for _, s := range subs {
		a, b := s.pts[0], s.pts[len(s.pts)-1]
		out = append(out, fmt.Sprintf("%.1f,%.1f>%.1f,%.1f", a.x, a.y, b.x, b.y))
	}
	return strings.Join(out, " ")
}

func TestParsePathCurves(t *testing.T) {
	tests := []struct{ d, want string }{
		{"M0 0 C0 10 10 10 10 0", "0.0,0.0>10.0,0.0"},
		{"M0 0 c0 10 10 10 10 0 s10 -10 10 0", "0.0,0.0>20.0,0.0"},
		{"M0 0 Q5 10 10 0 T20 0", "0.0,0.0>20.0,0.0"},
		{"M0 0 q5 10 10 0 t10 0", "0.0,0.0>20.0,0.0"},
		{"M0 0 A5 5 0 0 1 10 0", "0.0,0.0>10.0,0.0"},
		// Arc flags may be written without separators.
		{"M0 0a5 5 0 0110 0", "0.0,0.0>10.0,0.0"},
	}
	for _, tt := range tests {
		subs := parsePath(tt.d, 1)
		if got := endpoints(subs); got != tt.want {
			t.Errorf("parsePath(%q) endpoints = %s, want %s", tt.d, got, tt.want)
		}
		if n := len(subs[0].pts); n < 4 {
			t.Errorf("parsePath(%q): curve flattened to %d points", tt.d, n)
		}
	}
	// A half circle arc passes through its top (or bottom) point.
	subs := parsePath("M0 0 A5 5 0 0 1 10 0", 1)
	minY := math.Inf(1)
	for _, p := range subs[0].pts {
		minY = math.Min(minY, p.y)
	}
	if math.Abs(minY+5) > 0.1 {
		t.Errorf("arc top = %.2f, want -5", minY)
	}
	// Radii too small for the endpoints are scaled up.
	subs = parsePath("M0 0 A1 1 0 0 1 10 0", 1)
	minY = math.Inf(1)
	for _, p := range subs[0].pts {
		minY = math.Min(minY, p.y)
	}
	if math.Abs(minY+5) > 0.1 {
		t.Errorf("scaled arc top = %.2f, want -5", minY)
	}
}

func TestParsePathLimits(t *testing.T) {
	d := "M0 0" + strings.Repeat(" C1 1 2 2 3 3", 200000)
	subs := parsePath(d, 1)
	n := 0
	for _, s := range subs {
		n += len(s.pts)
	}
	if n > maxPoints {
		t.Errorf("%d points, limit is %d", n, maxPoints)
	}
}

func TestParseTransform(t *testing.T) {
	tests := []struct {
		in   string
		x, y float64 // where (1, 0) lands
	}{
		{"translate(10 5)", 11, 5},
		{"translate(10)", 11, 0},
		{"scale(2)", 2, 0},
		{"scale(2, 3) translate(1,1)", 4, 3},
		{"rotate(90)", 0, 1},
		{"rotate(90 1 0)", 1, 0},
		{"matrix(1 0 0 1 5 6)", 6, 6},
		{"skewX(45)", 1, 0},
		{"bogus(1) translate(1 1)", 1, 0},
		{"", 1, 0},
	}
	for _, tt := range tests {
		m := parseTransform(tt.in)
		x, y := m.apply(1, 0)
		if math.Abs(x-tt.x) > 1e-9 || math.Abs(y-tt.y) > 1e-9 {
			t.Errorf("parseTransform(%q) maps (1,0) to (%.3f,%.3f), want (%v,%v)", tt.in, x, y, tt.x, tt.y)
		}
	}
}
