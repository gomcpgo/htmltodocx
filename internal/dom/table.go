package dom

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
	"github.com/gomcpgo/htmltodocx/internal/model"
)

const (
	maxTableCols = 63 // Word's limit
	minColWidth  = 240
	cellPadding  = 216 // left plus right cell margin of the table styles
)

// tcell is a cell placed on the table grid.
type tcell struct {
	n      *html.Node // nil for filler cells
	st     style
	bx     box
	row    *trow
	col    int
	span   int
	vmerge int
	origin *tcell // for continuation cells, the cell being continued
}

type trow struct {
	bx      box
	bg      string // background inherited from the row group
	cells   []*tcell
	header  bool
	allHead bool // every cell is a <th>
}

type colOcc struct {
	rem    int // rows still covered by a rowspan from above
	origin *tcell
	fresh  bool // set in the current row; do not count it down yet
}

func spanAttr(n *html.Node, key string, limit int) int {
	v, err := strconv.Atoi(strings.TrimSpace(attr(n, key)))
	if err != nil || v < 1 {
		return 1
	}
	return min(v, limit)
}

func (c *conv) table(n *html.Node, bc bctx, st style, bx box) {
	c.flushPara()
	c.forcePendingNum(&bc)
	if bx.breakBefore {
		c.pendingBreak = true
	}

	// Pass 1: find the rows and place every cell on the grid.
	var head, body, foot []*trow
	var caption *html.Node
	var colLens []css.Length // declared column widths; Unit "" means none
	addCols := func(n *html.Node, pst *style) {
		_, cbx := c.computeStyle(n, pst)
		for i := spanAttr(n, "span", maxTableCols); i > 0 && len(colLens) < maxTableCols; i-- {
			var l css.Length
			if cbx.hasWidth {
				l = cbx.width
			}
			colLens = append(colLens, l)
		}
	}
	addRow := func(tr *html.Node, pst *style, groupBg string, dst *[]*trow) {
		if hasAttr(tr, "hidden") {
			return
		}
		rst, rbx := c.computeStyle(tr, pst)
		if rbx.display == "none" {
			return
		}
		row := &trow{bx: rbx, bg: groupBg, allHead: true}
		for td := tr.FirstChild; td != nil; td = td.NextSibling {
			if td.Type != html.ElementNode || (td.Data != "td" && td.Data != "th") || hasAttr(td, "hidden") {
				continue
			}
			cst, cbx := c.computeStyle(td, &rst)
			if cbx.display == "none" {
				continue
			}
			if td.Data != "th" {
				row.allHead = false
			}
			row.cells = append(row.cells, &tcell{n: td, st: cst, bx: cbx, row: row})
		}
		*dst = append(*dst, row)
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode {
			continue
		}
		switch ch.Data {
		case "caption":
			if caption == nil {
				caption = ch
			}
		case "colgroup":
			cols := false
			for col := ch.FirstChild; col != nil; col = col.NextSibling {
				if col.Type == html.ElementNode && col.Data == "col" {
					cols = true
					addCols(col, &st)
				}
			}
			if !cols {
				addCols(ch, &st)
			}
		case "col":
			addCols(ch, &st)
		case "thead", "tbody", "tfoot":
			gst, gbx := c.computeStyle(ch, &st)
			if gbx.display == "none" || hasAttr(ch, "hidden") {
				continue
			}
			dst := &body
			if ch.Data == "thead" {
				dst = &head
			} else if ch.Data == "tfoot" {
				dst = &foot
			}
			for tr := ch.FirstChild; tr != nil; tr = tr.NextSibling {
				if tr.Type == html.ElementNode && tr.Data == "tr" {
					addRow(tr, &gst, gbx.bg, dst)
				}
			}
		case "tr":
			addRow(ch, &st, "", &body)
		}
	}
	for _, r := range head {
		r.header = true
	}
	rows := append(append(head, body...), foot...)
	if len(head) == 0 && len(rows) > 1 && rows[0].allHead && len(rows[0].cells) > 0 {
		rows[0].header = true
	}

	ncols := c.placeCells(rows)

	if caption != nil {
		cst, cbx := c.computeStyle(caption, &st)
		if cbx.display != "none" {
			cbc := bc
			cbc.depth++
			c.block(caption, cbc, cst, cbx, blockOpts{style: model.StyleCaption, keepNext: true})
		}
	}
	if ncols == 0 {
		return
	}

	// Column widths.
	avail := max(bc.avail-bc.indent, 1440)
	t := &model.Table{Indent: bc.indent, Align: bx.tableAlign}
	if bx.autoLeft && bx.autoRight {
		t.Align = "center"
	}
	tableW := 0
	if bx.hasWidth {
		tableW = min(c.resolveWidth(bx.width, &st, avail), avail)
	}
	t.Width = tableW
	widths, known := c.columnWidths(rows, colLens, ncols, tableW, avail, &st)
	t.Cols = widths

	// Borders.
	cssBorders := bx.hasBorder()
	for _, r := range rows {
		if cssBorders {
			break
		}
		cssBorders = r.bx.hasBorder()
		for _, cell := range r.cells {
			if cell.n != nil && cell.bx.hasBorder() {
				cssBorders = true
			}
		}
	}
	borderAttr := strings.TrimSpace(attr(n, "border"))
	switch {
	case cssBorders:
		t.Style = model.StyleTablePlain
		if bx.hasBorder() {
			t.Borders = &[4]model.Border{}
			for i := range t.Borders {
				t.Borders[i] = bx.border(i)
			}
		}
	case borderAttr == "0" || (borderAttr == "" && bx.bordersOff()):
		t.Style = model.StyleTablePlain
	default:
		t.Style = model.StyleTableGrid
	}

	// Pass 2: convert the cell contents.
	for _, r := range rows {
		row := model.Row{Header: r.header}
		for _, cell := range r.cells {
			src := cell
			if cell.origin != nil {
				src = cell.origin
			}
			mc := model.Cell{ColSpan: cell.span, VMerge: cell.vmerge}
			w, allKnown := 0, true
			for i := cell.col; i < cell.col+cell.span && i < ncols; i++ {
				w += widths[i]
				allKnown = allKnown && known[i]
			}
			if allKnown {
				mc.Width = w
			}
			if src.n != nil {
				mc.Shade = firstNonEmpty(src.bx.bg, src.row.bx.bg, src.row.bg, bx.bg)
				mc.VAlign = firstNonEmpty(src.bx.valign, src.row.bx.valign)
				if cssBorders {
					mc.Borders = cellBorders(src)
				}
			}
			if cell.n != nil && cell.origin == nil {
				cbc := bctx{
					st:        cell.st,
					align:     cell.st.align,
					line:      cell.st.line,
					firstLine: cell.st.textIndent,
					bidi:      cell.st.rtl,
					link:      bc.link,
					listLevel: -1,
					avail:     max(w-cellPadding, minColWidth),
					depth:     bc.depth + 3,
				}
				mc.Blocks = c.cellBlocks(cell.n, cbc)
			}
			row.Cells = append(row.Cells, mc)
		}
		t.Rows = append(t.Rows, row)
	}
	*c.sink = append(*c.sink, t)
	if bx.breakAfter {
		c.pendingBreak = true
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// cellBorders resolves a cell's borders. A border set on the row applies to
// the top and bottom of its cells, as it does with border-collapse.
func cellBorders(cell *tcell) *[4]model.Border {
	var b [4]model.Border
	any := false
	for i := range b {
		b[i] = cell.bx.border(i)
		if b[i].Style == "" && (i == model.Top || i == model.Bottom) {
			b[i] = cell.row.bx.border(i)
		}
		any = any || b[i].Style != ""
	}
	if !any {
		return nil
	}
	return &b
}

// cellBlocks converts the contents of a cell into its own list of blocks.
func (c *conv) cellBlocks(n *html.Node, bc bctx) []model.Block {
	var blocks []model.Block
	savedSink, savedNum, savedBreak := c.sink, c.pendingNum, c.pendingBreak
	c.sink, c.pendingNum, c.pendingBreak = &blocks, nil, false
	c.maybeBookmark(n, false)
	c.children(n, bc)
	c.flushPara()
	c.sink, c.pendingNum, c.pendingBreak = savedSink, savedNum, savedBreak
	return blocks
}

// placeCells lays the cells out on a grid, resolving colspan and rowspan. On
// return every row covers the same number of columns: cells hidden by a
// rowspan are represented by continuation cells and gaps by empty cells. It
// returns the number of columns.
func (c *conv) placeCells(rows []*trow) int {
	var occ []colOcc
	ncols := 0
	for _, r := range rows {
		placed := make([]*tcell, 0, len(r.cells))
		col := 0
		skipCovered := func() {
			for col < len(occ) && occ[col].rem > 0 {
				o := occ[col].origin
				placed = append(placed, &tcell{row: r, col: col, span: o.span, vmerge: model.VMergeContinue, origin: o})
				col += o.span
			}
		}
		for _, cell := range r.cells {
			skipCovered()
			if col >= maxTableCols {
				c.warn("table", "a table has more than 63 columns; the extra cells were dropped")
				break
			}
			span := spanAttr(cell.n, "colspan", maxTableCols-col)
			for k := 1; k < span; k++ {
				if col+k < len(occ) && occ[col+k].rem > 0 {
					span = k // a rowspan from above is in the way
					break
				}
			}
			cell.col, cell.span = col, span
			for len(occ) < col+span {
				occ = append(occ, colOcc{})
			}
			if rs := spanAttr(cell.n, "rowspan", 65534); rs > 1 {
				cell.vmerge = model.VMergeRestart
				for k := 0; k < span; k++ {
					occ[col+k] = colOcc{rem: rs - 1, origin: cell, fresh: true}
				}
			}
			placed = append(placed, cell)
			col += span
		}
		for col < len(occ) {
			skipCovered()
			if col < len(occ) {
				placed = append(placed, &tcell{row: r, col: col, span: 1})
				col++
			}
		}
		for k := range occ {
			if occ[k].fresh {
				occ[k].fresh = false
			} else if occ[k].rem > 0 {
				occ[k].rem--
			}
		}
		r.cells = placed
		ncols = max(ncols, col)
	}
	for _, r := range rows {
		col := 0
		if n := len(r.cells); n > 0 {
			col = r.cells[n-1].col + r.cells[n-1].span
		}
		for ; col < ncols; col++ {
			r.cells = append(r.cells, &tcell{row: r, col: col, span: 1})
		}
	}
	// A rowspan that runs past the last row has nothing to merge with.
	for i, r := range rows {
		for _, cell := range r.cells {
			if cell.vmerge != model.VMergeRestart {
				continue
			}
			continued := false
			if i+1 < len(rows) {
				for _, next := range rows[i+1].cells {
					if next.origin == cell {
						continued = true
					}
				}
			}
			if !continued {
				cell.vmerge = model.VMergeNone
			}
		}
	}
	return ncols
}

// resolveWidth converts a CSS width to twips. Percentages refer to avail.
func (c *conv) resolveWidth(l css.Length, st *style, avail int) int {
	if l.Unit == "%" {
		return int(float64(avail) * l.V / 100)
	}
	pt, ok := l.Points(st.emPt, c.rootPt)
	if !ok || pt <= 0 {
		return 0
	}
	return int(pt * 20)
}

// columnWidths works out a width for every column. known[i] reports whether
// column i's width came from the document rather than from an even split.
func (c *conv) columnWidths(rows []*trow, colLens []css.Length, ncols, tableW, avail int, st *style) ([]int, []bool) {
	widths := make([]int, ncols)
	known := make([]bool, ncols)
	total := tableW
	if total == 0 {
		total = avail
	}
	set := func(i int, l css.Length) {
		if i < ncols && !known[i] && l.Unit != "" {
			if w := c.resolveWidth(l, st, total); w > 0 {
				widths[i], known[i] = max(w, minColWidth), true
			}
		}
	}
	for i, l := range colLens {
		set(i, l)
	}
	for _, r := range rows {
		for _, cell := range r.cells {
			if cell.n != nil && cell.origin == nil && cell.span == 1 && cell.bx.hasWidth {
				set(cell.col, cell.bx.width)
			}
		}
	}

	sum, unknown := 0, 0
	for i := range widths {
		if known[i] {
			sum += widths[i]
		} else {
			unknown++
		}
	}
	if unknown > 0 {
		share := max((total-sum)/unknown, minColWidth)
		if sum == 0 {
			share = max(total/ncols, minColWidth)
		}
		for i := range widths {
			if !known[i] {
				widths[i] = share
				known[i] = tableW > 0
			}
		}
		sum += share * unknown
	}
	if limit := max(total, minColWidth*ncols); sum > limit {
		for i := range widths {
			widths[i] = max(widths[i]*limit/sum, 1)
		}
	}
	return widths, known
}
