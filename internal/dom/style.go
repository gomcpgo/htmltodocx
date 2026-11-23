package dom

import (
	"math"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
	"github.com/gomcpgo/htmltodocx/internal/model"
)

// White-space handling modes.
const (
	wsNormal  uint8 = iota // collapse runs of white space
	wsPre                  // keep everything
	wsPreLine              // collapse spaces, keep newlines
)

// style is the part of an element's computed style that its descendants
// inherit. It is copied on the way down the tree, so it holds no pointers
// to anything that changes after it is built.
type style struct {
	vars *varScope // custom properties in effect

	// backdrop is the colour drawn behind this element's content (RRGGBB),
	// from the nearest element with a background; "" is the white page.
	// Translucent colours are blended over it.
	backdrop string
	// colorA is the alpha of a translucent text colour set on this element,
	// blended over the backdrop once the element's background is known.
	// 0 means the colour is opaque.
	colorA float64

	bold, italic, underline, strike model.Tri
	smallCaps, caps                 model.Tri
	lower                           bool // text-transform: lowercase
	color                           string
	font                            string
	emPt                            float64 // the font size in effect, in points
	sizeSet                         bool    // emPt was set explicitly and must be written
	vert                            int8
	code                            bool   // inside <code> and friends
	runBg                           string // background of the enclosing inline box
	align                           string // "", "left", "center", "right" or "both"
	line                            int    // line height in 240ths
	textIndent                      int    // twips
	lang                            string
	rtl                             bool
	ws                              uint8
	listStyle                       model.NumFormat

	// What the paragraph or character style in effect already turns on.
	// Switching one of these off needs an explicit "off" in the output;
	// anywhere else, "off" is the default and is left unsaid.
	styleBold, styleItalic, inLink bool
}

// turnOff returns the state for a property that CSS has set back to normal.
func turnOff(cur model.Tri, styleOn bool) model.Tri {
	if cur == model.On || styleOn {
		return model.Off
	}
	return cur
}

// box is the part of the computed style that is not inherited.
type box struct {
	display string // "", "none", "block" or "inline"

	// Flex and grid layout. layout is "flex" or "grid" for a block-level
	// container; inlineBox marks inline-block, inline-flex and inline-grid,
	// which are separate boxes inside a line.
	layout      string
	inlineBox   bool
	flexColumn  bool   // flex-direction is column or column-reverse
	justify     string // justify-content, lower case
	gridCols    string // grid-template-columns as written
	colGap      string // column gap as written
	bg          string // RRGGBB, or "" for transparent
	bgSet       bool
	marginLeft  int // twips
	autoLeft    bool
	autoRight   bool
	breakBefore bool
	breakAfter  bool
	width       css.Length
	height      css.Length
	hasWidth    bool
	hasHeight   bool
	valign      string
	tableAlign  string

	// Border pieces, indexed by model.Top..model.Right. They are kept apart
	// because CSS can set width, style and colour in separate declarations.
	bStyle  [4]string
	bWidth  [4]float64 // points
	bColor  [4]string
	bSet    [4]bool // a border-style was given for this side, possibly "none"
	bHasWid [4]bool
}

// border returns the resolved border of one side.
func (b *box) border(side int) model.Border {
	if b.bStyle[side] == "" {
		return model.Border{}
	}
	w := 2.25 // CSS "medium"
	if b.bHasWid[side] {
		w = b.bWidth[side]
	}
	if w <= 0 {
		return model.Border{}
	}
	size := int(math.Round(w * 8))
	size = max(2, min(size, 96))
	return model.Border{Style: b.bStyle[side], Size: size, Color: b.bColor[side]}
}

func (b *box) hasBorder() bool {
	for i := 0; i < 4; i++ {
		if b.border(i).Style != "" {
			return true
		}
	}
	return false
}

// bordersOff reports whether CSS explicitly removed every border.
func (b *box) bordersOff() bool {
	for i := 0; i < 4; i++ {
		if !b.bSet[i] || b.bStyle[i] != "" {
			return false
		}
	}
	return true
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key && a.Namespace == "" {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key && a.Namespace == "" {
			return true
		}
	}
	return false
}

