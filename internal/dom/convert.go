// Package dom turns a parsed HTML tree into the intermediate document model.
//
// It does the browser-like work: deciding what is a block and what is
// inline, collapsing white space, resolving styles, numbering lists and
// laying out table grids.
package dom

import (
	"bytes"
	"context"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
	"github.com/gomcpgo/htmltodocx/internal/model"
)

// CSS modes.
const (
	CSSAll    = iota // <style>, linked stylesheets and style attributes
	CSSInline        // style attributes only
	CSSNone          // no CSS at all
)

// Options configures a conversion.
type Options struct {
	CSS        int
	BaseURL    *url.URL
	Load       func(ctx context.Context, url string) ([]byte, error)
	Warn       func(code, message string)
	TextWidth  int // width of the text area, twips
	TextHeight int // height of the text area, twips
}

// maxDepth bounds recursion. The HTML parser already refuses documents nested
// more than 512 elements deep, so this is a second line of defence; content
// nested deeper is reduced to its text.
const maxDepth = 1024

type linkRef struct {
	url     string
	anchor  string
	tooltip string
}

type pendingNum struct {
	id       int
	level    int
	explicit bool // the list is indented, so the level's own indent is wrong
}

const (
	itemText = iota
	itemBreak
	itemImage
	itemBookmark
	itemTab
)

type item struct {
	kind int
	text []byte // text of a run, or the name of a bookmark
	pre  bool   // preformatted text: do not trim
	rp   model.RunProps
	link *linkRef
	img  *model.Image
}

type paraBuilder struct {
	props     model.ParaProps
	items     []item
	lastSpace bool // the next collapsible space would be redundant
	// spaceAfterBox is set when an inline-block box has just ended: if the
	// next text starts with a letter or digit, a space keeps the two apart.
	spaceAfterBox bool
	force         bool // keep the paragraph even if it ends up empty
}

// bctx is the context an element passes to its children. It is passed by
// value so each subtree gets its own copy.
type bctx struct {
	st        style
	paraStyle string
	align     string
	line      int
	firstLine int
	bidi      bool
	indent    int
	// indentRight is the right indent of paragraphs started here; avail is
	// reduced by the same amount.
	indentRight int
	shade       string
	border      *[4]model.Border // borders of the enclosing box, or nil
	tabs        []model.TabStop  // tab stops for paragraphs started here
	keepNext    bool
	link        *linkRef
	listLevel   int // -1 outside lists
	listBase    int
	avail       int // width available to content, twips
	depth       int
}

type conv struct {
	ctx      context.Context
	opt      Options
	sheet    *css.Stylesheet
	matchBuf []css.Matched
	declBuf  []css.Decl // an element's declarations in cascade order
	varBytes int        // bytes produced by var() substitution so far
	rootPt   float64
	doc      *model.Document

	sink             *[]model.Block
	para             *paraBuilder
	pendingNum       *pendingNum
	pendingBookmarks []string
	pendingBreak     bool

	ids        map[string]bool   // every id and anchor name in the document
	refs       map[string]bool   // fragments that some link points at
	bookmarks  map[string]string // id to bookmark name
	usedNames  map[string]bool
	bookmarked map[string]bool
	nameSeq    map[string]int // next suffix to try for a bookmark base name
	bulletNums map[model.NumFormat]int
	images     map[string]*imageEntry
	imageData  map[[32]byte]*model.ImageData
	warned     map[string]bool
	base       *url.URL
	elements   int
	stopped    bool
}

