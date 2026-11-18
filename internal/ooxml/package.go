package ooxml

import (
	"archive/zip"
	"io"
	"strconv"
	"time"

	"github.com/gomcpgo/htmltodocx/internal/model"
)

// Options controls the parts of the package that do not come from the
// document model. Lengths are in twips.
type Options struct {
	PageWidth    int
	PageHeight   int
	Landscape    bool
	MarginTop    int
	MarginRight  int
	MarginBottom int
	MarginLeft   int
	Created      time.Time
}

// Write writes doc to w as a .docx package.
func Write(w io.Writer, doc *model.Document, opt Options) error {
	d := &docWriter{
		linkIDs:   map[string]string{},
		mediaByID: map[*model.ImageData]*media{},
	}
	// Fixed relationships come first so their IDs are stable.
	d.addRel(relStyles, "styles.xml", false)
	d.addRel(relSettings, "settings.xml", false)
	hasNums := len(doc.Nums) > 0
	if hasNums {
		d.addRel(relNumbering, "numbering.xml", false)
	}
	d.document(doc, opt)

	zw := zip.NewWriter(w)
	created := opt.Created.UTC()
	add := func(name string, data []byte, method uint16) error {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: created})
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	}
	parts := []struct {
		name string
		data []byte
	}{
		{"[Content_Types].xml", contentTypes(d, hasNums)},
		{"_rels/.rels", packageRels()},
		{"word/document.xml", d.b.Bytes()},
		{"word/_rels/document.xml.rels", documentRels(d.rels)},
		{"word/styles.xml", stylesXML(doc)},
		{"word/settings.xml", settingsXML()},
		{"docProps/core.xml", coreXML(&doc.Meta, created)},
		{"docProps/app.xml", appXML()},
	}
	for _, p := range parts {
		if err := add(p.name, p.data, zip.Deflate); err != nil {
			return err
		}
	}
	if hasNums {
		if err := add("word/numbering.xml", numberingXML(doc.Nums), zip.Deflate); err != nil {
			return err
		}
	}
	for _, m := range d.media {
		// Images are already compressed.
		if err := add("word/media/"+m.name, m.data.Bytes, zip.Store); err != nil {
			return err
		}
	}
	return zw.Close()
}

func contentTypes(d *docWriter, hasNums bool) []byte {
	var b buf
	b.s(xmlHeader, `<Types xmlns="`, nsContentTypes, `">`,
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`,
		`<Default Extension="xml" ContentType="application/xml"/>`)
	seen := map[string]bool{}
	for _, m := range d.media {
		if !seen[m.data.Ext] {
			seen[m.data.Ext] = true
			b.s(`<Default Extension="`, m.data.Ext, `" ContentType="`, m.data.ContentType, `"/>`)
		}
	}
	const wml = "application/vnd.openxmlformats-officedocument.wordprocessingml."
	b.s(`<Override PartName="/word/document.xml" ContentType="`, wml, `document.main+xml"/>`,
		`<Override PartName="/word/styles.xml" ContentType="`, wml, `styles+xml"/>`,
		`<Override PartName="/word/settings.xml" ContentType="`, wml, `settings+xml"/>`)
	if hasNums {
		b.s(`<Override PartName="/word/numbering.xml" ContentType="`, wml, `numbering+xml"/>`)
	}
	b.s(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`,
		`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`,
		`</Types>`)
	return b.Bytes()
}

func packageRels() []byte {
	var b buf
	b.s(xmlHeader, `<Relationships xmlns="`, nsPkgRels, `">`,
		`<Relationship Id="rId1" Type="`, relDocument, `" Target="word/document.xml"/>`,
		`<Relationship Id="rId2" Type="`, relCore, `" Target="docProps/core.xml"/>`,
		`<Relationship Id="rId3" Type="`, relApp, `" Target="docProps/app.xml"/>`,
		`</Relationships>`)
	return b.Bytes()
}

func documentRels(rels []relationship) []byte {
	var b buf
	b.s(xmlHeader, `<Relationships xmlns="`, nsPkgRels, `">`)
	for _, r := range rels {
		b.s(`<Relationship Id="`, r.id, `" Type="`, r.typ, `" Target="`)
		b.esc(r.target)
		b.s(`"`)
		if r.external {
			b.s(` TargetMode="External"`)
		}
		b.s(`/>`)
	}
	b.s(`</Relationships>`)
	return b.Bytes()
}

