// Package svg reads SVG drawings, renders them to images, and writes a
// cleaned copy for embedding.
//
// SVG in a document is untrusted. Rendering is bounded in nodes, points and
// pixels, and the copy written for Word keeps only drawing elements and
// attributes: no scripts, no event handlers, and no reference outside the
// drawing itself.
package svg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
)

// Limits on the drawings accepted.
const (
	maxNodes   = 20000   // elements and text nodes
	maxDepth   = 128     // nesting
	maxXMLSize = 8 << 20 // bytes of an SVG file
)

const nsSVG = "http://www.w3.org/2000/svg"

// Node is an element of a drawing, or a text node when Tag is "".
type Node struct {
	Tag  string
	Attr map[string]string // attributes, with the element's CSS folded in
	Text string            // the text of a text node
	Kids []*Node
}

func (n *Node) attr(k string) string { return n.Attr[k] }

var errTooBig = errors.New("the drawing is too large or too deeply nested")

// FromHTML converts an <svg> element parsed as part of an HTML page. rules
// returns the stylesheet declarations that apply to an element, or nil.
func FromHTML(root *html.Node, rules func(*html.Node) []css.Decl) (*Node, error) {
	count := 0
	var conv func(h *html.Node, depth int) (*Node, error)
	conv = func(h *html.Node, depth int) (*Node, error) {
		count++
		if count > maxNodes || depth > maxDepth {
			return nil, errTooBig
		}
		n := &Node{Tag: h.Data, Attr: make(map[string]string, len(h.Attr))}
		var style string
		for _, a := range h.Attr {
			key := a.Key
			switch a.Namespace {
			case "":
			case "xlink":
				if key != "href" {
					continue
				}
			default:
				continue
			}
			if key == "style" {
				style = a.Val
				continue
			}
			n.Attr[key] = a.Val
		}
		if rules != nil {
			n.applyDecls(rules(h))
		}
		n.applyDecls(css.ParseDeclarations(style))
		for c := h.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				count++
				n.Kids = append(n.Kids, &Node{Text: c.Data})
			case c.Type == html.ElementNode && c.Namespace == "svg":
				k, err := conv(c, depth+1)
				if err != nil {
					return nil, err
				}
				n.Kids = append(n.Kids, k)
			}
		}
		return n, nil
	}
	return conv(root, 0)
}

// applyDecls copies CSS declarations onto the attributes: a property set in
// CSS wins over the presentation attribute of the same name.
func (n *Node) applyDecls(decls []css.Decl) {
	for _, d := range decls {
		if css.IsCustomProperty(d.Prop) || css.HasVar(d.Value) {
			continue
		}
		n.Attr[d.Prop] = d.Value
	}
}

// ParseXML reads a standalone SVG file.
func ParseXML(data []byte) (*Node, error) {
	if len(data) > maxXMLSize {
		return nil, errTooBig
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(charset string, r io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "us-ascii") {
			return r, nil
		}
		return nil, errors.New("unsupported charset " + charset)
	}
	var root *Node
	var stack []*Node
	skip := 0 // depth inside an element from another namespace
	count := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if root != nil {
				break // keep what was read, as a browser would draw it
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 || (t.Name.Space != "" && t.Name.Space != nsSVG) || (root == nil && t.Name.Local != "svg") {
				skip++
				continue
			}
			count++
			if count > maxNodes || len(stack) >= maxDepth {
				return nil, errTooBig
			}
			n := &Node{Tag: t.Name.Local, Attr: make(map[string]string, len(t.Attr))}
			var style string
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "":
				case a.Name.Local == "href" && (a.Name.Space == "http://www.w3.org/1999/xlink" || a.Name.Space == "xlink"):
				default:
					continue
				}
				if a.Name.Local == "style" {
					style = a.Value
					continue
				}
				n.Attr[a.Name.Local] = a.Value
			}
			n.applyDecls(css.ParseDeclarations(style))
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Kids = append(p.Kids, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if skip == 0 && len(stack) > 0 {
				count++
				p := stack[len(stack)-1]
				p.Kids = append(p.Kids, &Node{Text: string(t)})
			}
		}
	}
	if root == nil {
		return nil, errors.New("no <svg> element")
	}
	return root, nil
}

// Looks reports whether data looks like an SVG file rather than a bitmap.
func Looks(data []byte) bool {
	head := data[:min(len(data), 1024)]
	return bytes.Contains(head, []byte("<svg"))
}