// Convert builds the document model for the HTML tree under root.
func Convert(ctx context.Context, root *html.Node, opt Options) (*model.Document, error) {
	c := &conv{
		ctx:        ctx,
		opt:        opt,
		rootPt:     model.BaseSizePt,
		doc:        &model.Document{},
		ids:        map[string]bool{},
		refs:       map[string]bool{},
		bookmarks:  map[string]string{},
		usedNames:  map[string]bool{},
		nameSeq:    map[string]int{},
		bookmarked: map[string]bool{},
		bulletNums: map[model.NumFormat]int{},
		images:     map[string]*imageEntry{},
		imageData:  map[[32]byte]*model.ImageData{},
		warned:     map[string]bool{},
		base:       opt.BaseURL,
	}
	c.sink = &c.doc.Blocks
	c.prepass(root)

	bc := bctx{
		st:        style{emPt: model.BaseSizePt},
		listLevel: -1,
		avail:     opt.TextWidth,
	}
	c.children(root, bc)
	c.flushPara()
	if len(c.pendingBookmarks) > 0 {
		c.ensurePara(&bc)
		c.para.force = true
		c.flushPara()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.doc, nil
}

func (c *conv) warn(code, msg string) {
	if c.opt.Warn == nil {
		return
	}
	key := code + "\x00" + msg
	if c.warned[key] {
		return
	}
	c.warned[key] = true
	c.opt.Warn(code, msg)
}

// walkTree visits every node under root without recursion. fn returns false
// to skip a node's children.
func walkTree(root *html.Node, fn func(*html.Node) bool) {
	n := root
	for n != nil {
		if fn(n) && n.FirstChild != nil {
			n = n.FirstChild
			continue
		}
		for n != root && n.NextSibling == nil {
			n = n.Parent
			if n == nil {
				return
			}
		}
		if n == root {
			return
		}
		n = n.NextSibling
	}
}

func textContent(root *html.Node) string {
	var b strings.Builder
	walkTree(root, func(n *html.Node) bool {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		return true
	})
	return b.String()
}

func fragmentID(href string) string {
	frag := href[1:]
	if u, err := url.PathUnescape(frag); err == nil {
		frag = u
	}
	return frag
}

// prepass collects what the main walk needs to know up front: metadata,
// stylesheets, every id, and which ids are link targets.
func (c *conv) prepass(root *html.Node) {
	var sheets []string
	walkTree(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		if id := attr(n, "id"); id != "" {
			c.ids[id] = true
		}
		switch n.Data {
		case "html":
			c.doc.Meta.Lang = strings.TrimSpace(attr(n, "lang"))
		case "title":
			if n.Namespace == "" && c.doc.Meta.Title == "" {
				c.doc.Meta.Title = strings.Join(strings.Fields(textContent(n)), " ")
			}
			return false
		case "base":
			if c.base == nil {
				if u, err := url.Parse(strings.TrimSpace(attr(n, "href"))); err == nil && u.IsAbs() {
					c.base = u
				}
			}
		case "meta":
			content := strings.TrimSpace(attr(n, "content"))
			switch strings.ToLower(attr(n, "name")) {
			case "author":
				c.doc.Meta.Author = content
			case "description":
				c.doc.Meta.Description = content
			case "keywords":
				c.doc.Meta.Keywords = content
			case "subject":
				c.doc.Meta.Subject = content
			}
		case "a":
			if name := attr(n, "name"); name != "" {
				c.ids[name] = true
			}
			if href := strings.TrimSpace(attr(n, "href")); len(href) > 1 && href[0] == '#' {
				c.refs[fragmentID(href)] = true
			}
		case "style":
			if c.opt.CSS == CSSAll && css.MediaMatches(attr(n, "media")) {
				sheets = append(sheets, textContent(n))
			}
			return false
		case "link":
			if c.opt.CSS == CSSAll && c.opt.Load != nil && isStylesheetLink(n) {
				if text, ok := c.loadStylesheet(attr(n, "href")); ok {
					sheets = append(sheets, text)
				}
			}
		case "script", "template":
			return false
		}
		return true
	})
	if len(sheets) > 0 {
		c.sheet = css.NewStylesheet()
		for _, s := range sheets {
			c.sheet.Add(s)
		}
	}
}

func isStylesheetLink(n *html.Node) bool {
	for _, rel := range strings.Fields(strings.ToLower(attr(n, "rel"))) {
		if rel == "stylesheet" {
			return css.MediaMatches(attr(n, "media")) && strings.TrimSpace(attr(n, "href")) != ""
		}
	}
	return false
}

