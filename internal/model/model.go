// Package model is the intermediate document model that sits between the HTML
// reader (internal/dom) and the OOXML writer (internal/ooxml).
//
// The model is layout-free: it records what the document contains and how it
// is formatted, in units that Word understands, but nothing about pages or
// line breaking.
package model

// Shared sizes, in points. The reader needs them to resolve relative CSS font
// sizes and the writer needs them to build the default stylesheet, so they
// live here.
const (
	BaseSizePt = 11.0 // body text
	CodeSizePt = 10.0 // code blocks and inline code
)

// HeadingSizePt holds the font size of the Heading 1..6 styles.
var HeadingSizePt = [6]float64{20, 16, 14, 12, 11, 11}

// Style IDs defined by the built-in stylesheet.
const (
	StyleHeadingPrefix  = "Heading" // followed by 1..6
	StyleQuote          = "Quote"
	StyleSourceCode     = "SourceCode"
	StyleCaption        = "Caption"
	StyleListParagraph  = "ListParagraph"
	StyleDefinitionTerm = "DefinitionTerm"
	StyleVerbatimChar   = "VerbatimChar"
	StyleHyperlink      = "Hyperlink"
	StyleTableGrid      = "TableGrid"
	StyleTablePlain     = "TablePlain"
)

// Tri is a three-state flag. Unset means "whatever the paragraph or
// character style says", which lets a heading stay bold without every run
// repeating it, while still allowing an explicit Off.
type Tri int8

const (
	Unset Tri = iota
	On
	Off
)

// Document is a whole converted document.
type Document struct {
	Meta     Meta
	Defaults Defaults
	Blocks   []Block
	Nums     []NumDef
}

// Defaults is formatting that applies to the whole document, taken from the
// styles of the HTML body. Zero fields mean "use the built-in default".
type Defaults struct {
	Font   string
	Color  string  // RRGGBB
	SizePt float64 // body font size
	Line   int     // line spacing in 240ths of a line
}

// Scale is the ratio of the document's body font size to the built-in one.
// The sizes of the heading and code styles are multiplied by it so they keep
// their proportions.
func (d Defaults) Scale() float64 {
	if d.SizePt <= 0 {
		return 1
	}
	return d.SizePt / BaseSizePt
}

// Meta holds document properties.
type Meta struct {
	Title       string
	Author      string
	Subject     string
	Keywords    string
	Description string
	Lang        string
}

// NumFormat names the marker format of a list.
type NumFormat string

const (
	NumBullet      NumFormat = "bullet" // glyph varies with nesting level
	NumDisc        NumFormat = "disc"
	NumCircle      NumFormat = "circle"
	NumSquare      NumFormat = "square"
	NumDecimal     NumFormat = "decimal"
	NumLowerLetter NumFormat = "lowerLetter"
	NumUpperLetter NumFormat = "upperLetter"
	NumLowerRoman  NumFormat = "lowerRoman"
	NumUpperRoman  NumFormat = "upperRoman"
	NumNone        NumFormat = "none"
)

// Ordered reports whether the format counts items.
func (f NumFormat) Ordered() bool {
	switch f {
	case NumDecimal, NumLowerLetter, NumUpperLetter, NumLowerRoman, NumUpperRoman:
		return true
	}
	return false
}

// MaxListLevel is the deepest list level Word supports (zero-based).
const MaxListLevel = 8

// NumDef is one list instance. Each HTML list gets its own so that numbering
// restarts; bullet lists of the same format may share one.
type NumDef struct {
	ID     int // w:numId, starting at 1
	Format NumFormat
	Level  int // the only level this instance uses
	Start  int
}

// NumRef attaches a paragraph to a list.
type NumRef struct {
	ID    int
	Level int
}

// Block is a block-level node: *Paragraph, *Table or *HorizontalRule.
type Block interface{ block() }

// Paragraph is a paragraph of inline content.
type Paragraph struct {
	Props   ParaProps
	Inlines []Inline
}

