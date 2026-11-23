package dom

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
	"github.com/gomcpgo/htmltodocx/internal/model"
)

// Flex and grid containers lay their items out side by side, and each item
// is a separate box even when the markup has no white space between items.
// Word paragraphs have no such layout. The converter keeps the reading order
// and the separation, and approximates the arrangement:
//
//   - If any item holds block content, or the items run in a column, every
//     item becomes a block of its own, which is what CSS does to flex and
//     grid items anyway ("blockification").
//   - A row of inline items stays on one line. Items spread out with
//     justify-content: space-between (and the other space-* values) are
//     split by tabs, with a right tab stop at the end of the line; packed
//     items are split by a space.
//   - A grid of inline items becomes one line per grid row, with left tab
//     stops at the column positions from grid-template-columns.

// maxGridTracks bounds the column count taken from grid-template-columns;
// Word allows few tab stops, and repeat() could ask for millions.
const maxGridTracks = 32

type layoutItem struct {
	node  *html.Node // nil for a run of text
	text  string
	block bool
}

func (c *conv) layoutChildren(n *html.Node, bc bctx, bx box) {
	items, anyBlock := c.layoutItems(n, bc)
	var starts []int
	if bx.layout == "grid" {
		starts = c.gridColumnStarts(bx, &bc)
	}
	switch {
	case anyBlock || (bx.layout == "flex" && bx.flexColumn) || (bx.layout == "grid" && len(starts) == 0):
		for _, it := range items {
			c.flushPara()
			c.renderItem(it, bc)
			c.flushPara()
		}
	case bx.layout == "grid":
		c.gridRows(items, bc, starts)
	default:
		c.flexRow(items, bc, bx)
	}
}

// layoutItems returns the items of a flex or grid container: element
// children that are displayed and have something to show, and runs of text
// that are not just white space.
func (c *conv) layoutItems(n *html.Node, bc bctx) (items []layoutItem, anyBlock bool) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch ch.Type {
		case html.TextNode:
			if strings.TrimLeft(ch.Data, " \t\n\r\f") != "" {
				items = append(items, layoutItem{text: ch.Data})
			}
		case html.ElementNode:
			if ch.Namespace != "" {
				items = append(items, layoutItem{node: ch})
				continue
			}
			if skipTags[ch.Data] || hasAttr(ch, "hidden") || isEmptyBox(ch) {
				continue
			}
			_, cbx := c.computeStyle(ch, &bc.st)
			if cbx.display == "none" {
				continue
			}
			blk := isBlockLevel(ch.Data, cbx.display)
			items = append(items, layoutItem{node: ch, block: blk})
			anyBlock = anyBlock || blk
		}
	}
	return items, anyBlock
}

// isEmptyBox reports whether an element shows nothing: no text, no images,
// no form controls. Such items (icon placeholders drawn with CSS) would
// otherwise take a tab position of their own.
func isEmptyBox(n *html.Node) bool {
	if n.Data == "img" || n.Data == "input" || n.Data == "br" {
		return false
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch ch.Type {
		case html.TextNode:
			if strings.TrimLeft(ch.Data, " \t\n\r\f") != "" {
				return false
			}
		case html.ElementNode:
			if ch.Namespace != "" || !isEmptyBox(ch) {
				return false
			}
		}
	}
	return true
}

// isBlockLevel reports whether an element is laid out as a block, following
// element(): some tags are always blocks, the rest go by display and tag.
func isBlockLevel(tag, display string) bool {
	switch tag {
	case "table", "ul", "ol", "menu", "dir", "h1", "h2", "h3", "h4", "h5", "h6",
		"pre", "listing", "xmp", "plaintext", "hr":
		return true
	}
	switch display {
	case "block":
		return true
	case "inline":
		return tag == "li"
	}
	return blockTags[tag]
}

func (c *conv) renderItem(it layoutItem, bc bctx) {
	if it.node == nil {
		c.text(it.text, &bc)
		return
	}
	c.element(it.node, bc)
}

func (c *conv) flexRow(items []layoutItem, bc bctx, bx box) {
	switch bx.justify {
	case "space-between", "space-around", "space-evenly":
		if n := len(items); n > 1 {
			left, right := bc.indent, max(bc.avail, bc.indent+1440)
			bc.tabs = make([]model.TabStop, 0, n-1)
			for k := 1; k < n; k++ {
				align := "center"
				if k == n-1 {
					align = "right"
				}
				bc.tabs = append(bc.tabs, model.TabStop{Pos: left + (right-left)*k/(n-1), Align: align})
			}
			c.itemsInLine(items, bc, true)
			return
		}
	case "center":
		bc.align = "center"
	case "flex-end", "end", "right":
		bc.align = "right"
	}
	c.itemsInLine(items, bc, false)
}

func (c *conv) gridRows(items []layoutItem, bc bctx, starts []int) {
	bc.tabs = make([]model.TabStop, len(starts))
	for j, pos := range starts {
		bc.tabs[j] = model.TabStop{Pos: pos, Align: "left"}
	}
	cols := len(starts) + 1
	for r := 0; r < len(items); r += cols {
		c.flushPara()
		c.itemsInLine(items[r:min(r+cols, len(items))], bc, true)
		c.flushPara()
	}
}