func (c *conv) loadStylesheet(href string) (string, bool) {
	target, ok := c.resolve(href)
	if !ok {
		return "", false
	}
	data, err := c.opt.Load(c.ctx, target)
	if err != nil {
		c.warn("stylesheet", "could not load stylesheet "+shorten(href)+": "+err.Error())
		return "", false
	}
	return string(data), true
}

// resolve makes a resource reference absolute when a base URL is known.
func (c *conv) resolve(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if c.base == nil {
		return ref, true
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "", false
	}
	return c.base.ResolveReference(u).String(), true
}

func shorten(s string) string {
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

func (c *conv) children(n *html.Node, bc bctx) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch ch.Type {
		case html.TextNode:
			c.text(ch.Data, &bc)
		case html.ElementNode:
			c.element(ch, bc)
		}
	}
}

// blockTags are the elements that start a new block by default.
var blockTags = map[string]bool{
	"html": true, "body": true, "address": true, "article": true, "aside": true,
	"blockquote": true, "center": true, "dd": true, "details": true, "dialog": true,
	"div": true, "dl": true, "dt": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "header": true, "hgroup": true,
	"legend": true, "li": true, "main": true, "nav": true, "p": true,
	"section": true, "summary": true, "search": true, "caption": true,
	"tr": true, "td": true, "th": true, "thead": true, "tbody": true, "tfoot": true,
	"optgroup": true, "option": true, "noscript": true,
}

// skipTags are elements with nothing to show in a document.
var skipTags = map[string]bool{
	"head": true, "script": true, "style": true, "template": true, "title": true,
	"meta": true, "link": true, "base": true, "iframe": true, "object": true,
	"embed": true, "video": true, "audio": true, "canvas": true, "map": true,
	"area": true, "datalist": true, "select": true, "textarea": true, "source": true,
	"track": true, "param": true, "col": true, "colgroup": true, "noframes": true,
	"frameset": true, "frame": true, "wbr": true, "progress": true, "meter": true,
}

func (c *conv) element(n *html.Node, bc bctx) {
	if c.stopped {
		return
	}
	c.elements++
	if c.elements&0xff == 0 && c.ctx.Err() != nil {
		c.stopped = true
		return
	}
	tag := n.Data
	if n.Namespace != "" {
		c.foreign(n, bc)
		return
	}
	if skipTags[tag] || hasAttr(n, "hidden") {
		return
	}
	if tag == "dialog" && !hasAttr(n, "open") {
		return
	}
	bc.depth++
	if bc.depth > maxDepth {
		c.warn("depth", "content nested too deeply was reduced to plain text")
		c.text(textContent(n), &bc)
		return
	}

	st, bx := c.computeStyle(n, &bc.st)
	if bx.display == "none" {
		return
	}

	switch tag {
	case "br":
		c.lineBreak(&bc)
	case "img":
		c.image(n, bc, st, bx)
	case "hr":
		c.flushPara()
		if bx.breakBefore {
			c.pendingBreak = true
		}
		*c.sink = append(*c.sink, &model.HorizontalRule{IndentLeft: bc.indent})
	case "table":
		c.table(n, bc, st, bx)
	case "ul", "ol", "menu", "dir":
		c.list(n, bc, st, bx)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.block(n, bc, st, bx, blockOpts{style: model.StyleHeadingPrefix + tag[1:], bookmark: true})
	case "pre", "listing", "xmp", "plaintext":
		c.block(n, bc, st, bx, blockOpts{style: model.StyleSourceCode, pre: true})
	case "blockquote":
		c.block(n, bc, st, bx, blockOpts{style: model.StyleQuote, indent: 720})
	case "dt":
		c.block(n, bc, st, bx, blockOpts{style: model.StyleDefinitionTerm, keepNext: true})
	case "dd":
		c.block(n, bc, st, bx, blockOpts{indent: 720})
	case "figure":
		c.block(n, bc, st, bx, blockOpts{keepNext: true})
	case "figcaption", "caption":
		bc.keepNext = false
		c.block(n, bc, st, bx, blockOpts{style: model.StyleCaption})
	case "a":
		if name := attr(n, "name"); name != "" && c.refs[name] {
			c.bookmark(name)
		}
		if ref := c.linkTarget(n); ref != nil {
			bc.link = ref
		}
		c.generic(n, bc, st, bx, false)
	case "q":
		c.generic(n, bc, st, bx, true)
	case "input":
		if strings.EqualFold(attr(n, "type"), "checkbox") {
			bc.st = st
			box := "☐ "
			if hasAttr(n, "checked") {
				box = "☒ "
			}
			c.ensurePara(&bc)
			c.addText(box, &bc, true)
			c.para.lastSpace = true
		}
	case "button":
		c.generic(n, bc, st, bx, false)
	default:
		c.generic(n, bc, st, bx, false)
	}
}

