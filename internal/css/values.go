package css

import (
	"math"
	"strconv"
	"strings"
)

// Color is a parsed CSS colour.
type Color struct {
	R, G, B uint8
	A       float64 // 0..1
}

// Hex returns the colour as RRGGBB, ignoring alpha.
func (c Color) Hex() string {
	const digits = "0123456789ABCDEF"
	b := [6]byte{
		digits[c.R>>4], digits[c.R&15],
		digits[c.G>>4], digits[c.G&15],
		digits[c.B>>4], digits[c.B&15],
	}
	return string(b[:])
}

// OnWhite blends a translucent colour over white, which is what a reader
// sees for a background colour on an unstyled page.
func (c Color) OnWhite() Color {
	if c.A >= 1 {
		return c
	}
	mix := func(v uint8) uint8 {
		return uint8(math.Round(float64(v)*c.A + 255*(1-c.A)))
	}
	return Color{mix(c.R), mix(c.G), mix(c.B), 1}
}

// ParseColor parses a CSS colour value. It returns false for values that are
// not colours, including "inherit" and "currentcolor". "transparent" parses
// to a colour with zero alpha.
func ParseColor(s string) (Color, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return Color{}, false
	}
	if s[0] == '#' {
		return parseHexColor(s[1:])
	}
	if s == "transparent" {
		return Color{}, true
	}
	if i := strings.IndexByte(s, '('); i > 0 && s[len(s)-1] == ')' {
		args := splitColorArgs(s[i+1 : len(s)-1])
		switch s[:i] {
		case "rgb", "rgba":
			return parseRGB(args)
		case "hsl", "hsla":
			return parseHSL(args)
		}
		return Color{}, false
	}
	if v, ok := namedColors[s]; ok {
		return Color{uint8(v >> 16), uint8(v >> 8), uint8(v), 1}, true
	}
	return Color{}, false
}

func parseHexColor(h string) (Color, bool) {
	nib := func(b byte) (uint8, bool) {
		switch {
		case b >= '0' && b <= '9':
			return b - '0', true
		case b >= 'a' && b <= 'f':
			return b - 'a' + 10, true
		}
		return 0, false
	}
	var v [8]uint8
	if len(h) != 3 && len(h) != 4 && len(h) != 6 && len(h) != 8 {
		return Color{}, false
	}
	for i := 0; i < len(h); i++ {
		n, ok := nib(h[i])
		if !ok {
			return Color{}, false
		}
		v[i] = n
	}
	c := Color{A: 1}
	switch len(h) {
	case 3, 4:
		c.R, c.G, c.B = v[0]*17, v[1]*17, v[2]*17
		if len(h) == 4 {
			c.A = float64(v[3]*17) / 255
		}
	default:
		c.R, c.G, c.B = v[0]<<4|v[1], v[2]<<4|v[3], v[4]<<4|v[5]
		if len(h) == 8 {
			c.A = float64(v[6]<<4|v[7]) / 255
		}
	}
	return c, true
}

// splitColorArgs splits "1, 2, 3", "1 2 3" and "1 2 3 / 50%" alike.
func splitColorArgs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '/' || r == ' ' || r == '\t' || r == '\n'
	})
}

func parseAlpha(s string) (float64, bool) {
	pct := strings.HasSuffix(s, "%")
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || math.IsNaN(f) {
		return 0, false
	}
	if pct {
		f /= 100
	}
	return clamp(f, 0, 1), true
}

func parseRGB(args []string) (Color, bool) {
	if len(args) != 3 && len(args) != 4 {
		return Color{}, false
	}
	c := Color{A: 1}
	ch := [3]*uint8{&c.R, &c.G, &c.B}
	for i := 0; i < 3; i++ {
		a := args[i]
		pct := strings.HasSuffix(a, "%")
		f, err := strconv.ParseFloat(strings.TrimSuffix(a, "%"), 64)
		if err != nil || math.IsNaN(f) {
			return Color{}, false
		}
		if pct {
			f = f * 255 / 100
		}
		*ch[i] = uint8(math.Round(clamp(f, 0, 255)))
	}
	if len(args) == 4 {
		a, ok := parseAlpha(args[3])
		if !ok {
			return Color{}, false
		}
		c.A = a
	}
	return c, true
}