var headingScale = [6]float64{2, 1.5, 1.17, 1, 0.83, 0.67}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		return int(tag[1] - '0')
	}
	return 0
}

// computeStyle works out the style of element n from its parent's inherited
// style, in cascade order: browser defaults for the tag, presentational
// attributes, stylesheet rules, then the style attribute.
func (c *conv) computeStyle(n *html.Node, parent *style) (style, box) {
	st := *parent
	var bx box
	tag := n.Data

	// Browser defaults.
	switch tag {
	case "b", "strong":
		st.bold = model.On
	case "i", "em", "cite", "var", "dfn", "address":
		st.italic = model.On
	case "u", "ins":
		st.underline = model.On
	case "s", "strike", "del":
		st.strike = model.On
	case "sub":
		st.vert = model.VertSub
	case "sup":
		st.vert = model.VertSuper
	case "small":
		st.emPt *= 0.83
		st.sizeSet = true
	case "big":
		st.emPt *= 1.2
		st.sizeSet = true
	case "code", "kbd", "samp", "tt":
		st.code = true
		st.font, st.sizeSet, st.emPt = "", false, model.CodeSizePt*c.doc.Defaults.Scale()
	case "pre", "listing", "xmp", "plaintext":
		st.ws = wsPre
		st.font, st.sizeSet, st.emPt = "", false, model.CodeSizePt*c.doc.Defaults.Scale()
	case "mark":
		bx.bg, bx.bgSet = "FFFF00", true
	case "h1", "h2", "h3", "h4", "h5", "h6":
		// The heading styles carry weight and size. Only an explicitly set
		// size is scaled, the way a browser's "2em" would scale it.
		lvl := headingLevel(tag)
		st.bold, st.styleBold = model.Unset, true
		if lvl == 6 {
			st.italic, st.styleItalic = model.Unset, true
		}
		if st.sizeSet {
			st.emPt *= headingScale[lvl-1]
		} else {
			st.emPt = model.HeadingSizePt[lvl-1] * c.doc.Defaults.Scale()
		}
	case "th":
		st.bold = model.On
		st.align = "center"
	case "caption":
		st.align = "center"
		st.styleItalic = true
	case "figcaption":
		st.styleItalic = true
	case "dt":
		st.styleBold = true
	case "center":
		st.align = "center"
	case "a":
		if hasAttr(n, "href") {
			// The Hyperlink character style supplies colour and underline.
			st.color, st.underline, st.inLink = "", model.Unset, true
		}
	case "ul", "menu", "dir":
		st.listStyle = model.NumBullet
	case "ol":
		st.listStyle = model.NumDecimal
	case "table":
		st.textIndent = 0
	}

	// Presentational attributes.
	for _, a := range n.Attr {
		if a.Namespace != "" {
			if a.Key == "lang" && a.Val != "" {
				st.lang = a.Val
			}
			continue
		}
		switch a.Key {
		case "lang":
			if a.Val != "" {
				st.lang = a.Val
			}
		case "dir":
			switch strings.ToLower(a.Val) {
			case "rtl":
				st.rtl = true
			case "ltr":
				st.rtl = false
			}
		case "align":
			v := strings.ToLower(strings.TrimSpace(a.Val))
			if tag == "table" {
				if v == "center" || v == "right" {
					bx.tableAlign = v
				}
			} else if tag != "img" {
				if al, ok := alignValue(v, st.rtl); ok {
					st.align = al
				}
			}
		case "bgcolor":
			if tag != "body" {
				if col, ok := legacyColor(a.Val); ok {
					bx.bg, bx.bgSet = col, true
				}
			}
		case "valign":
			bx.valign = valignValue(a.Val)
		case "width", "height":
			switch tag {
			case "img", "table", "td", "th", "col", "colgroup":
				if l, ok := attrLength(a.Val); ok {
					if a.Key == "width" {
						bx.width, bx.hasWidth = l, true
					} else {
						bx.height, bx.hasHeight = l, true
					}
				}
			}
		case "type":
			if tag == "ol" || tag == "ul" {
				if f, ok := listTypeAttr(a.Val); ok {
					st.listStyle = f
				}
			}
		case "color", "face", "size":
			if tag == "font" {
				c.fontAttr(&st, parent, a.Key, a.Val)
			}
		}
	}

	if c.opt.CSS == CSSNone {
		return st, bx
	}

	// Author styles: normal declarations first, then !important ones; within
	// each, stylesheet rules before the style attribute.
	var inline []css.Decl
	if s := attr(n, "style"); s != "" {
		inline = css.ParseDeclarations(s)
	}
	c.matchBuf = c.matchBuf[:0]
	if c.sheet != nil {
		c.matchBuf = c.sheet.Match(n, c.matchBuf)
	}
	ordered := c.declBuf[:0]
	for pass := 0; pass < 2; pass++ {
		important := pass == 1
		for i := range c.matchBuf {
			for _, d := range c.matchBuf[i].Decls {
				if d.Important == important {
					ordered = append(ordered, d)
				}
			}
		}
		for _, d := range inline {
			if d.Important == important {
				ordered = append(ordered, d)
			}
		}
	}
	c.declBuf = ordered

	// Custom properties first, since this element's own declarations may use
	// them. A var() that cannot be resolved drops its declaration. The
	// specification says the property should become "unset" instead, but no
	// property supports unset yet (see applyDecl).
	st.vars = c.customProperties(parent.vars, ordered)
	for _, d := range ordered {
		if css.IsCustomProperty(d.Prop) {
			continue
		}
		if css.HasVar(d.Value) {
			v, ok := c.substitute(d.Value, st.vars.lookup)
			if !ok {
				continue
			}
			d.Value = v
		}
		c.applyDecl(&st, &bx, parent, d)
	}

	page := tag == "html" || tag == "body"
	if page {
		// Page background and page margins are not paragraph formatting.
		bx.bg, bx.bgSet, bx.marginLeft = "", false, 0
	}
	if bx.bgSet && bx.bg != "" {
		st.backdrop = bx.bg
	}
	if st.colorA > 0 {
		if col, ok := css.HexColor(st.color); ok {
			col.A = st.colorA
			st.color = col.Over(backdropColor(st.backdrop)).Hex()
		}
		st.colorA = 0
	}
	if page {
		if tag == "html" && st.sizeSet {
			c.rootPt = st.emPt
		}
		c.hoistDefaults(&st)
	}
	return st, bx
}