// generic handles an element with no special behaviour, as a block or inline
// box according to its tag and display value.
func (c *conv) generic(n *html.Node, bc bctx, st style, bx box, quote bool) {
	block := blockTags[n.Data]
	switch bx.display {
	case "block":
		block = true
	case "inline":
		if n.Data != "li" && n.Data != "body" && n.Data != "html" {
			block = false
		}
	}
	if block {
		c.block(n, bc, st, bx, blockOpts{})
		return
	}
	if bx.bgSet {
		st.runBg = bx.bg
	}
	if bx.inlineBox {
		c.boxSpaceBefore(&bc)
	}
	bc.st = st
	c.maybeBookmark(n, false)
	if quote {
		c.text("“", &bc)
	}
	c.children(n, bc)
	if quote {
		c.text("”", &bc)
	}
	if bx.inlineBox && c.para != nil {
		c.para.spaceAfterBox = true
	}
}

type blockOpts struct {
	style    string
	indent   int
	keepNext bool
	bookmark bool // always bookmark the id, even if nothing links to it
	pre      bool
}

// enterBlock updates the context for the inside of a block element.
func (c *conv) enterBlock(bc *bctx, st style, bx box) {
	bc.st = st
	bc.align, bc.line, bc.firstLine, bc.bidi = st.align, st.line, st.textIndent, st.rtl
	if bx.marginLeft > 0 {
		bc.indent = min(bc.indent+bx.marginLeft, c.opt.TextWidth*2/3)
	}
	shaded := false
	if bx.bgSet && !(bx.bg == "FFFFFF" && bc.shade == "") {
		bc.shade = bx.bg
		shaded = bx.bg != ""
	}
	c.boxBorders(bc, &bx, shaded)
}

// maxBorderSpace is the largest distance between a paragraph border and its
// text that Word accepts, in points.
const maxBorderSpace = 31

// boxBorders gives the paragraphs inside a block box the box's borders and
// padding. Paragraph borders are drawn outside the indents, so the indents
// grow by each border's width and spacing and the border lands where the box
// starts. Rounded corners and shadows cannot be drawn and are ignored.
func (c *conv) boxBorders(bc *bctx, bx *box, shaded bool) {
	var bs [4]model.Border
	drawn := false
	for side := range bs {
		b := bx.border(side)
		pad := bx.pad[side]
		if b.Style == "" {
			if !shaded || pad <= 0 {
				continue
			}
			// Shading fills a paragraph up to its borders. Padding on a side
			// without one becomes a hairline in the background colour, so
			// the shading covers the padding as it does in a browser.
			b = model.Border{Style: "single", Size: 2, Color: bc.shade}
		}
		b.Space = min(int(math.Round(pad)), maxBorderSpace)
		bs[side] = b
		drawn = true
	}
	if !drawn {
		return
	}
	inset := func(side int) int {
		if bs[side].Style == "" {
			return 0
		}
		return int(math.Round((float64(bs[side].Size)/8 + float64(bs[side].Space)) * 20))
	}
	// Keep at least an inch for the text, however deep boxes are nested.
	bc.indent = min(bc.indent+inset(model.Left), c.opt.TextWidth*2/3, max(bc.indent, bc.avail-1440))
	right := min(inset(model.Right), max(0, bc.avail-bc.indent-1440))
	bc.indentRight += right
	bc.avail -= right
	bc.border = &bs
}

