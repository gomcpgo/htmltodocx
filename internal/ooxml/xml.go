// Package ooxml writes the document model as a WordprocessingML package
// (.docx), following ECMA-376.
package ooxml

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// Namespaces.
const (
	nsW   = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsR   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsWP  = "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
	nsA   = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsPic = "http://schemas.openxmlformats.org/drawingml/2006/picture"

	nsPkgRels      = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsContentTypes = "http://schemas.openxmlformats.org/package/2006/content-types"

	relDocument  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	relCore      = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	relApp       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties"
	relStyles    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	relSettings  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings"
	relNumbering = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	relHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	relImage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
)

// buf is a byte buffer with helpers for writing XML by hand. Writing the
// markup directly is much faster than encoding/xml and gives exact control
// over element order, which the schema fixes.
type buf struct {
	bytes.Buffer
}

func (b *buf) s(parts ...string) {
	for _, p := range parts {
		b.WriteString(p)
	}
}

func (b *buf) i(v int) {
	var tmp [20]byte
	b.Write(strconv.AppendInt(tmp[:0], int64(v), 10))
}

func (b *buf) i64(v int64) {
	var tmp [20]byte
	b.Write(strconv.AppendInt(tmp[:0], v, 10))
}

// validXMLChar reports whether r may appear in an XML 1.0 document.
func validXMLChar(r rune) bool {
	return r == 0x9 || r == 0xA || r == 0xD ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

// esc writes s as XML text or as an attribute value, escaping markup and
// dropping characters XML cannot represent.
func (b *buf) esc(s string) {
	last := 0
	for i := 0; i < len(s); {
		c := s[i]
		var rep string
		width := 1
		switch {
		case c == '&':
			rep = "&amp;"
		case c == '<':
			rep = "&lt;"
		case c == '>':
			rep = "&gt;"
		case c == '"':
			rep = "&quot;"
		case c == '\t':
			rep = "&#9;"
		case c == '\n':
			rep = "&#10;"
		case c == '\r':
			rep = "&#13;"
		case c < 0x20:
			rep = ""
		case c < utf8.RuneSelf:
			i++
			continue
		default:
			r, w := utf8.DecodeRuneInString(s[i:])
			width = w
			if r == utf8.RuneError && w == 1 {
				rep = "�"
			} else if validXMLChar(r) {
				i += w
				continue
			}
		}
		b.WriteString(s[last:i])
		b.WriteString(rep)
		i += width
		last = i
	}
	b.WriteString(s[last:])
}