// minContrast is the WCAG contrast ratio below which a text colour is taken
// to be unreadable on its background. It is set low so only text that is
// practically invisible is affected: white on white is 1, #ddd on white 1.4.
// The usual cause is a background the converter cannot draw, such as a page
// background or an image, that the author put light text on.
const minContrast = 1.3

// backdropColor returns a backdrop as a colour; "" is the white page.
func backdropColor(hex string) css.Color {
	if col, ok := css.HexColor(hex); ok {
		return col
	}
	return css.White
}

// readableColor returns text colour fg, or "" (Word's automatic colour, which
// turns white on dark shading) when fg would be practically invisible on
// background bg. Both are RRGGBB; an empty bg is the white page.
func (c *conv) readableColor(fg, bg string) string {
	col, ok := css.HexColor(fg)
	if !ok {
		return fg
	}
	back := backdropColor(bg)
	if col.Contrast(back) >= minContrast {
		return fg
	}
	c.warn("color", "text colour #"+fg+" would be nearly invisible on background #"+back.Hex()+
		"; the automatic colour was used instead")
	return ""
}

// hoistDefaults moves the font, colour, size and line height set on <html>
// or <body> into the document defaults, so that they are stated once in the
// stylesheet instead of on every run and paragraph. The page background is
// not drawn, so a body colour meant for a dark page is dropped.
func (c *conv) hoistDefaults(st *style) {
	d := &c.doc.Defaults
	if st.font != "" {
		d.Font, st.font = st.font, ""
	}
	if st.color != "" {
		d.Color, st.color = c.readableColor(st.color, ""), ""
	}
	if st.sizeSet {
		st.emPt = math.Max(6, math.Min(st.emPt, 36))
		d.SizePt, st.sizeSet = st.emPt, false
	}
	if st.line != 0 {
		d.Line, st.line = st.line, 0
	}
}