// separateBoxes keeps a box from merging with the box before it. Word draws
// adjacent paragraphs with identical borders as one box, which would join two
// cards, or turn list items that each have a bottom rule into a list with one
// rule under the last item. A one-unit change of colour is invisible but
// makes the borders differ. It changes the box's shared borders, so all of
// its paragraphs stay alike.
func separateBoxes(prev model.Block, cur *[4]model.Border) {
	p, ok := prev.(*model.Paragraph)
	if !ok || p.Props.Borders == nil || p.Props.Borders == cur || *p.Props.Borders != *cur {
		return
	}
	for i := range cur {
		b := &cur[i]
		if b.Style == "" {
			continue
		}
		if b.Color == "" {
			b.Color = "000000" // the automatic colour on a white page
			continue
		}
		// Flip the lowest bit of the blue channel.
		n := len(b.Color) - 1
		v, err := strconv.ParseUint(b.Color[n:], 16, 8)
		if err != nil {
			continue
		}
		b.Color = b.Color[:n] + strings.ToUpper(strconv.FormatUint(v^1, 16))
	}
}

func (c *conv) block(n *html.Node, bc bctx, st style, bx box, o blockOpts) {
	c.flushPara()
	if bx.breakBefore {
		c.pendingBreak = true
	}
	c.enterBlock(&bc, st, bx)
	if o.style != "" {
		bc.paraStyle = o.style
	}
	if o.indent > 0 {
		bc.indent = min(bc.indent+o.indent, c.opt.TextWidth*2/3)
	}
	if o.keepNext {
		bc.keepNext = true
	}
	c.maybeBookmark(n, o.bookmark)

	switch {
	case o.pre && st.ws == wsPre:
		c.preformatted(n, bc)
	case bx.layout != "":
		c.layoutChildren(n, bc, bx)
	default:
		c.children(n, bc)
	}
	c.flushPara()
	if bx.breakAfter {
		c.pendingBreak = true
	}
}

// preformatted handles the body of a <pre>. The parser has already dropped
// the newline right after the start tag; a final newline before the end tag
// is dropped here so the block does not end with an empty line.
func (c *conv) preformatted(n *html.Node, bc bctx) {
	c.children(n, bc)
	p := c.para
	if p == nil {
		return
	}
	for i := len(p.items) - 1; i >= 0; i-- {
		it := &p.items[i]
		if it.kind == itemBookmark {
			continue
		}
		if n := len(it.text); it.kind == itemText && n > 0 && it.text[n-1] == '\n' {
			it.text = it.text[:n-1]
			if len(it.text) == 0 {
				p.items = append(p.items[:i], p.items[i+1:]...)
				p.force = true
			}
		}
		break
	}
}

func (c *conv) foreign(n *html.Node, bc bctx) {
	switch n.Data {
	case "svg":
		c.warn("svg", "inline SVG is not supported and was skipped")
	case "math":
		c.warn("math", "MathML is not supported; only its text was kept")
		c.text(strings.Join(strings.Fields(textContent(n)), " "), &bc)
	}
}

// ---- paragraphs ----

func (c *conv) ensurePara(bc *bctx) {
	if c.para != nil {
		return
	}
	p := &paraBuilder{lastSpace: true}
	p.props = model.ParaProps{
		Style:       bc.paraStyle,
		Align:       bc.align,
		IndentLeft:  bc.indent,
		IndentRight: bc.indentRight,
		FirstLine:   bc.firstLine,
		Line:        bc.line,
		Shade:       bc.shade,
		Borders:     bc.border,
		Tabs:        bc.tabs,
		KeepNext:    bc.keepNext,
		Bidi:        bc.bidi,
	}
	if p.props.Align == "left" && !bc.bidi {
		p.props.Align = "" // every built-in style is already left aligned
	}
	if c.pendingBreak {
		p.props.PageBreakBefore = true
		c.pendingBreak = false
	}
	if pn := c.pendingNum; pn != nil {
		p.props.Num = &model.NumRef{ID: pn.id, Level: pn.level}
		if pn.explicit {
			p.props.FirstLine = -360
		} else {
			p.props.IndentLeft, p.props.FirstLine = 0, 0
		}
		c.pendingNum = nil
	}
	for _, name := range c.pendingBookmarks {
		p.items = append(p.items, item{kind: itemBookmark, text: []byte(name)})
	}
	c.pendingBookmarks = c.pendingBookmarks[:0]
	c.para = p
}