func settingsXML() []byte {
	var b buf
	b.s(xmlHeader, `<w:settings xmlns:w="`, nsW, `">`,
		`<w:zoom w:percent="100"/>`,
		`<w:defaultTabStop w:val="720"/>`,
		`<w:characterSpacingControl w:val="doNotCompress"/>`,
		// Without this Word opens the file in compatibility mode.
		`<w:compat><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat>`,
		`</w:settings>`)
	return b.Bytes()
}

func coreXML(m *model.Meta, created time.Time) []byte {
	var b buf
	b.s(xmlHeader, `<cp:coreProperties`,
		` xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"`,
		` xmlns:dc="http://purl.org/dc/elements/1.1/"`,
		` xmlns:dcterms="http://purl.org/dc/terms/"`,
		` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`)
	field := func(tag, val string) {
		if val != "" {
			b.s(`<`, tag, `>`)
			b.esc(val)
			b.s(`</`, tag, `>`)
		}
	}
	field("dc:title", m.Title)
	field("dc:subject", m.Subject)
	field("dc:creator", m.Author)
	field("cp:keywords", m.Keywords)
	field("dc:description", m.Description)
	if validLang(m.Lang) {
		field("dc:language", m.Lang)
	}
	stamp := created.Format("2006-01-02T15:04:05Z")
	b.s(`<dcterms:created xsi:type="dcterms:W3CDTF">`, stamp, `</dcterms:created>`,
		`<dcterms:modified xsi:type="dcterms:W3CDTF">`, stamp, `</dcterms:modified>`,
		`</cp:coreProperties>`)
	return b.Bytes()
}

func appXML() []byte {
	var b buf
	b.s(xmlHeader, `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties">`,
		`<Application>htmltodocx</Application></Properties>`)
	return b.Bytes()
}

// ---- numbering ----

var bulletGlyphs = [3]string{"•", "◦", "▪"} // bullet, white bullet, small square

func numberingXML(nums []model.NumDef) []byte {
	var b buf
	b.s(xmlHeader, `<w:numbering xmlns:w="`, nsW, `">`)
	// One abstract definition per format; every list instance refers to one
	// and restarts its own counter.
	abstract := map[model.NumFormat]int{}
	for _, n := range nums {
		if _, ok := abstract[n.Format]; ok {
			continue
		}
		id := len(abstract)
		abstract[n.Format] = id
		b.s(`<w:abstractNum w:abstractNumId="`)
		b.i(id)
		b.s(`"><w:multiLevelType w:val="hybridMultilevel"/>`)
		for lvl := 0; lvl <= model.MaxListLevel; lvl++ {
			numFmt, text := string(n.Format), "%"+strconv.Itoa(lvl+1)+"."
			switch n.Format {
			case model.NumBullet:
				numFmt, text = "bullet", bulletGlyphs[lvl%3]
			case model.NumDisc:
				numFmt, text = "bullet", bulletGlyphs[0]
			case model.NumCircle:
				numFmt, text = "bullet", bulletGlyphs[1]
			case model.NumSquare:
				numFmt, text = "bullet", bulletGlyphs[2]
			case model.NumNone:
				numFmt, text = "none", ""
			}
			b.s(`<w:lvl w:ilvl="`)
			b.i(lvl)
			b.s(`"><w:start w:val="1"/><w:numFmt w:val="`, numFmt, `"/><w:lvlText w:val="`, text,
				`"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="`)
			b.i(720 * (lvl + 1))
			b.s(`" w:hanging="360"/></w:pPr></w:lvl>`)
		}
		b.s(`</w:abstractNum>`)
	}
	for _, n := range nums {
		b.s(`<w:num w:numId="`)
		b.i(n.ID)
		b.s(`"><w:abstractNumId w:val="`)
		b.i(abstract[n.Format])
		b.s(`"/>`)
		if n.Format.Ordered() {
			b.s(`<w:lvlOverride w:ilvl="`)
			b.i(n.Level)
			b.s(`"><w:startOverride w:val="`)
			b.i(n.Start)
			b.s(`"/></w:lvlOverride>`)
		}
		b.s(`</w:num>`)
	}
	b.s(`</w:numbering>`)
	return b.Bytes()
}