func alignValue(v string, rtl bool) (string, bool) {
	switch v {
	case "left":
		return "left", true
	case "right":
		return "right", true
	case "center", "middle", "-webkit-center", "-moz-center":
		return "center", true
	case "justify":
		return "both", true
	case "start":
		if rtl {
			return "right", true
		}
		return "left", true
	case "end":
		if rtl {
			return "left", true
		}
		return "right", true
	}
	return "", false
}

func valignValue(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "top":
		return "top"
	case "middle", "center":
		return "center"
	case "bottom":
		return "bottom"
	}
	return ""
}

// legacyColor parses the value of a bgcolor or color attribute, which may be
// a hex colour without its "#".
func legacyColor(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if col, ok := css.ParseColor(v); ok && col.A > 0 {
		return col.Hex(), true
	}
	if len(v) == 6 {
		if col, ok := css.ParseColor("#" + v); ok {
			return col.Hex(), true
		}
	}
	return "", false
}

// attrLength parses a width or height attribute: pixels, or a percentage.
func attrLength(v string) (css.Length, bool) {
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
			return css.Length{}, false
		}
		return css.Length{V: f, Unit: "%"}, true
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.ToLower(v), "px"), 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return css.Length{}, false
	}
	return css.Length{V: f, Unit: "px"}, true
}

func listTypeAttr(v string) (model.NumFormat, bool) {
	switch strings.TrimSpace(v) {
	case "1":
		return model.NumDecimal, true
	case "a":
		return model.NumLowerLetter, true
	case "A":
		return model.NumUpperLetter, true
	case "i":
		return model.NumLowerRoman, true
	case "I":
		return model.NumUpperRoman, true
	}
	return listStyleValue(strings.ToLower(strings.TrimSpace(v)))
}

func listStyleValue(v string) (model.NumFormat, bool) {
	switch v {
	case "disc":
		return model.NumDisc, true
	case "circle":
		return model.NumCircle, true
	case "square":
		return model.NumSquare, true
	case "decimal", "decimal-leading-zero":
		return model.NumDecimal, true
	case "lower-alpha", "lower-latin":
		return model.NumLowerLetter, true
	case "upper-alpha", "upper-latin":
		return model.NumUpperLetter, true
	case "lower-roman":
		return model.NumLowerRoman, true
	case "upper-roman":
		return model.NumUpperRoman, true
	case "none":
		return model.NumNone, true
	}
	return "", false
}

var fontAttrSizes = [8]float64{0, 7.5, 10, 12, 13.5, 18, 24, 36}

func (c *conv) fontAttr(st *style, parent *style, key, val string) {
	val = strings.TrimSpace(val)
	switch key {
	case "color":
		if col, ok := legacyColor(val); ok {
			st.color = col
		}
	case "face":
		if f := fontFamily(val); f != "" {
			st.font = f
		}
	case "size":
		n, err := strconv.Atoi(strings.TrimPrefix(val, "+"))
		if err != nil {
			return
		}
		if strings.HasPrefix(val, "+") || strings.HasPrefix(val, "-") {
			n += 3
		}
		n = max(1, min(n, 7))
		// Size 3 is the browser default, so scale from the document base.
		st.emPt = fontAttrSizes[n] / 12 * c.rootPt
		st.sizeSet = n != 3 || parent.sizeSet
	}
}