func (c *conv) runProps(bc *bctx) model.RunProps {
	st := &bc.st
	rp := model.RunProps{
		Font:      st.font,
		Bold:      st.bold,
		Italic:    st.italic,
		Underline: st.underline,
		Strike:    st.strike,
		SmallCaps: st.smallCaps,
		Caps:      st.caps,
		Color:     st.color,
		Shade:     st.runBg,
		Vert:      st.vert,
		RTL:       st.rtl,
	}
	if rp.Color != "" {
		rp.Color = c.readableColor(rp.Color, st.backdrop)
	}
	if st.sizeSet {
		rp.SizeHalfPt = int(math.Round(st.emPt * 2))
	}
	if st.code && bc.paraStyle != model.StyleSourceCode {
		rp.Style = model.StyleVerbatimChar
	}
	if st.lang != c.doc.Meta.Lang {
		rp.Lang = st.lang
	}
	return rp
}

func isCollapsible(b byte) bool {
	return b == ' ' || b == '\n' || b == '\t' || b == '\r' || b == '\f'
}

// text adds a text node's content to the current paragraph, applying the
// white-space rules in effect.
func (c *conv) text(s string, bc *bctx) {
	if s == "" {
		return
	}
	if bc.st.lower {
		s = strings.ToLower(s)
	}
	if bc.st.ws == wsPre {
		if strings.IndexByte(s, '\r') >= 0 {
			s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
		}
		c.ensurePara(bc)
		c.addText(s, bc, true)
		c.para.lastSpace = false
		return
	}

	if c.para != nil && c.para.spaceAfterBox {
		// White space in s separates the box by itself; only a word that
		// would touch the box needs a space.
		c.para.spaceAfterBox = false
		if r, ok := firstVisibleRune(s); ok && !c.para.lastSpace && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			c.addText(" ", bc, false)
			c.para.lastSpace = true
		}
	}
	keepNL := bc.st.ws == wsPreLine
	last := c.para == nil || c.para.lastSpace
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !isCollapsible(b) {
			buf = append(buf, b)
			last = false
			continue
		}
		if keepNL && b == '\n' {
			if n := len(buf); n > 0 && buf[n-1] == ' ' {
				buf = buf[:n-1]
			}
			buf = append(buf, '\n')
			last = true
			continue
		}
		if !last {
			buf = append(buf, ' ')
			last = true
		}
	}
	if len(buf) == 0 {
		return
	}
	c.ensurePara(bc)
	c.addText(string(buf), bc, false)
	c.para.lastSpace = last
}

func (c *conv) addText(s string, bc *bctx, pre bool) {
	p := c.para
	rp := c.runProps(bc)
	if n := len(p.items); n > 0 {
		last := &p.items[n-1]
		if last.kind == itemText && last.pre == pre && last.link == bc.link && last.rp == rp {
			// Appending to a byte slice keeps a paragraph built from many
			// small text nodes linear; string concatenation would not be.
			last.text = append(last.text, s...)
			return
		}
	}
	p.items = append(p.items, item{kind: itemText, text: []byte(s), pre: pre, rp: rp, link: bc.link})
}

// trimTrailing removes collapsible space from the end of the paragraph.
func (p *paraBuilder) trimTrailing() {
	for i := len(p.items) - 1; i >= 0; i-- {
		it := &p.items[i]
		if it.kind == itemBookmark {
			continue
		}
		if it.kind != itemText || it.pre {
			return
		}
		it.text = bytes.TrimRight(it.text, " ")
		if len(it.text) != 0 {
			return
		}
		p.items = append(p.items[:i], p.items[i+1:]...)
	}
}