// itemsInLine renders items into the current line, separated by a tab or by
// a space. A tab is taken back if the item after it adds nothing.
func (c *conv) itemsInLine(items []layoutItem, bc bctx, tab bool) {
	for i, it := range items {
		if i > 0 && tab && c.para != nil {
			p := c.para
			p.trimTrailing()
			at := len(p.items)
			p.items = append(p.items, item{kind: itemTab, rp: c.runProps(&bc)})
			p.lastSpace, p.spaceAfterBox = true, false
			c.renderItem(it, bc)
			if c.para == p && len(p.items) == at+1 {
				p.items = p.items[:at]
			}
			continue
		}
		if i > 0 && !tab {
			c.text(" ", &bc) // collapses into any space already there
		}
		c.renderItem(it, bc)
	}
}

// gridColumnStarts returns the positions where the second and later grid
// columns start, in twips from the start of the text area, or nil when the
// grid has one column or its column count is unknown (auto-fill, auto-fit).
func (c *conv) gridColumnStarts(bx box, bc *bctx) []int {
	tracks := expandTracks(bx.gridCols)
	if len(tracks) < 2 {
		return nil
	}
	width := float64(max(bc.avail-bc.indent, 1440))
	gap := 0.0
	if bx.colGap != "" {
		if l, ok := css.ParseLength(bx.colGap); ok && l.Unit == "%" {
			gap = width * l.V / 100
		} else if tw, ok := c.lengthTwips(bx.colGap, bc.st.emPt); ok {
			gap = float64(tw)
		}
	}
	free := width - gap*float64(len(tracks)-1)
	widths := make([]float64, len(tracks))
	frs := make([]float64, len(tracks))
	var fixed, frTotal float64
	for i, t := range tracks {
		if strings.HasPrefix(t, "minmax(") && strings.HasSuffix(t, ")") {
			if args := css.SplitComma(t[len("minmax(") : len(t)-1]); len(args) == 2 {
				t = strings.TrimSpace(args[1])
			}
		}
		if f, ok := frValue(t); ok {
			frs[i] = f
			frTotal += f
			continue
		}
		if l, ok := css.ParseLength(t); ok && l.Unit == "%" {
			widths[i] = free * l.V / 100
		} else if tw, ok := c.lengthTwips(t, bc.st.emPt); ok {
			widths[i] = float64(tw)
		} else {
			frs[i] = 1 // auto, min-content, fit-content(): share what is left
			frTotal++
			continue
		}
		fixed += widths[i]
	}
	if frTotal > 0 {
		rest := math.Max(free-fixed, 0)
		for i := range widths {
			if frs[i] > 0 {
				widths[i] = rest * frs[i] / frTotal
			}
		}
	}
	starts := make([]int, 0, len(tracks)-1)
	pos := float64(bc.indent)
	for i := 0; i < len(tracks)-1; i++ {
		pos += widths[i] + gap
		starts = append(starts, int(math.Round(pos)))
	}
	return starts
}

// expandTracks splits a grid-template-columns value into one entry per
// column, expanding repeat(n, ...) and dropping line names. It returns nil
// when the count depends on the container width or exceeds maxGridTracks.
func expandTracks(spec string) []string {
	var out []string
	for _, t := range css.Fields(spec) {
		if strings.HasPrefix(t, "[") {
			continue
		}
		if strings.HasPrefix(t, "repeat(") && strings.HasSuffix(t, ")") {
			args := css.SplitComma(t[len("repeat(") : len(t)-1])
			if len(args) < 2 {
				return nil
			}
			count, err := strconv.Atoi(strings.TrimSpace(args[0]))
			if err != nil || count < 1 || count > maxGridTracks {
				return nil // auto-fill, auto-fit, or nonsense
			}
			inner := expandTracks(strings.Join(args[1:], ","))
			if inner == nil || len(out)+count*len(inner) > maxGridTracks {
				return nil
			}
			for k := 0; k < count; k++ {
				out = append(out, inner...)
			}
			continue
		}
		switch t {
		case "none", "subgrid", "masonry", "inherit", "initial", "unset":
			return nil
		}
		out = append(out, t)
		if len(out) > maxGridTracks {
			return nil
		}
	}
	return out
}

func frValue(t string) (float64, bool) {
	if !strings.HasSuffix(t, "fr") {
		return 0, false
	}
	f, err := strconv.ParseFloat(t[:len(t)-2], 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// boxSpaceBefore separates an inline-block box from a word it would touch.
func (c *conv) boxSpaceBefore(bc *bctx) {
	p := c.para
	if p == nil || p.lastSpace {
		return
	}
	p.spaceAfterBox = false
	for i := len(p.items) - 1; i >= 0; i-- {
		it := &p.items[i]
		if it.kind == itemBookmark {
			continue
		}
		if it.kind != itemText || len(it.text) == 0 {
			return
		}
		r, _ := utf8.DecodeLastRune(it.text)
		if !strings.ContainsRune(" \t\n([{“‘\"'¿¡", r) {
			c.addText(" ", bc, false)
			p.lastSpace = true
		}
		return
	}
}

// firstVisibleRune returns the first rune of s if it is not white space.
func firstVisibleRune(s string) (rune, bool) {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || (size == 1 && isCollapsible(s[0])) {
		return 0, false
	}
	return r, true
}