// fontFamily picks one font from a CSS font-family list: the first named
// font, or a stand-in for the first generic family.
func fontFamily(v string) string {
	generic := ""
	for _, f := range css.SplitComma(v) {
		f = strings.TrimSpace(f)
		if n := len(f); n >= 2 && (f[0] == '"' || f[0] == '\'') && f[n-1] == f[0] {
			f = strings.TrimSpace(f[1 : n-1])
			if f != "" {
				return cleanFontName(f)
			}
			continue
		}
		switch strings.ToLower(f) {
		case "":
		case "serif", "ui-serif":
			if generic == "" {
				generic = "Times New Roman"
			}
		case "sans-serif", "ui-sans-serif":
			if generic == "" {
				generic = "Arial"
			}
		case "monospace", "ui-monospace":
			if generic == "" {
				generic = "Courier New"
			}
		case "system-ui", "-apple-system", "blinkmacsystemfont", "cursive", "fantasy",
			"ui-rounded", "emoji", "math", "fangsong", "inherit", "initial", "unset", "revert":
		default:
			return cleanFontName(f)
		}
	}
	return generic
}

func cleanFontName(f string) string {
	f = strings.Join(strings.Fields(f), " ")
	if len(f) > 64 {
		return ""
	}
	return f
}

// fontSize resolves a font-size value to points. parentPt is the parent
// element's font size, which em and percentages refer to.
func (c *conv) fontSize(v string, parentPt float64) (float64, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	// The document's base size stands in for the browser's 16px default, so
	// keywords and relative units scale from it. Absolute units are exact.
	var pt float64
	switch v {
	case "xx-small":
		pt = c.rootPt * 9 / 16
	case "x-small":
		pt = c.rootPt * 10 / 16
	case "small":
		pt = c.rootPt * 13 / 16
	case "medium":
		pt = c.rootPt
	case "large":
		pt = c.rootPt * 18 / 16
	case "x-large":
		pt = c.rootPt * 24 / 16
	case "xx-large":
		pt = c.rootPt * 2
	case "xxx-large":
		pt = c.rootPt * 3
	case "smaller":
		pt = parentPt * 0.83
	case "larger":
		pt = parentPt * 1.2
	default:
		l, ok := css.ParseLength(v)
		if !ok {
			return 0, false
		}
		if l.Unit == "%" {
			pt = parentPt * l.V / 100
		} else if pt, ok = l.Points(parentPt, c.rootPt); !ok {
			return 0, false
		}
	}
	if pt <= 0 {
		return 0, false
	}
	return math.Max(1, math.Min(pt, 400)), true
}