func (c *conv) lineBreak(bc *bctx) {
	c.ensurePara(bc)
	c.para.trimTrailing()
	c.para.items = append(c.para.items, item{kind: itemBreak, link: bc.link})
	c.para.lastSpace = true
}

func (c *conv) flushPara() {
	p := c.para
	if p == nil {
		return
	}
	c.para = nil
	p.trimTrailing()
	// A line break at the very end of a block adds no line in a browser,
	// unless it is all the block contains.
	for i := len(p.items) - 1; i >= 0; i-- {
		if p.items[i].kind == itemBookmark {
			continue
		}
		if p.items[i].kind == itemBreak {
			p.items = append(p.items[:i], p.items[i+1:]...)
			p.force = true
			p.trimTrailing()
		}
		break
	}

	para := &model.Paragraph{Props: p.props}
	var cur *model.Link
	var curRef *linkRef
	content := false
	for i := range p.items {
		it := &p.items[i]
		var in model.Inline
		switch it.kind {
		case itemText:
			in = &model.Text{Text: string(it.text), Props: it.rp}
			content = true
		case itemBreak:
			in = &model.LineBreak{}
			content = true
		case itemTab:
			in = &model.Text{Text: "\t", Props: it.rp}
			content = true
		case itemImage:
			in = it.img
			content = true
		case itemBookmark:
			in = &model.Bookmark{Name: string(it.text)}
		}
		if it.kind != itemBookmark && it.link != curRef {
			cur, curRef = nil, it.link
			if it.link != nil {
				cur = &model.Link{URL: it.link.url, Anchor: it.link.anchor, Tooltip: it.link.tooltip}
				para.Inlines = append(para.Inlines, cur)
			}
		}
		if cur != nil {
			cur.Inlines = append(cur.Inlines, in)
		} else {
			para.Inlines = append(para.Inlines, in)
		}
	}
	if !content && !p.force {
		// Nothing to show: hand what the paragraph was carrying to the next.
		for i := range p.items {
			c.pendingBookmarks = append(c.pendingBookmarks, string(p.items[i].text))
		}
		if p.props.PageBreakBefore {
			c.pendingBreak = true
		}
		return
	}
	if b := para.Props.Borders; b != nil && len(*c.sink) > 0 {
		separateBoxes((*c.sink)[len(*c.sink)-1], b)
	}
	*c.sink = append(*c.sink, para)
}

// forcePendingNum writes an empty list item when the item's marker has not
// been used by any paragraph: an empty <li>, or one that starts with a
// nested list or a table.
func (c *conv) forcePendingNum(bc *bctx) {
	if c.pendingNum == nil {
		return
	}
	c.ensurePara(bc)
	c.para.force = true
	c.flushPara()
}

// ---- bookmarks and links ----

// bookmarkName maps an HTML id to a name Word accepts: at most 40
// characters, starting with a letter, with letters, digits and underscores
// only, and unique without regard to case.
func (c *conv) bookmarkName(id string) string {
	if name, ok := c.bookmarks[id]; ok {
		return name
	}
	b := make([]byte, 0, len(id))
	for i := 0; i < len(id) && len(b) < 32; i++ {
		ch := id[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			b = append(b, ch)
		} else {
			b = append(b, '_')
		}
	}
	name := string(b)
	if name == "" || !((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) {
		name = "b_" + name
	}
	if name != id || c.usedNames[strings.ToLower(name)] {
		base := name
		for {
			c.nameSeq[base]++
			name = base + "_" + strconv.Itoa(c.nameSeq[base])
			if !c.usedNames[strings.ToLower(name)] {
				break
			}
		}
	}
	c.usedNames[strings.ToLower(name)] = true
	c.bookmarks[id] = name
	return name
}

func (c *conv) bookmark(id string) {
	if c.bookmarked[id] {
		return
	}
	c.bookmarked[id] = true
	name := c.bookmarkName(id)
	if c.para != nil {
		c.para.items = append(c.para.items, item{kind: itemBookmark, text: []byte(name)})
	} else {
		c.pendingBookmarks = append(c.pendingBookmarks, name)
	}
}

// maybeBookmark bookmarks the element's id if something links to it, or
// unconditionally when always is set.
func (c *conv) maybeBookmark(n *html.Node, always bool) {
	if id := attr(n, "id"); id != "" && (always || c.refs[id]) {
		c.bookmark(id)
	}
}

func (c *conv) linkTarget(n *html.Node) *linkRef {
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" {
		return nil
	}
	if href[0] == '#' {
		frag := fragmentID(href)
		if frag == "" || !c.ids[frag] {
			return nil
		}
		return &linkRef{anchor: c.bookmarkName(frag)}
	}
	u, err := url.Parse(href)
	if err != nil {
		c.warn("link", "dropped a link with an invalid URL: "+shorten(href))
		return nil
	}
	if c.base != nil {
		u = c.base.ResolveReference(u)
	}
	switch strings.ToLower(u.Scheme) {
	case "", "http", "https", "mailto", "tel", "sms", "ftp", "ftps", "file":
	default:
		return nil // javascript:, data: and the like
	}
	return &linkRef{url: u.String(), tooltip: strings.TrimSpace(attr(n, "title"))}
}

