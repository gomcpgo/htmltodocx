package dom

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/model"
)

// Text measurement for table layout. The converter has no fonts, so widths
// are estimated from character classes, with proportions typical of the
// sans-serif fonts documents use. Table layout adds widthSlack to them when
// there is room: the reader may substitute a wider font than the one named,
// and a column a little too wide costs less than a word broken in the middle.

// widthSlack is the room added to text widths, as a fraction.
const widthSlack = 0.15

// runeEm returns the advance width of r as a fraction of the font size.
func runeEm(r rune) float64 {
	switch {
	case r == ' ' || r == ' ':
		return 0.28
	case strings.ContainsRune("iljI.,:;'|!`", r):
		return 0.28
	case strings.ContainsRune("frt()[]{}-/\"", r):
		return 0.36
	case r >= '0' && r <= '9':
		return 0.62
	case r == 'm' || r == 'w':
		return 0.85
	case r == 'M' || r == 'W' || r == '%' || r == '@':
		return 0.9
	case r >= 'a' && r <= 'z':
		return 0.55
	case r >= 'A' && r <= 'Z':
		return 0.7
	case r < utf8.RuneSelf:
		return 0.6
	case isWide(r):
		return 1
	}
	return 0.65
}

// isWide reports whether r is drawn a full em wide: CJK ideographs, kana,
// Hangul and full-width forms.
func isWide(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		(r >= 0xFF00 && r <= 0xFF60)
}

// maxMeasureNodes bounds the nodes visited to measure one cell.
const maxMeasureNodes = 2000

// lineMeasure accumulates the widths of words and lines, in points.
type lineMeasure struct {
	min, max   float64 // results so far
	line, word float64 // the line and the word being measured
	space      bool    // the last character was white space
}

func (m *lineMeasure) endWord() {
	m.min = max(m.min, m.word)
	m.word = 0
}

func (m *lineMeasure) endLine() {
	m.endWord()
	m.max = max(m.max, m.line)
	m.line, m.space = 0, false
}

// text adds s, in the given style, to the current line.
func (m *lineMeasure) text(s string, st *style, rootPt float64) {
	pt := st.emPt
	bold := st.bold == model.On || st.styleBold
	if st.caps == model.On {
		s = strings.ToUpper(s)
	}
	spacing := 0.0
	if st.letterSpacing.Unit != "" {
		if v, ok := st.letterSpacing.Points(pt, rootPt); ok {
			spacing = v
		}
	}
	for _, r := range s {
		if r < utf8.RuneSelf && isCollapsible(byte(r)) {
			if !m.space && m.line > 0 {
				m.endWord()
				m.line += runeEm(' ') * pt
			}
			m.space = true
			continue
		}
		m.space = false
		w := runeEm(r) * pt
		if bold {
			w *= 1.07
		}
		w += spacing
		m.word += w
		m.line += w
		if r == '-' || r == '/' {
			m.endWord() // a line may break after it
		}
	}
}

// contentWidths estimates the narrowest width n's content can take without
// breaking a word (min) and the width it takes with no line breaks other
// than forced ones (max), in twips, without slack. Each text is measured in
// the style of the element holding it. unsized reports content that cannot
// be measured from the markup: a picture without a width attribute, or a
// nested table.
func (c *conv) contentWidths(n *html.Node, st *style) (minW, maxW int, unsized bool) {
	var m lineMeasure
	visited := 0
	var walk func(*html.Node, *style)
	walk = func(p *html.Node, pst *style) {
		for ch := p.FirstChild; ch != nil && visited < maxMeasureNodes; ch = ch.NextSibling {
			visited++
			switch ch.Type {
			case html.TextNode:
				m.text(ch.Data, pst, c.rootPt)
			case html.ElementNode:
				if (ch.Namespace == "" && skipTags[ch.Data]) || hasAttr(ch, "hidden") {
					continue
				}
				switch {
				case ch.Data == "br":
					m.endLine()
				case ch.Data == "img" || ch.Data == "svg":
					if w, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(attr(ch, "width")), "px"), 64); err == nil && w > 0 && w < 1e5 {
						m.endWord()
						m.min = max(m.min, w*0.75)
						m.line += w * 0.75
					} else {
						unsized = true
					}
				case ch.Namespace != "":
				case ch.Data == "table":
					unsized = true
				default:
					cst, cbx := c.computeStyle(ch, pst)
					if cbx.display == "none" {
						continue
					}
					if isBlockLevel(ch.Data, cbx.display) {
						m.endLine()
						walk(ch, &cst)
						m.endLine()
					} else {
						walk(ch, &cst)
					}
				}
			}
		}
	}
	walk(n, st)
	m.endLine()
	return int(m.min * 20), int(m.max * 20), unsized
}
