package ooxml

import (
	"strconv"
	"strings"

	"github.com/gomcpgo/htmltodocx/internal/model"
)

type relationship struct {
	id       string
	typ      string
	target   string
	external bool
}

type media struct {
	data *model.ImageData
	name string // file name under word/media
	rid  string
}

// docWriter builds word/document.xml and collects the relationships and
// media files it refers to.
type docWriter struct {
	b         buf
	rels      []relationship
	linkIDs   map[string]string
	media     []*media
	mediaByID map[*model.ImageData]*media
	bookmarks int
	drawings  int
}

func (d *docWriter) addRel(typ, target string, external bool) string {
	id := "rId" + strconv.Itoa(len(d.rels)+1)
	d.rels = append(d.rels, relationship{id, typ, target, external})
	return id
}

func (d *docWriter) document(doc *model.Document, opt Options) {
	b := &d.b
	b.s(xmlHeader, `<w:document xmlns:w="`, nsW, `" xmlns:r="`, nsR, `" xmlns:wp="`, nsWP,
		`" xmlns:a="`, nsA, `" xmlns:pic="`, nsPic, `"><w:body>`)
	d.blocks(doc.Blocks, false)
	b.s(`<w:sectPr><w:pgSz w:w="`)
	b.i(opt.PageWidth)
	b.s(`" w:h="`)
	b.i(opt.PageHeight)
	if opt.Landscape {
		b.s(`" w:orient="landscape`)
	}
	b.s(`"/><w:pgMar w:top="`)
	b.i(opt.MarginTop)
	b.s(`" w:right="`)
	b.i(opt.MarginRight)
	b.s(`" w:bottom="`)
	b.i(opt.MarginBottom)
	b.s(`" w:left="`)
	b.i(opt.MarginLeft)
	b.s(`" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr></w:body></w:document>`)
}

func (d *docWriter) blocks(blocks []model.Block, inCell bool) {
	prevTable := false
	for _, blk := range blocks {
		switch v := blk.(type) {
		case *model.Paragraph:
			d.paragraph(v)
			prevTable = false
		case *model.HorizontalRule:
			d.b.s(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="A6A6A6"/></w:pBdr>`)
			if v.IndentLeft > 0 {
				d.b.s(`<w:ind w:left="`)
				d.b.i(v.IndentLeft)
				d.b.s(`"/>`)
			}
			d.b.s(`</w:pPr></w:p>`)
			prevTable = false
		case *model.Table:
			if prevTable {
				// Word joins tables that touch; keep them apart.
				d.b.s(`<w:p/>`)
			}
			d.table(v)
			prevTable = true
		}
	}
	// A cell must contain a paragraph, and must end with one.
	if inCell && (len(blocks) == 0 || prevTable) {
		d.b.s(`<w:p/>`)
	}
}