// ---- lists ----

func (c *conv) newNum(format model.NumFormat, start, level int) int {
	if !format.Ordered() {
		if id, ok := c.bulletNums[format]; ok {
			return id
		}
	}
	id := len(c.doc.Nums) + 1
	c.doc.Nums = append(c.doc.Nums, model.NumDef{ID: id, Format: format, Level: level, Start: start})
	if !format.Ordered() {
		c.bulletNums[format] = id
	}
	return id
}

func (c *conv) list(n *html.Node, bc bctx, st style, bx box) {
	c.flushPara()
	c.forcePendingNum(&bc)
	if bx.breakBefore {
		c.pendingBreak = true
	}
	level := min(bc.listLevel+1, model.MaxListLevel)
	if bc.listLevel < 0 {
		bc.listBase = bc.indent + bx.marginLeft
	}
	bc.listLevel = level
	bc.st = st
	bc.align, bc.line, bc.firstLine, bc.bidi = st.align, st.line, st.textIndent, st.rtl
	if bx.bgSet && !(bx.bg == "FFFFFF" && bc.shade == "") {
		bc.shade = bx.bg
	}

	format := st.listStyle
	if format == "" {
		format = model.NumBullet
	}
	start := 1
	if n.Data == "ol" {
		if v, err := strconv.Atoi(strings.TrimSpace(attr(n, "start"))); err == nil && v >= 0 && v < 1<<20 {
			start = v
		}
		if hasAttr(n, "reversed") {
			c.warn("list", "reversed lists are numbered upwards")
		}
	}
	numID := c.newNum(format, start, level)
	c.maybeBookmark(n, false)

	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch {
		case ch.Type == html.ElementNode && ch.Data == "li" && ch.Namespace == "":
			c.listItem(ch, bc, numID, level)
		case ch.Type == html.ElementNode:
			c.element(ch, bc)
		case ch.Type == html.TextNode:
			c.text(ch.Data, &bc)
		}
	}
	c.flushPara()
	if bx.breakAfter {
		c.pendingBreak = true
	}
}

func (c *conv) listItem(n *html.Node, bc bctx, numID, level int) {
	if hasAttr(n, "hidden") {
		return
	}
	st, bx := c.computeStyle(n, &bc.st)
	if bx.display == "none" {
		return
	}
	c.flushPara()
	bc.depth++
	bx.marginLeft = 0
	c.enterBlock(&bc, st, bx)
	bc.indent = bc.listBase + 720*(level+1)
	bc.paraStyle = model.StyleListParagraph
	pn := &pendingNum{id: numID, level: level, explicit: bc.listBase != 0}
	c.pendingNum = pn
	c.maybeBookmark(n, false)
	c.children(n, bc)
	c.flushPara()
	if c.pendingNum == pn {
		c.forcePendingNum(&bc)
	}
	c.pendingNum = nil
}