func isBold(v string) (model.Tri, bool) {
	switch v {
	case "bold", "bolder":
		return model.On, true
	case "normal", "lighter":
		return model.Off, true
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 1000 {
		if n >= 600 {
			return model.On, true
		}
		return model.Off, true
	}
	return model.Unset, false
}

// expand4 expands a 1 to 4 value shorthand into values indexed by
// model.Top..model.Right.
func expand4(vals []string) ([4]string, bool) {
	var out [4]string
	switch len(vals) {
	case 1:
		out = [4]string{vals[0], vals[0], vals[0], vals[0]}
	case 2:
		out[model.Top], out[model.Bottom] = vals[0], vals[0]
		out[model.Left], out[model.Right] = vals[1], vals[1]
	case 3:
		out[model.Top], out[model.Bottom] = vals[0], vals[2]
		out[model.Left], out[model.Right] = vals[1], vals[1]
	case 4:
		out[model.Top], out[model.Right], out[model.Bottom], out[model.Left] = vals[0], vals[1], vals[2], vals[3]
	default:
		return out, false
	}
	return out, true
}

func borderStyleValue(v string) (string, bool) {
	switch v {
	case "none", "hidden":
		return "", true
	case "solid", "inset", "outset", "groove", "ridge":
		return "single", true
	case "dashed":
		return "dashed", true
	case "dotted":
		return "dotted", true
	case "double":
		return "double", true
	}
	return "", false
}

func (c *conv) borderWidthValue(v string, emPt float64) (float64, bool) {
	switch v {
	case "thin":
		return 0.75, true
	case "medium":
		return 2.25, true
	case "thick":
		return 3.75, true
	}
	l, ok := css.ParseLength(v)
	if !ok {
		return 0, false
	}
	pt, ok := l.Points(emPt, c.rootPt)
	if !ok || pt < 0 {
		return 0, false
	}
	return pt, true
}

func sideIndex(name string) int {
	switch name {
	case "top":
		return model.Top
	case "left":
		return model.Left
	case "bottom":
		return model.Bottom
	case "right":
		return model.Right
	}
	return -1
}

// applyBorder handles every border-* property. It reports whether prop was
// one of them.
func (c *conv) applyBorder(st *style, bx *box, prop, val string) bool {
	if !strings.HasPrefix(prop, "border") {
		return false
	}
	val = strings.ToLower(val)
	rest := strings.TrimPrefix(strings.TrimPrefix(prop, "border"), "-")
	sides := []int{model.Top, model.Left, model.Bottom, model.Right}
	part := rest
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		if s := sideIndex(rest[:i]); s >= 0 {
			sides, part = []int{s}, rest[i+1:]
		}
	} else if s := sideIndex(rest); s >= 0 {
		sides, part = []int{s}, ""
	}

	switch part {
	case "": // border, border-top, ...: width, style and colour in any order
		toks := css.Fields(val)
		var bs string
		var bw float64
		var bc string
		hasStyle, hasWidth := false, false
		for _, t := range toks {
			if s, ok := borderStyleValue(t); ok {
				bs, hasStyle = s, true
			} else if w, ok := c.borderWidthValue(t, st.emPt); ok {
				bw, hasWidth = w, true
			} else if col, ok := css.ParseColor(t); ok && col.A > 0 {
				bc = col.OnWhite().Hex()
			}
		}
		for _, s := range sides {
			// A shorthand resets the pieces it does not mention.
			bx.bSet[s] = true
			bx.bStyle[s] = bs
			bx.bWidth[s], bx.bHasWid[s] = bw, hasWidth
			bx.bColor[s] = bc
			if !hasStyle {
				bx.bStyle[s] = ""
			}
		}
	case "style", "width", "color":
		vals := [4]string{val, val, val, val}
		if len(sides) == 4 {
			var ok bool
			if vals, ok = expand4(css.Fields(val)); !ok {
				return true
			}
		}
		for _, s := range sides {
			switch part {
			case "style":
				if v, ok := borderStyleValue(vals[s]); ok {
					bx.bStyle[s], bx.bSet[s] = v, true
				}
			case "width":
				if w, ok := c.borderWidthValue(vals[s], st.emPt); ok {
					bx.bWidth[s], bx.bHasWid[s] = w, true
				}
			case "color":
				if col, ok := css.ParseColor(vals[s]); ok && col.A > 0 {
					bx.bColor[s] = col.OnWhite().Hex()
				}
			}
		}
	}
	return true
}

const maxIndent = 4320 // three inches

func (c *conv) lengthTwips(v string, emPt float64) (int, bool) {
	l, ok := css.ParseLength(v)
	if !ok {
		return 0, false
	}
	pt, ok := l.Points(emPt, c.rootPt)
	if !ok {
		return 0, false
	}
	return int(math.Round(pt * 20)), true
}