// ---- styles ----

const bodyFont = "Calibri"

func halfPt(pt float64) string { return strconv.Itoa(int(pt*2 + 0.5)) }

// stylesXML builds the default stylesheet. It is written for this library
// from the WordprocessingML specification; the style names are Word's
// standard built-in names so that Word's style gallery, navigation pane and
// table of contents recognise them.
func stylesXML(doc *model.Document) []byte {
	lang := doc.Meta.Lang
	if !validLang(lang) {
		lang = "en-US"
	}
	def := doc.Defaults
	scale := def.Scale()
	var b buf
	b.s(xmlHeader, `<w:styles xmlns:w="`, nsW, `">`)

	base := halfPt(model.BaseSizePt * scale)
	font := def.Font
	if font == "" {
		font = bodyFont
	}
	line := def.Line
	if line == 0 {
		line = 264
	}
	b.s(`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="`)
	b.esc(font)
	b.s(`" w:hAnsi="`)
	b.esc(font)
	b.s(`" w:eastAsia="`)
	b.esc(font)
	b.s(`" w:cs="`)
	b.esc(font)
	b.s(`"/>`)
	if def.Color != "" {
		b.s(`<w:color w:val="`, def.Color, `"/>`)
	}
	b.s(`<w:sz w:val="`, base, `"/><w:szCs w:val="`, base, `"/>`,
		`<w:lang w:val="`, lang, `"/>`,
		`</w:rPr></w:rPrDefault>`,
		`<w:pPrDefault><w:pPr><w:spacing w:after="160" w:line="`, strconv.Itoa(line), `" w:lineRule="auto"/></w:pPr></w:pPrDefault>`,
		`</w:docDefaults>`)

	// Defaults for each style type.
	b.s(`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>`,
		`<w:style w:type="character" w:default="1" w:styleId="DefaultParagraphFont"><w:name w:val="Default Paragraph Font"/>`,
		`<w:uiPriority w:val="1"/><w:semiHidden/><w:unhideWhenUsed/></w:style>`,
		`<w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/>`,
		`<w:uiPriority w:val="99"/><w:semiHidden/><w:unhideWhenUsed/>`,
		`<w:tblPr><w:tblInd w:w="0" w:type="dxa"/><w:tblCellMar>`,
		`<w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/><w:right w:w="108" w:type="dxa"/>`,
		`</w:tblCellMar></w:tblPr></w:style>`,
		`<w:style w:type="numbering" w:default="1" w:styleId="NoList"><w:name w:val="No List"/>`,
		`<w:uiPriority w:val="99"/><w:semiHidden/><w:unhideWhenUsed/></w:style>`)

	// Headings.
	spaceBefore := [6]string{"360", "280", "240", "200", "200", "200"}
	for i := 0; i < 6; i++ {
		n := strconv.Itoa(i + 1)
		size := halfPt(model.HeadingSizePt[i] * scale)
		b.s(`<w:style w:type="paragraph" w:styleId="`, model.StyleHeadingPrefix, n, `"><w:name w:val="heading `, n, `"/>`,
			`<w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>`,
			`<w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="`, spaceBefore[i], `" w:after="80"/>`,
			`<w:outlineLvl w:val="`, strconv.Itoa(i), `"/></w:pPr>`,
			`<w:rPr><w:b/><w:bCs/>`)
		if i == 5 {
			b.s(`<w:i/><w:iCs/>`)
		}
		b.s(`<w:sz w:val="`, size, `"/><w:szCs w:val="`, size, `"/></w:rPr></w:style>`)
	}

	code := halfPt(model.CodeSizePt * scale)
	caption := halfPt(10 * scale)
	b.s(
		// Block quote.
		`<w:style w:type="paragraph" w:styleId="`, model.StyleQuote, `"><w:name w:val="Quote"/>`,
		`<w:basedOn w:val="Normal"/><w:uiPriority w:val="29"/><w:qFormat/>`,
		`<w:pPr><w:ind w:left="720" w:right="720"/></w:pPr></w:style>`,

		// Code block.
		`<w:style w:type="paragraph" w:styleId="`, model.StyleSourceCode, `"><w:name w:val="Source Code"/>`,
		`<w:basedOn w:val="Normal"/><w:qFormat/>`,
		`<w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F5F5F5"/><w:spacing w:line="240" w:lineRule="auto"/></w:pPr>`,
		`<w:rPr><w:rFonts w:ascii="`, monoFont, `" w:hAnsi="`, monoFont, `" w:eastAsia="`, monoFont, `" w:cs="`, monoFont, `"/>`,
		`<w:noProof/><w:sz w:val="`, code, `"/><w:szCs w:val="`, code, `"/></w:rPr></w:style>`,

		// Captions of figures and tables.
		`<w:style w:type="paragraph" w:styleId="`, model.StyleCaption, `"><w:name w:val="caption"/>`,
		`<w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="35"/><w:qFormat/>`,
		`<w:pPr><w:spacing w:after="200"/></w:pPr>`,
		`<w:rPr><w:i/><w:iCs/><w:sz w:val="`, caption, `"/><w:szCs w:val="`, caption, `"/></w:rPr></w:style>`,

		// List items sit closer together than ordinary paragraphs.
		`<w:style w:type="paragraph" w:styleId="`, model.StyleListParagraph, `"><w:name w:val="List Paragraph"/>`,
		`<w:basedOn w:val="Normal"/><w:uiPriority w:val="34"/><w:qFormat/>`,
		`<w:pPr><w:contextualSpacing/></w:pPr></w:style>`,

		// The term of a definition list.
		`<w:style w:type="paragraph" w:styleId="`, model.StyleDefinitionTerm, `"><w:name w:val="Definition Term"/>`,
		`<w:basedOn w:val="Normal"/><w:next w:val="Normal"/>`,
		`<w:pPr><w:keepNext/><w:spacing w:after="0"/></w:pPr><w:rPr><w:b/><w:bCs/></w:rPr></w:style>`,

		// Inline code.
		`<w:style w:type="character" w:styleId="`, model.StyleVerbatimChar, `"><w:name w:val="Verbatim Char"/>`,
		`<w:rPr><w:rFonts w:ascii="`, monoFont, `" w:hAnsi="`, monoFont, `" w:eastAsia="`, monoFont, `" w:cs="`, monoFont, `"/>`,
		`<w:noProof/><w:sz w:val="`, code, `"/><w:szCs w:val="`, code, `"/></w:rPr></w:style>`,

		// Links.
		`<w:style w:type="character" w:styleId="`, model.StyleHyperlink, `"><w:name w:val="Hyperlink"/>`,
		`<w:uiPriority w:val="99"/><w:unhideWhenUsed/>`,
		`<w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr></w:style>`,

		// Tables. The plain style only tightens paragraph spacing inside
		// cells; the grid style adds borders.
		`<w:style w:type="table" w:styleId="`, model.StyleTablePlain, `"><w:name w:val="Table Plain"/>`,
		`<w:basedOn w:val="TableNormal"/><w:uiPriority w:val="40"/>`,
		`<w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr>`,
		`<w:tblPr><w:tblCellMar>`,
		`<w:top w:w="40" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="40" w:type="dxa"/><w:right w:w="108" w:type="dxa"/>`,
		`</w:tblCellMar></w:tblPr></w:style>`,

		`<w:style w:type="table" w:styleId="`, model.StyleTableGrid, `"><w:name w:val="Table Grid"/>`,
		`<w:basedOn w:val="`, model.StyleTablePlain, `"/><w:uiPriority w:val="39"/>`,
		`<w:tblPr><w:tblBorders>`,
		`<w:top w:val="single" w:sz="4" w:space="0" w:color="A6A6A6"/>`,
		`<w:left w:val="single" w:sz="4" w:space="0" w:color="A6A6A6"/>`,
		`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="A6A6A6"/>`,
		`<w:right w:val="single" w:sz="4" w:space="0" w:color="A6A6A6"/>`,
		`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="A6A6A6"/>`,
		`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="A6A6A6"/>`,
		`</w:tblBorders></w:tblPr></w:style>`,
	)
	b.s(`</w:styles>`)
	return b.Bytes()
}
