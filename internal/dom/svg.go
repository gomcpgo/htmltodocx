package dom

import (
	"bytes"
	"errors"
	"image/png"
	"math"
	"strings"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
	"github.com/gomcpgo/htmltodocx/internal/model"
	"github.com/gomcpgo/htmltodocx/internal/svg"
)

// SVG is embedded twice: the drawing itself, which Word 2016 and later
// show, and a PNG rendering for readers that do not understand SVG.

// rasterScale is how many image pixels a CSS pixel gets in the PNG, so the
// fallback stays sharp when printed.
const rasterScale = 2

// maxRasterPixels bounds the PNG pixels rendered in one conversion, about
// thirty drawings at the largest size. Past it, drawings get a one-pixel
// fallback; Word still shows the SVG.
const maxRasterPixels = 32 << 20

// svgPair renders a drawing shown at w×h CSS pixels and returns its PNG and
// its cleaned SVG.
func (c *conv) svgPair(im *svg.Image, w, h float64, currentColor string) (pngData, svgData *model.ImageData, ok bool) {
	s := float64(rasterScale)
	if m := math.Max(w, h) * s; m > svg.MaxRaster {
		s = svg.MaxRaster / math.Max(w, h)
	}
	pw, ph := max(1, int(math.Round(w*s))), max(1, int(math.Round(h*s)))
	if c.rasterPixels += pw * ph; c.rasterPixels > maxRasterPixels {
		c.warn("svg", "the document has too many large SVG drawings; later ones have no fallback image for readers without SVG support")
		pw, ph = 1, 1
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im.Render(pw, ph, currentColor)); err != nil {
		return nil, nil, false
	}
	pngData = c.imageBytes(buf.Bytes(), "png", "image/png")
	svgData = c.imageBytes(im.Encode(w, h, currentColor), "svg", "image/svg+xml")
	return pngData, svgData, true
}

// defaultSVGSize is the size a browser gives an SVG that states none.
const defaultSVGWidth, defaultSVGHeight = 300.0, 150.0

func svgOwnSize(im *svg.Image) (w, h float64) {
	w, h = im.Size()
	if w <= 0 {
		w = defaultSVGWidth
	}
	if h <= 0 {
		h = defaultSVGHeight
	}
	return w, h
}

// loadSVGImage fills e for an <img> whose source is an SVG file. Like a
// browser, the drawing does not inherit the page's colour.
func (c *conv) loadSVGImage(e *imageEntry, raw []byte) {
	root, err := svg.ParseXML(raw)
	if err != nil {
		e.err = err
		return
	}
	im := svg.New(root)
	e.w, e.h = svgOwnSize(im)
	var ok bool
	if e.data, e.svg, ok = c.svgPair(im, e.w, e.h, ""); !ok {
		e.err = errBadSVG
	}
}

var errBadSVG = errors.New("the drawing could not be rendered")

// inlineSVG adds an <svg> element of the page as a picture.
func (c *conv) inlineSVG(n *html.Node, bc bctx) {
	st, bx := c.computeStyle(n, &bc.st)
	if bx.display == "none" {
		return
	}
	bc.st = st
	var rules func(*html.Node) []css.Decl
	if c.sheet != nil {
		rules = c.svgRules
	}
	root, err := svg.FromHTML(n, rules)
	if err != nil {
		c.warn("svg", "an inline SVG drawing was skipped: "+err.Error())
		return
	}
	im := svg.New(root)
	if im.Empty() {
		return
	}
	alt := im.Title()
	if strings.EqualFold(attr(n, "aria-hidden"), "true") {
		alt = ""
	}
	iw, ih := svgOwnSize(im)
	w, h := c.imageSize(bc, st, bx, iw, ih)
	color := st.color
	if color == "" {
		color = c.doc.Defaults.Color
	}
	pngData, svgData, ok := c.svgPair(im, w, h, color)
	if !ok {
		c.warn("svg", "an inline SVG drawing was skipped: "+errBadSVG.Error())
		c.text(alt, &bc)
		return
	}
	c.addImage(bc, bx, &model.Image{Data: pngData, SVG: svgData, Alt: alt}, w, h)
}

// svgRules returns the page's stylesheet declarations for an element of an
// inline drawing, normal ones before important ones.
func (c *conv) svgRules(n *html.Node) []css.Decl {
	c.matchBuf = c.sheet.Match(n, c.matchBuf[:0])
	var out []css.Decl
	for pass := 0; pass < 2; pass++ {
		for i := range c.matchBuf {
			for _, d := range c.matchBuf[i].Decls {
				if d.Important == (pass == 1) {
					out = append(out, d)
				}
			}
		}
	}
	return out
}