// ParaProps is paragraph formatting. Lengths are in twips.
type ParaProps struct {
	Style       string // style ID; empty means Normal
	Align       string // "", "left", "center", "right" or "both"
	IndentLeft  int
	IndentRight int
	FirstLine   int // negative values are a hanging indent
	Line        int // line spacing in 240ths of a line; 0 means unset
	Shade       string
	// Borders are the edges of the box the paragraph is in, or nil. Word
	// draws adjacent paragraphs with identical borders and indents as one
	// box, so every paragraph of a box shares the same values.
	Borders         *[4]Border
	Tabs            []TabStop // custom tab stops, in increasing position
	KeepNext        bool
	PageBreakBefore bool
	Bidi            bool
	Num             *NumRef
}

// TabStop is a custom tab stop. Pos is in twips from the start of the text
// area; Align is "left", "center" or "right".
type TabStop struct {
	Pos   int
	Align string
}

// HorizontalRule is a thematic break.
type HorizontalRule struct {
	IndentLeft int
}

// Border is one edge of a table, cell or paragraph box.
type Border struct {
	Style string // "", "single", "dashed", "dotted" or "double"; "" means none
	Size  int    // eighths of a point
	Color string // RRGGBB, or "" for automatic
	Space int    // points between the border and the text (paragraphs only), 0..31
}

// Sides indexes the edges of a box.
const (
	Top = iota
	Left
	Bottom
	Right
)

// Table is a table whose rows have been normalised: in every row the column
// spans add up to len(Cols).
type Table struct {
	Style   string // StyleTableGrid or StyleTablePlain
	Cols    []int  // column widths in twips
	Rows    []Row
	Indent  int
	Align   string     // "", "center" or "right"
	Width   int        // twips; 0 means automatic
	Borders *[4]Border // outer borders set by CSS, or nil
}

// Row is a table row.
type Row struct {
	Cells  []Cell
	Header bool
}

// VMerge states for vertically merged cells.
const (
	VMergeNone = iota
	VMergeRestart
	VMergeContinue
)

// Cell is a table cell.
type Cell struct {
	Blocks  []Block
	ColSpan int
	VMerge  int
	Width   int    // twips; 0 means automatic
	Shade   string // RRGGBB or ""
	VAlign  string // "", "top", "center" or "bottom"
	Borders *[4]Border
}

func (*Paragraph) block()      {}
func (*Table) block()          {}
func (*HorizontalRule) block() {}

// Inline is an inline node: *Text, *LineBreak, *Link, *Image or *Bookmark.
type Inline interface{ inline() }

// Text is a run of text with uniform formatting. A newline in Text is a line
// break and a tab is a tab stop.
type Text struct {
	Text  string
	Props RunProps
}

// LineBreak is a forced line break.
type LineBreak struct{}

// Link is a hyperlink. Exactly one of URL and Anchor is set.
type Link struct {
	URL     string
	Anchor  string // bookmark name
	Tooltip string
	Inlines []Inline
}

// Image is an inline picture. Width and Height are in EMUs. SVG, when set,
// is the drawing that Data is a rendering of; readers that understand SVG
// show it instead.
type Image struct {
	Data   *ImageData
	SVG    *ImageData
	Width  int64
	Height int64
	Alt    string
}

// ImageData is the encoded image. One ImageData may be shared by several
// Image nodes.
type ImageData struct {
	Bytes       []byte
	Ext         string // "png", "jpeg", "gif" or "svg"
	ContentType string
}

// Bookmark is a named location that internal links can point at.
type Bookmark struct {
	Name string
}

func (*Text) inline()      {}
func (*LineBreak) inline() {}
func (*Link) inline()      {}
func (*Image) inline()     {}
func (*Bookmark) inline()  {}

// Vertical alignment of a run.
const (
	VertNone int8 = iota
	VertSuper
	VertSub
)

// RunProps is character formatting. It is comparable so that adjacent runs
// with equal formatting can be merged.
type RunProps struct {
	Style      string // character style ID
	Font       string
	SizeHalfPt int
	Bold       Tri
	Italic     Tri
	Underline  Tri
	Strike     Tri
	SmallCaps  Tri
	Caps       Tri
	Color      string // RRGGBB
	Shade      string // RRGGBB
	Vert       int8
	RTL        bool
	Lang       string
}