func parseHSL(args []string) (Color, bool) {
	if len(args) != 3 && len(args) != 4 {
		return Color{}, false
	}
	hs := strings.TrimSuffix(args[0], "deg")
	h, err := strconv.ParseFloat(hs, 64)
	if err != nil || math.IsNaN(h) || math.IsInf(h, 0) {
		return Color{}, false
	}
	s, ok1 := parseAlpha(args[1])
	l, ok2 := parseAlpha(args[2])
	if !ok1 || !ok2 || !strings.HasSuffix(args[1], "%") || !strings.HasSuffix(args[2], "%") {
		return Color{}, false
	}
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	// Standard HSL to RGB conversion.
	f := func(n float64) uint8 {
		k := math.Mod(n+h/30, 12)
		a := s * math.Min(l, 1-l)
		v := l - a*math.Max(-1, math.Min(math.Min(k-3, 9-k), 1))
		return uint8(math.Round(clamp(v, 0, 1) * 255))
	}
	c := Color{f(0), f(8), f(4), 1}
	if len(args) == 4 {
		a, ok := parseAlpha(args[3])
		if !ok {
			return Color{}, false
		}
		c.A = a
	}
	return c, true
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// Length is a CSS dimension. Unit is lower case; it is empty for a bare
// number and "%" for a percentage.
type Length struct {
	V    float64
	Unit string
}

// ParseLength parses a single CSS length, percentage or number.
func ParseLength(s string) (Length, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return Length{}, false
	}
	i := len(s)
	for i > 0 {
		c := s[i-1]
		if (c >= 'a' && c <= 'z') || c == '%' {
			i--
			continue
		}
		break
	}
	num, unit := s[:i], s[i:]
	// Reject things strconv accepts but CSS does not ("inf", "nan", "0x10").
	for j := 0; j < len(num); j++ {
		c := num[j]
		if !(c >= '0' && c <= '9') && c != '.' && c != '-' && c != '+' {
			return Length{}, false
		}
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return Length{}, false
	}
	switch unit {
	case "", "%", "px", "pt", "pc", "in", "cm", "mm", "q", "em", "rem", "ex", "ch":
		return Length{f, unit}, true
	}
	return Length{}, false
}

// Points converts the length to points. emPt is the font size that em units
// refer to and remPt the root font size. Percentages and bare numbers other
// than zero have no fixed size, so they return false.
func (l Length) Points(emPt, remPt float64) (float64, bool) {
	switch l.Unit {
	case "px":
		return l.V * 0.75, true
	case "pt":
		return l.V, true
	case "pc":
		return l.V * 12, true
	case "in":
		return l.V * 72, true
	case "cm":
		return l.V * 72 / 2.54, true
	case "mm":
		return l.V * 72 / 25.4, true
	case "q":
		return l.V * 72 / 101.6, true
	case "em":
		return l.V * emPt, true
	case "rem":
		return l.V * remPt, true
	case "ex", "ch":
		return l.V * emPt / 2, true
	case "":
		if l.V == 0 {
			return 0, true
		}
	}
	return 0, false
}