// applyDecl applies one declaration. parent is the parent element's style,
// which relative font sizes refer to.
func (c *conv) applyDecl(st *style, bx *box, parent *style, d css.Decl) {
	val := d.Value
	lv := strings.ToLower(val)
	switch lv {
	case "inherit", "initial", "unset", "revert", "revert-layer":
		return
	}
	switch d.Prop {
	case "color":
		if col, ok := css.ParseColor(val); ok && col.A > 0 {
			st.color, st.colorA = col.Hex(), 0
			if col.A < 1 {
				st.colorA = col.A
			}
		}
	case "background-color", "background", "background-image":
		if lv == "none" {
			if d.Prop != "background-image" {
				bx.bg, bx.bgSet = "", true
			}
			return
		}
		if bg, ok := backgroundValue(val, d.Prop, backdropColor(parent.backdrop)); ok {
			bx.bg, bx.bgSet = bg, true
		}
	case "font-weight":
		if t, ok := isBold(lv); ok {
			if t == model.Off {
				t = turnOff(st.bold, st.styleBold)
			}
			st.bold = t
		}
	case "font-style":
		switch {
		case strings.HasPrefix(lv, "italic"), strings.HasPrefix(lv, "oblique"):
			st.italic = model.On
		case lv == "normal":
			st.italic = turnOff(st.italic, st.styleItalic)
		}
	case "font-family":
		if f := fontFamily(val); f != "" {
			st.font = f
		}
	case "font-size":
		if pt, ok := c.fontSize(val, parent.emPt); ok {
			st.emPt, st.sizeSet = pt, true
		}
	case "font":
		c.fontShorthand(st, parent, val)
	case "font-variant", "font-variant-caps":
		switch lv {
		case "small-caps", "all-small-caps":
			st.smallCaps = model.On
		case "normal":
			st.smallCaps = turnOff(st.smallCaps, false)
		}
	case "text-transform":
		switch lv {
		case "uppercase":
			st.caps, st.lower = model.On, false
		case "lowercase":
			st.caps, st.lower = turnOff(st.caps, false), true
		case "none":
			st.caps, st.lower = turnOff(st.caps, false), false
		}
	case "text-decoration", "text-decoration-line":
		for _, t := range css.Fields(lv) {
			switch t {
			case "underline":
				st.underline = model.On
			case "line-through":
				st.strike = model.On
			case "none":
				st.underline = turnOff(st.underline, st.inLink)
				st.strike = turnOff(st.strike, false)
			}
		}
	case "text-align":
		if al, ok := alignValue(lv, st.rtl); ok {
			st.align = al
		}
	case "vertical-align":
		switch lv {
		case "sub":
			st.vert = model.VertSub
		case "super":
			st.vert = model.VertSuper
		case "baseline":
			st.vert = model.VertNone
		default:
			if v := valignValue(lv); v != "" {
				bx.valign = v
			}
		}
	case "line-height":
		if lv == "normal" {
			st.line = 0
			return
		}
		l, ok := css.ParseLength(lv)
		if !ok {
			return
		}
		var mult float64
		switch l.Unit {
		case "":
			mult = l.V
		case "%":
			mult = l.V / 100
		default:
			pt, ok := l.Points(st.emPt, c.rootPt)
			if !ok || st.emPt <= 0 {
				return
			}
			mult = pt / st.emPt
		}
		if mult > 0 {
			st.line = int(math.Round(240 * math.Max(0.5, math.Min(mult, 5))))
		}
	case "text-indent":
		if tw, ok := c.lengthTwips(lv, st.emPt); ok {
			st.textIndent = max(-maxIndent, min(tw, maxIndent))
		}
	case "margin-left", "margin-inline-start":
		c.applyMarginLeft(st, bx, lv)
	case "margin-right", "margin-inline-end":
		bx.autoRight = lv == "auto"
	case "margin":
		if v, ok := expand4(css.Fields(lv)); ok {
			c.applyMarginLeft(st, bx, v[model.Left])
			bx.autoRight = v[model.Right] == "auto"
		}
	case "white-space":
		switch lv {
		case "pre", "pre-wrap", "break-spaces":
			st.ws = wsPre
		case "pre-line":
			st.ws = wsPreLine
		case "normal", "nowrap":
			st.ws = wsNormal
		}
	case "display":
		switch lv {
		case "none":
			bx.display = "none"
		case "inline", "inline-block", "inline-flex", "inline-grid", "contents":
			bx.display = "inline"
			bx.layout, bx.inlineBox = "", lv != "inline" && lv != "contents"
		case "block", "flex", "grid", "flow-root", "list-item":
			bx.display = "block"
			bx.layout, bx.inlineBox = "", false
			if lv == "flex" || lv == "grid" {
				bx.layout = lv
			}
		}
	case "flex-direction":
		bx.flexColumn = strings.HasPrefix(lv, "column")
	case "flex-flow":
		for _, t := range css.Fields(lv) {
			if strings.HasPrefix(t, "row") || strings.HasPrefix(t, "column") {
				bx.flexColumn = strings.HasPrefix(t, "column")
			}
		}
	case "justify-content":
		bx.justify = lv
	case "grid-template-columns":
		bx.gridCols = lv
	case "gap", "grid-gap":
		// "row column", or one value for both.
		if f := css.Fields(lv); len(f) > 0 {
			bx.colGap = f[len(f)-1]
		}
	case "column-gap", "grid-column-gap":
		bx.colGap = lv
	case "visibility":
		if lv == "hidden" || lv == "collapse" {
			bx.display = "none"
		}
	case "direction":
		switch lv {
		case "rtl":
			st.rtl = true
		case "ltr":
			st.rtl = false
		}
	case "list-style-type", "list-style":
		for _, t := range css.Fields(lv) {
			if f, ok := listStyleValue(t); ok {
				st.listStyle = f
				break
			}
		}
	case "page-break-before", "break-before":
		bx.breakBefore = isPageBreak(lv)
	case "page-break-after", "break-after":
		bx.breakAfter = isPageBreak(lv)
	case "width":
		if l, ok := css.ParseLength(lv); ok && l.V > 0 && l.Unit != "" {
			bx.width, bx.hasWidth = l, true
		} else if lv == "auto" {
			bx.hasWidth = false
		}
	case "height":
		if l, ok := css.ParseLength(lv); ok && l.V > 0 && l.Unit != "" {
			bx.height, bx.hasHeight = l, true
		} else if lv == "auto" {
			bx.hasHeight = false
		}
	default:
		c.applyBorder(st, bx, d.Prop, val)
	}
}

