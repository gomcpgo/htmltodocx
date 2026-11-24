package dom

import (
	"strings"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
)

// maxGenerated bounds the text produced by ::before and ::after in one
// conversion. counters() on a deeply nested counter repeated on many
// elements could otherwise produce text quadratic in the input.
const maxGenerated = 4 << 20

// counter is one instance of a CSS counter. Its scope is the descendants of
// scope: an element's counter-reset applies to the element, its following
// siblings and their descendants, so scope is the element's parent.
type counter struct {
	scope *html.Node
	value int
}

func isAncestorOrSelf(a, n *html.Node) bool {
	for ; n != nil; n = n.Parent {
		if n == a {
			return true
		}
	}
	return false
}

// liveCounters returns the instances of a counter in scope at ref, outermost
// first, dropping those whose scope has ended. Instances are pushed in
// document order, so each one's scope lies inside the one below it, and only
// the top needs checking.
func (c *conv) liveCounters(name string, ref *html.Node) []counter {
	s := c.counters[name]
	for len(s) > 0 && !isAncestorOrSelf(s[len(s)-1].scope, ref) {
		s = s[:len(s)-1]
	}
	c.counters[name] = s
	return s
}

// innermost returns the counter that ref's operations apply to, creating one
// with the given scope if none is in scope, as CSS does.
func (c *conv) innermost(name string, ref, scope *html.Node) *counter {
	s := c.liveCounters(name, ref)
	if len(s) == 0 {
		s = append(s, counter{scope: scope})
		c.counters[name] = s
	}
	return &s[len(s)-1]
}

// applyCounters runs counter-reset, counter-increment and counter-set, in
// that order, for an element (scope is its parent) or for its ::before or
// ::after (scope is the element).
func (c *conv) applyCounters(ref, scope *html.Node, bx *box) {
	if c.counters == nil {
		c.counters = map[string][]counter{}
	}
	for _, op := range bx.counterReset {
		s := c.liveCounters(op.Name, ref)
		if n := len(s); n > 0 && s[n-1].scope == scope {
			// A reset on a sibling replaces the sibling's counter.
			s[n-1].value = op.Value
		} else {
			c.counters[op.Name] = append(s, counter{scope: scope, value: op.Value})
		}
	}
	for _, op := range bx.counterIncrement {
		c.innermost(op.Name, ref, scope).value += op.Value
	}
	for _, op := range bx.counterSet {
		c.innermost(op.Name, ref, scope).value = op.Value
	}
}

// generated adds the ::before (pe is css.Before) or ::after content of n to
// the current paragraph. bc is the context inside n.
func (c *conv) generated(n *html.Node, bc *bctx, pe int) {
	if c.sheet == nil || !c.sheet.HasPseudo(pe) || c.generatedBytes > maxGenerated {
		return
	}
	c.matchBuf = c.sheet.MatchPseudo(n, pe, c.matchBuf[:0])
	if len(c.matchBuf) == 0 {
		return
	}
	// The pseudo-element is a child of n: it inherits n's style, and its
	// box properties start from nothing.
	parent := bc.st
	st := parent
	var bx box
	c.applyAuthorStyles(&st, &bx, &parent, nil)
	finishColors(&st, &bx)
	if bx.display == "none" || !bx.hasContent {
		return
	}
	items, ok := css.ParseContent(bx.content)
	if !ok || len(items) == 0 {
		return // content: none or normal makes no box
	}
	c.applyCounters(n, n, &bx)
	text := c.contentText(n, items)
	if text == "" {
		return
	}
	pbc := *bc
	if bx.bgSet {
		st.runBg = bx.bg
	}
	pbc.st = st
	block := bx.display == "block"
	if block {
		c.flushPara()
	}
	// A positioned marker, such as a bullet placed in the padding, sits
	// apart from the text in a browser; in a paragraph it needs a space.
	if bx.absolute {
		c.boxSpaceBefore(&pbc)
	}
	c.text(text, &pbc)
	switch {
	case block:
		c.flushPara()
	case bx.absolute && c.para != nil:
		c.para.spaceAfterBox = true
	}
}

func (c *conv) contentText(n *html.Node, items []css.ContentItem) string {
	var b strings.Builder
	for _, it := range items {
		switch it.Kind {
		case css.ContentText:
			b.WriteString(it.Text)
		case css.ContentCounter:
			v := c.innermost(it.Name, n, n).value
			b.WriteString(css.FormatCounter(v, it.Style))
		case css.ContentCounters:
			c.innermost(it.Name, n, n)
			for i, ct := range c.counters[it.Name] {
				if i > 0 {
					b.WriteString(it.Text)
				}
				b.WriteString(css.FormatCounter(ct.value, it.Style))
				if b.Len() > maxGenerated {
					break
				}
			}
		case css.ContentAttr:
			b.WriteString(attr(n, it.Name))
		case css.ContentOpenQuote:
			b.WriteString(quoteMarks[c.quoteDepth%2][0])
			c.quoteDepth++
		case css.ContentCloseQuote:
			if c.quoteDepth > 0 {
				c.quoteDepth--
			}
			b.WriteString(quoteMarks[c.quoteDepth%2][1])
		case css.ContentURL:
			c.warn("content", "images in CSS generated content (url()) are not supported and were skipped")
		}
	}
	c.generatedBytes += b.Len()
	if c.generatedBytes > maxGenerated {
		c.warn("content", "the document generates too much text from CSS content; the rest was skipped")
		return ""
	}
	return b.String()
}

// quoteMarks are the open and close quotes of the outer and inner levels.
var quoteMarks = [2][2]string{{"“", "”"}, {"‘", "’"}}