// Fields splits a CSS value on whitespace, keeping quoted strings and
// parenthesised groups such as rgb(1, 2, 3) together.
func Fields(s string) []string {
	var out []string
	start, depth := -1, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
			if start < 0 {
				start = i
			}
		case c == '(':
			depth++
			if start < 0 {
				start = i
			}
		case c == ')':
			if depth > 0 {
				depth--
			}
			if start < 0 {
				start = i
			}
		case depth == 0 && (c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'):
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// SplitComma splits a CSS value on top-level commas.
func SplitComma(s string) []string {
	var out []string
	start, depth := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			if depth > 0 {
				depth--
			}
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

var namedColors = map[string]uint32{
	"aliceblue": 0xF0F8FF, "antiquewhite": 0xFAEBD7, "aqua": 0x00FFFF, "aquamarine": 0x7FFFD4,
	"azure": 0xF0FFFF, "beige": 0xF5F5DC, "bisque": 0xFFE4C4, "black": 0x000000,
	"blanchedalmond": 0xFFEBCD, "blue": 0x0000FF, "blueviolet": 0x8A2BE2, "brown": 0xA52A2A,
	"burlywood": 0xDEB887, "cadetblue": 0x5F9EA0, "chartreuse": 0x7FFF00, "chocolate": 0xD2691E,
	"coral": 0xFF7F50, "cornflowerblue": 0x6495ED, "cornsilk": 0xFFF8DC, "crimson": 0xDC143C,
	"cyan": 0x00FFFF, "darkblue": 0x00008B, "darkcyan": 0x008B8B, "darkgoldenrod": 0xB8860B,
	"darkgray": 0xA9A9A9, "darkgreen": 0x006400, "darkgrey": 0xA9A9A9, "darkkhaki": 0xBDB76B,
	"darkmagenta": 0x8B008B, "darkolivegreen": 0x556B2F, "darkorange": 0xFF8C00, "darkorchid": 0x9932CC,
	"darkred": 0x8B0000, "darksalmon": 0xE9967A, "darkseagreen": 0x8FBC8F, "darkslateblue": 0x483D8B,
	"darkslategray": 0x2F4F4F, "darkslategrey": 0x2F4F4F, "darkturquoise": 0x00CED1, "darkviolet": 0x9400D3,
	"deeppink": 0xFF1493, "deepskyblue": 0x00BFFF, "dimgray": 0x696969, "dimgrey": 0x696969,
	"dodgerblue": 0x1E90FF, "firebrick": 0xB22222, "floralwhite": 0xFFFAF0, "forestgreen": 0x228B22,
	"fuchsia": 0xFF00FF, "gainsboro": 0xDCDCDC, "ghostwhite": 0xF8F8FF, "gold": 0xFFD700,
	"goldenrod": 0xDAA520, "gray": 0x808080, "green": 0x008000, "greenyellow": 0xADFF2F,
	"grey": 0x808080, "honeydew": 0xF0FFF0, "hotpink": 0xFF69B4, "indianred": 0xCD5C5C,
	"indigo": 0x4B0082, "ivory": 0xFFFFF0, "khaki": 0xF0E68C, "lavender": 0xE6E6FA,
	"lavenderblush": 0xFFF0F5, "lawngreen": 0x7CFC00, "lemonchiffon": 0xFFFACD, "lightblue": 0xADD8E6,
	"lightcoral": 0xF08080, "lightcyan": 0xE0FFFF, "lightgoldenrodyellow": 0xFAFAD2, "lightgray": 0xD3D3D3,
	"lightgreen": 0x90EE90, "lightgrey": 0xD3D3D3, "lightpink": 0xFFB6C1, "lightsalmon": 0xFFA07A,
	"lightseagreen": 0x20B2AA, "lightskyblue": 0x87CEFA, "lightslategray": 0x778899, "lightslategrey": 0x778899,
	"lightsteelblue": 0xB0C4DE, "lightyellow": 0xFFFFE0, "lime": 0x00FF00, "limegreen": 0x32CD32,
	"linen": 0xFAF0E6, "magenta": 0xFF00FF, "maroon": 0x800000, "mediumaquamarine": 0x66CDAA,
	"mediumblue": 0x0000CD, "mediumorchid": 0xBA55D3, "mediumpurple": 0x9370DB, "mediumseagreen": 0x3CB371,
	"mediumslateblue": 0x7B68EE, "mediumspringgreen": 0x00FA9A, "mediumturquoise": 0x48D1CC, "mediumvioletred": 0xC71585,
	"midnightblue": 0x191970, "mintcream": 0xF5FFFA, "mistyrose": 0xFFE4E1, "moccasin": 0xFFE4B5,
	"navajowhite": 0xFFDEAD, "navy": 0x000080, "oldlace": 0xFDF5E6, "olive": 0x808000,
	"olivedrab": 0x6B8E23, "orange": 0xFFA500, "orangered": 0xFF4500, "orchid": 0xDA70D6,
	"palegoldenrod": 0xEEE8AA, "palegreen": 0x98FB98, "paleturquoise": 0xAFEEEE, "palevioletred": 0xDB7093,
	"papayawhip": 0xFFEFD5, "peachpuff": 0xFFDAB9, "peru": 0xCD853F, "pink": 0xFFC0CB,
	"plum": 0xDDA0DD, "powderblue": 0xB0E0E6, "purple": 0x800080, "rebeccapurple": 0x663399,
	"red": 0xFF0000, "rosybrown": 0xBC8F8F, "royalblue": 0x4169E1, "saddlebrown": 0x8B4513,
	"salmon": 0xFA8072, "sandybrown": 0xF4A460, "seagreen": 0x2E8B57, "seashell": 0xFFF5EE,
	"sienna": 0xA0522D, "silver": 0xC0C0C0, "skyblue": 0x87CEEB, "slateblue": 0x6A5ACD,
	"slategray": 0x708090, "slategrey": 0x708090, "snow": 0xFFFAFA, "springgreen": 0x00FF7F,
	"steelblue": 0x4682B4, "tan": 0xD2B48C, "teal": 0x008080, "thistle": 0xD8BFD8,
	"tomato": 0xFF6347, "turquoise": 0x40E0D0, "violet": 0xEE82EE, "wheat": 0xF5DEB3,
	"white": 0xFFFFFF, "whitesmoke": 0xF5F5F5, "yellow": 0xFFFF00, "yellowgreen": 0x9ACD32,
}
