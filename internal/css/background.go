package css

import (
	"math"
	"strings"
)

// White is the colour of the page.
var White = Color{255, 255, 255, 1}

// Over blends a translucent colour over an opaque backdrop, which is what a
// reader sees. An opaque colour is returned unchanged.
func (c Color) Over(backdrop Color) Color {
	if c.A >= 1 {
		return Color{c.R, c.G, c.B, 1}
	}
	mix := func(v, b uint8) uint8 {
		return uint8(math.Round(float64(v)*c.A + float64(b)*(1-c.A)))
	}
	return Color{mix(c.R, backdrop.R), mix(c.G, backdrop.G), mix(c.B, backdrop.B), 1}
}

// Contrast returns the WCAG contrast ratio between two opaque colours, from 1
// (identical luminance) to 21 (black on white).
func (c Color) Contrast(o Color) float64 {
	l1, l2 := c.luminance(), o.luminance()
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func (c Color) luminance() float64 {
	ch := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// HexColor parses an RRGGBB string as written by Color.Hex.
func HexColor(hex string) (Color, bool) {
	if len(hex) != 6 {
		return Color{}, false
	}
	return parseHexColor(strings.ToLower(hex))
}

// GradientColor returns one colour standing in for a CSS gradient (linear,
// radial or conic, repeating or not): the average of its colour stops. Word
// shading is a single colour, and the average is closest to the overall tone
// a reader sees, which is what decides whether text on it stays readable.
// Stops are averaged with their alpha, so transparent stops thin the colour
// rather than darken it. ok is false for anything that is not a gradient or
// whose stops are all transparent.
func GradientColor(s string) (Color, bool) {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '(')
	if open < 0 || s[len(s)-1] != ')' {
		return Color{}, false
	}
	switch strings.ToLower(s[:open]) {
	case "linear-gradient", "radial-gradient", "conic-gradient",
		"repeating-linear-gradient", "repeating-radial-gradient", "repeating-conic-gradient":
	default:
		return Color{}, false
	}
	var r, g, b, a float64
	n := 0
	for _, stop := range SplitComma(s[open+1 : len(s)-1]) {
		for _, t := range Fields(stop) {
			col, ok := ParseColor(t)
			if !ok {
				continue
			}
			r += float64(col.R) * col.A
			g += float64(col.G) * col.A
			b += float64(col.B) * col.A
			a += col.A
			n++
			break
		}
	}
	if n == 0 || a == 0 {
		return Color{}, false
	}
	round := func(v float64) uint8 { return uint8(math.Round(v / a)) }
	return Color{round(r), round(g), round(b), a / float64(n)}, true
}