func isPageBreak(v string) bool {
	switch v {
	case "always", "page", "left", "right", "recto", "verso":
		return true
	}
	return false
}

func (c *conv) applyMarginLeft(st *style, bx *box, v string) {
	if v == "auto" {
		bx.autoLeft, bx.marginLeft = true, 0
		return
	}
	bx.autoLeft = false
	if tw, ok := c.lengthTwips(v, st.emPt); ok {
		bx.marginLeft = max(0, min(tw, maxIndent))
	}
}

// fontShorthand handles the common shapes of the font shorthand:
// [style] [variant] [weight] size[/line-height] family.
func (c *conv) fontShorthand(st *style, parent *style, val string) {
	toks := css.Fields(val)
	for i, t := range toks {
		lt := strings.ToLower(t)
		switch lt {
		case "italic", "oblique":
			st.italic = model.On
			continue
		case "small-caps":
			st.smallCaps = model.On
			continue
		case "normal":
			continue
		}
		if b, ok := isBold(lt); ok {
			if b == model.Off {
				b = turnOff(st.bold, st.styleBold)
			}
			st.bold = b
			continue
		}
		size, lh, _ := strings.Cut(lt, "/")
		pt, ok := c.fontSize(size, parent.emPt)
		if !ok {
			return // not a shape we understand (for example "caption")
		}
		st.emPt, st.sizeSet = pt, true
		if lh != "" {
			c.applyDecl(st, &box{}, parent, css.Decl{Prop: "line-height", Value: lh})
		}
		if f := fontFamily(strings.Join(toks[i+1:], " ")); f != "" {
			st.font = f
		}
		return
	}
}

// backgroundValue works out the single colour that stands in for a
// background-color, background or background-image value, blended over the
// backdrop behind the element. Images cannot be drawn as shading and are
// skipped; a gradient is reduced to its average colour, and since background
// images are painted over the background colour, a gradient layer wins over
// a colour in the same value. ok is false when the value says nothing usable.
// An empty result means transparent.
func backgroundValue(val, prop string, backdrop css.Color) (bg string, ok bool) {
	var plain, grad css.Color
	var hasPlain, hasGrad bool
	for _, layer := range css.SplitComma(val) {
		for _, t := range css.Fields(layer) {
			if !hasGrad {
				if col, isGrad := css.GradientColor(t); isGrad {
					grad, hasGrad = col, true
					continue
				}
			}
			if prop != "background-image" && !hasPlain {
				if col, isCol := css.ParseColor(t); isCol {
					plain, hasPlain = col, true
				}
			}
		}
	}
	if !hasPlain && !hasGrad {
		return "", false
	}
	base := backdrop
	if hasPlain {
		if plain.A == 0 && !hasGrad {
			return "", true
		}
		base = plain.Over(base)
	}
	if hasGrad {
		base = grad.Over(base)
	}
	return base.Hex(), true
}