func (d *docWriter) paragraph(p *model.Paragraph) {
	b := &d.b
	b.s(`<w:p>`)
	pp := &p.Props
	mark := b.Len()
	b.s(`<w:pPr>`)
	empty := b.Len()
	if pp.Style != "" {
		b.s(`<w:pStyle w:val="`)
		b.esc(pp.Style)
		b.s(`"/>`)
	}
	if pp.KeepNext {
		b.s(`<w:keepNext/>`)
	}
	if pp.PageBreakBefore {
		b.s(`<w:pageBreakBefore/>`)
	}
	if pp.Num != nil {
		b.s(`<w:numPr><w:ilvl w:val="`)
		b.i(pp.Num.Level)
		b.s(`"/><w:numId w:val="`)
		b.i(pp.Num.ID)
		b.s(`"/></w:numPr>`)
	}
	b.borders("pBdr", pp.Borders)
	if pp.Shade != "" {
		b.s(`<w:shd w:val="clear" w:color="auto" w:fill="`, pp.Shade, `"/>`)
	}
	if len(pp.Tabs) > 0 {
		b.s(`<w:tabs>`)
		for _, t := range pp.Tabs {
			b.s(`<w:tab w:val="`, t.Align, `" w:pos="`)
			b.i(t.Pos)
			b.s(`"/>`)
		}
		b.s(`</w:tabs>`)
	}
	if pp.Bidi {
		b.s(`<w:bidi/>`)
	}
	if pp.Line > 0 {
		b.s(`<w:spacing w:line="`)
		b.i(pp.Line)
		b.s(`" w:lineRule="auto"/>`)
	}
	if pp.IndentLeft > 0 || pp.IndentRight > 0 || pp.FirstLine != 0 {
		b.s(`<w:ind`)
		if pp.IndentLeft > 0 {
			b.s(` w:left="`)
			b.i(pp.IndentLeft)
			b.s(`"`)
		}
		if pp.IndentRight > 0 {
			b.s(` w:right="`)
			b.i(pp.IndentRight)
			b.s(`"`)
		}
		if pp.FirstLine > 0 {
			b.s(` w:firstLine="`)
			b.i(pp.FirstLine)
			b.s(`"`)
		} else if pp.FirstLine < 0 {
			b.s(` w:hanging="`)
			b.i(-pp.FirstLine)
			b.s(`"`)
		}
		b.s(`/>`)
	}
	if al := pp.Align; al != "" {
		// In a right-to-left paragraph Word reads "left" as the leading
		// edge, so physical alignment has to be mirrored.
		if pp.Bidi {
			switch al {
			case "left":
				al = "right"
			case "right":
				al = "left"
			}
		}
		b.s(`<w:jc w:val="`, al, `"/>`)
	}
	if b.Len() == empty {
		b.Truncate(mark)
	} else {
		b.s(`</w:pPr>`)
	}
	d.inlines(p.Inlines, false)
	b.s(`</w:p>`)
}

func (d *docWriter) inlines(ins []model.Inline, inLink bool) {
	b := &d.b
	for _, in := range ins {
		switch v := in.(type) {
		case *model.Text:
			if v.Text == "" {
				continue
			}
			b.s(`<w:r>`)
			d.runProps(&v.Props, inLink)
			d.runText(v.Text)
			b.s(`</w:r>`)
		case *model.LineBreak:
			b.s(`<w:r><w:br/></w:r>`)
		case *model.Bookmark:
			d.bookmarks++
			b.s(`<w:bookmarkStart w:id="`)
			b.i(d.bookmarks)
			b.s(`" w:name="`)
			b.esc(v.Name)
			b.s(`"/><w:bookmarkEnd w:id="`)
			b.i(d.bookmarks)
			b.s(`"/>`)
		case *model.Image:
			d.image(v)
		case *model.Link:
			if inLink || (v.URL == "" && v.Anchor == "") {
				d.inlines(v.Inlines, inLink)
				continue
			}
			b.s(`<w:hyperlink`)
			if v.Anchor != "" {
				b.s(` w:anchor="`)
				b.esc(v.Anchor)
				b.s(`"`)
			} else {
				id, ok := d.linkIDs[v.URL]
				if !ok {
					id = d.addRel(relHyperlink, v.URL, true)
					d.linkIDs[v.URL] = id
				}
				b.s(` r:id="`, id, `"`)
			}
			if v.Tooltip != "" {
				b.s(` w:tooltip="`)
				b.esc(v.Tooltip)
				b.s(`"`)
			}
			b.s(` w:history="1">`)
			d.inlines(v.Inlines, true)
			b.s(`</w:hyperlink>`)
		}
	}
}

func (b *buf) toggle(name string, t model.Tri) {
	switch t {
	case model.On:
		b.s(`<w:`, name, `/>`)
	case model.Off:
		b.s(`<w:`, name, ` w:val="0"/>`)
	}
}

const monoFont = "Courier New"

