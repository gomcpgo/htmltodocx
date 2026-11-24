package svg

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
)

// Elements kept in the embedded copy. <a> becomes <g>; anything else, with
// its content, is dropped: scripts, styles, foreign HTML, external images,
// animation and filters.
var keepElements = map[string]bool{
	"svg": true, "g": true, "defs": true, "symbol": true, "use": true, "switch": true,
	"path": true, "rect": true, "circle": true, "ellipse": true, "line": true, "polyline": true, "polygon": true,
	"text": true, "tspan": true, "title": true, "desc": true,
	"linearGradient": true, "radialGradient": true, "stop": true,
	"clipPath": true, "mask": true, "pattern": true, "marker": true,
}

// textElements are those whose character data is content.
var textElements = map[string]bool{"text": true, "tspan": true, "title": true, "desc": true}

// keepAttrs are the attributes kept: geometry, presentation and ids for
// references inside the drawing. Event handlers, class and style (already
// folded into attributes) are dropped.
var keepAttrs = map[string]bool{
	"id": true, "d": true, "x": true, "y": true, "x1": true, "y1": true, "x2": true, "y2": true,
	"cx": true, "cy": true, "r": true, "rx": true, "ry": true, "fx": true, "fy": true, "fr": true,
	"width": true, "height": true, "points": true, "viewBox": true, "preserveAspectRatio": true,
	"transform": true, "offset": true, "pathLength": true,
	"gradientUnits": true, "gradientTransform": true, "spreadMethod": true,
	"patternUnits": true, "patternContentUnits": true, "patternTransform": true,
	"clipPathUnits": true, "maskUnits": true, "maskContentUnits": true,
	"refX": true, "refY": true, "markerWidth": true, "markerHeight": true, "markerUnits": true, "orient": true,
	"dx": true, "dy": true, "rotate": true, "textLength": true, "lengthAdjust": true,
	"text-anchor": true, "dominant-baseline": true, "alignment-baseline": true, "baseline-shift": true,
	"font-family": true, "font-size": true, "font-weight": true, "font-style": true, "font-variant": true,
	"letter-spacing": true, "word-spacing": true, "text-decoration": true, "writing-mode": true,
	"clip-path": true, "clip-rule": true, "mask": true, "marker-start": true, "marker-mid": true, "marker-end": true,
	"fill": true, "fill-opacity": true, "fill-rule": true,
	"stroke": true, "stroke-width": true, "stroke-opacity": true, "stroke-linecap": true, "stroke-linejoin": true,
	"stroke-miterlimit": true, "stroke-dasharray": true, "stroke-dashoffset": true,
	"opacity": true, "color": true, "display": true, "visibility": true, "overflow": true,
	"stop-color": true, "stop-opacity": true, "vector-effect": true, "paint-order": true, "shape-rendering": true,
}

// localRefs reports whether every url() in v refers inside the drawing.
func localRefs(v string) bool {
	for {
		i := strings.Index(strings.ToLower(v), "url(")
		if i < 0 {
			return true
		}
		ref := strings.TrimLeft(v[i+4:], " \t\"'")
		if !strings.HasPrefix(ref, "#") {
			return false
		}
		v = ref[1:]
	}
}

// Encode writes the drawing as a standalone SVG file of w×h pixels, keeping
// only what draws. currentColor is the colour inherited from the page.
func (im *Image) Encode(w, h float64, currentColor string) []byte {
	var b bytes.Buffer
	root := im.root
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="`)
	b.WriteString(num(w))
	b.WriteString(`" height="`)
	b.WriteString(num(h))
	b.WriteString(`" viewBox="`)
	if vb, ok := viewBox(root); ok {
		b.WriteString(num(vb[0]) + " " + num(vb[1]) + " " + num(vb[2]) + " " + num(vb[3]))
	} else if iw, ih := im.Size(); iw > 0 && ih > 0 {
		b.WriteString("0 0 " + num(iw) + " " + num(ih))
	} else {
		b.WriteString("0 0 " + num(w) + " " + num(h))
	}
	b.WriteString(`"`)
	if currentColor != "" {
		b.WriteString(` color="#` + currentColor + `"`)
	}
	writeAttrs(&b, root, map[string]bool{"width": true, "height": true, "viewBox": true, "x": true, "y": true, "color": currentColor != ""})
	b.WriteString(">")
	for _, k := range root.Kids {
		encodeNode(&b, k, root.Tag)
	}
	b.WriteString("</svg>")
	return b.Bytes()
}

func encodeNode(b *bytes.Buffer, n *Node, parent string) {
	if n.Tag == "" {
		if textElements[parent] {
			escape(b, n.Text)
		}
		return
	}
	tag := n.Tag
	if tag == "a" {
		tag = "g"
	}
	if !keepElements[tag] {
		return
	}
	b.WriteString("<" + tag)
	writeAttrs(b, n, nil)
	if tag == "use" {
		if href := strings.TrimSpace(n.Attr["href"]); strings.HasPrefix(href, "#") {
			b.WriteString(` xlink:href="`)
			escape(b, href)
			b.WriteString(`"`)
		}
	}
	if len(n.Kids) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteString(">")
	for _, k := range n.Kids {
		encodeNode(b, k, tag)
	}
	b.WriteString("</" + tag + ">")
}

// writeAttrs writes the kept attributes of n in name order, so the output
// does not depend on map order.
func writeAttrs(b *bytes.Buffer, n *Node, skip map[string]bool) {
	keys := make([]string, 0, len(n.Attr))
	for k, v := range n.Attr {
		if keepAttrs[k] && !skip[k] && localRefs(v) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.WriteString(" " + k + `="`)
		escape(b, n.Attr[k])
		b.WriteString(`"`)
	}
}

func escape(b *bytes.Buffer, s string) {
	for _, r := range s {
		switch {
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '&':
			b.WriteString("&amp;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20 || r == 0xFFFE || r == 0xFFFF || (r >= 0xD800 && r <= 0xDFFF):
			// Not allowed in XML.
		default:
			b.WriteRune(r)
		}
	}
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