func validLang(s string) bool {
	if s == "" || len(s) > 35 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

// runProps writes w:rPr. The children are in the order the schema lists them.
func (d *docWriter) runProps(rp *model.RunProps, inLink bool) {
	b := &d.b
	style, font := rp.Style, rp.Font
	if inLink {
		// A run takes one character style. The link wins, so code inside a
		// link keeps its monospaced font directly.
		if style == model.StyleVerbatimChar && font == "" {
			font = monoFont
		}
		style = model.StyleHyperlink
	}
	mark := b.Len()
	b.s(`<w:rPr>`)
	empty := b.Len()
	if style != "" {
		b.s(`<w:rStyle w:val="`)
		b.esc(style)
		b.s(`"/>`)
	}
	if font != "" {
		b.s(`<w:rFonts w:ascii="`)
		b.esc(font)
		b.s(`" w:hAnsi="`)
		b.esc(font)
		b.s(`" w:eastAsia="`)
		b.esc(font)
		b.s(`" w:cs="`)
		b.esc(font)
		b.s(`"/>`)
	}
	b.toggle("b", rp.Bold)
	b.toggle("bCs", rp.Bold)
	b.toggle("i", rp.Italic)
	b.toggle("iCs", rp.Italic)
	b.toggle("caps", rp.Caps)
	b.toggle("smallCaps", rp.SmallCaps)
	b.toggle("strike", rp.Strike)
	if rp.Color != "" {
		b.s(`<w:color w:val="`, rp.Color, `"/>`)
	}
	if rp.SizeHalfPt > 0 {
		b.s(`<w:sz w:val="`)
		b.i(rp.SizeHalfPt)
		b.s(`"/><w:szCs w:val="`)
		b.i(rp.SizeHalfPt)
		b.s(`"/>`)
	}
	switch rp.Underline {
	case model.On:
		b.s(`<w:u w:val="single"/>`)
	case model.Off:
		b.s(`<w:u w:val="none"/>`)
	}
	if rp.Shade != "" {
		b.s(`<w:shd w:val="clear" w:color="auto" w:fill="`, rp.Shade, `"/>`)
	}
	switch rp.Vert {
	case model.VertSuper:
		b.s(`<w:vertAlign w:val="superscript"/>`)
	case model.VertSub:
		b.s(`<w:vertAlign w:val="subscript"/>`)
	}
	if rp.RTL {
		b.s(`<w:rtl/>`)
	}
	if validLang(rp.Lang) {
		if rp.RTL {
			b.s(`<w:lang w:bidi="`, rp.Lang, `"/>`)
		} else {
			b.s(`<w:lang w:val="`, rp.Lang, `"/>`)
		}
	}
	if b.Len() == empty {
		b.Truncate(mark)
	} else {
		b.s(`</w:rPr>`)
	}
}

// runText writes the text of a run. Newlines become line breaks and tabs
// become tab elements.
func (d *docWriter) runText(s string) {
	b := &d.b
	for len(s) > 0 {
		i := strings.IndexAny(s, "\n\t")
		seg := s
		if i >= 0 {
			seg = s[:i]
		}
		if seg != "" {
			if seg[0] == ' ' || seg[len(seg)-1] == ' ' {
				b.s(`<w:t xml:space="preserve">`)
			} else {
				b.s(`<w:t>`)
			}
			b.esc(seg)
			b.s(`</w:t>`)
		}
		if i < 0 {
			return
		}
		if s[i] == '\n' {
			b.s(`<w:br/>`)
		} else {
			b.s(`<w:tab/>`)
		}
		s = s[i+1:]
	}
}

// mediaFor returns the media part for an image, adding it on first use.
func (d *docWriter) mediaFor(data *model.ImageData) *media {
	m := d.mediaByID[data]
	if m == nil {
		m = &media{data: data, name: "image" + strconv.Itoa(len(d.media)+1) + "." + data.Ext}
		m.rid = d.addRel(relImage, "media/"+m.name, false)
		d.media = append(d.media, m)
		d.mediaByID[data] = m
	}
	return m
}

// svgBlipExt is the extension, from Office 2016, that attaches an SVG part to
// a picture. Readers that do not know it show the PNG the picture names.
const svgBlipExt = `<a:extLst><a:ext uri="{96DAC541-7B7A-43D3-8B79-37D633B846F1}">` +
	`<asvg:svgBlip xmlns:asvg="http://schemas.microsoft.com/office/drawing/2016/SVG/main" r:embed="`

func (d *docWriter) image(img *model.Image) {
	if img.Data == nil {
		return
	}
	m := d.mediaFor(img.Data)
	var svgRID string
	if img.SVG != nil {
		svgRID = d.mediaFor(img.SVG).rid
	}
	d.drawings++
	b := &d.b
	b.s(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="`)
	b.i64(img.Width)
	b.s(`" cy="`)
	b.i64(img.Height)
	b.s(`"/><wp:effectExtent l="0" t="0" r="0" b="0"/><wp:docPr id="`)
	b.i(d.drawings)
	b.s(`" name="Picture `)
	b.i(d.drawings)
	b.s(`" descr="`)
	b.esc(img.Alt)
	b.s(`"/><wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>`,
		`<a:graphic><a:graphicData uri="`, nsPic, `"><pic:pic><pic:nvPicPr><pic:cNvPr id="`)
	b.i(d.drawings)
	b.s(`" name="`, m.name, `"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="`, m.rid)
	if svgRID != "" {
		b.s(`">`, svgBlipExt, svgRID, `"/></a:ext></a:extLst></a:blip>`)
	} else {
		b.s(`"/>`)
	}
	b.s(`<a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="`)
	b.i64(img.Width)
	b.s(`" cy="`)
	b.i64(img.Height)
	b.s(`"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic>`,
		`</a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`)
}

func (b *buf) border(name string, br model.Border) {
	if br.Style == "" {
		return
	}
	color := br.Color
	if color == "" {
		color = "auto"
	}
	b.s(`<w:`, name, ` w:val="`, br.Style, `" w:sz="`)
	b.i(br.Size)
	b.s(`" w:space="`)
	b.i(br.Space)
	b.s(`" w:color="`, color, `"/>`)
}

var sideNames = [4]string{model.Top: "top", model.Left: "left", model.Bottom: "bottom", model.Right: "right"}

func (b *buf) borders(elem string, bs *[4]model.Border) {
	if bs == nil {
		return
	}
	b.s(`<w:`, elem, `>`)
	for i, name := range sideNames {
		b.border(name, bs[i])
	}
	b.s(`</w:`, elem, `>`)
}

func (d *docWriter) table(t *model.Table) {
	b := &d.b
	b.s(`<w:tbl><w:tblPr>`)
	if t.Style != "" {
		b.s(`<w:tblStyle w:val="`)
		b.esc(t.Style)
		b.s(`"/>`)
	}
	if t.Width > 0 {
		b.s(`<w:tblW w:w="`)
		b.i(t.Width)
		b.s(`" w:type="dxa"/>`)
	} else {
		b.s(`<w:tblW w:w="0" w:type="auto"/>`)
	}
	if t.Align != "" {
		b.s(`<w:jc w:val="`, t.Align, `"/>`)
	}
	if t.Indent > 0 && t.Align == "" {
		b.s(`<w:tblInd w:w="`)
		b.i(t.Indent)
		b.s(`" w:type="dxa"/>`)
	}
	b.borders("tblBorders", t.Borders)
	b.s(`</w:tblPr><w:tblGrid>`)
	for _, w := range t.Cols {
		b.s(`<w:gridCol w:w="`)
		b.i(w)
		b.s(`"/>`)
	}
	b.s(`</w:tblGrid>`)
	for i := range t.Rows {
		row := &t.Rows[i]
		b.s(`<w:tr>`)
		if row.Header {
			b.s(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for j := range row.Cells {
			cell := &row.Cells[j]
			b.s(`<w:tc><w:tcPr>`)
			if cell.Width > 0 {
				b.s(`<w:tcW w:w="`)
				b.i(cell.Width)
				b.s(`" w:type="dxa"/>`)
			} else {
				b.s(`<w:tcW w:w="0" w:type="auto"/>`)
			}
			if cell.ColSpan > 1 {
				b.s(`<w:gridSpan w:val="`)
				b.i(cell.ColSpan)
				b.s(`"/>`)
			}
			switch cell.VMerge {
			case model.VMergeRestart:
				b.s(`<w:vMerge w:val="restart"/>`)
			case model.VMergeContinue:
				b.s(`<w:vMerge/>`)
			}
			b.borders("tcBorders", cell.Borders)
			if cell.Shade != "" {
				b.s(`<w:shd w:val="clear" w:color="auto" w:fill="`, cell.Shade, `"/>`)
			}
			if cell.VAlign != "" {
				b.s(`<w:vAlign w:val="`, cell.VAlign, `"/>`)
			}
			b.s(`</w:tcPr>`)
			d.blocks(cell.Blocks, true)
			b.s(`</w:tc>`)
		}
		b.s(`</w:tr>`)
	}
	b.s(`</w:tbl>`)
}
