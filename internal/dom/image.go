package dom

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif" // register decoders for image.DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/gomcpgo/htmltodocx/internal/css"
	"github.com/gomcpgo/htmltodocx/internal/model"
)

const emuPerPx = 9525

type imageEntry struct {
	data *model.ImageData
	w, h int // intrinsic size, pixels
	err  error
}

func (c *conv) image(n *html.Node, bc bctx, st style, bx box) {
	bc.st = st
	src := strings.TrimSpace(attr(n, "src"))
	alt := attr(n, "alt")
	e := c.loadImage(src)
	if e.err != nil {
		if src != "" {
			name := shorten(src)
			if len(src) >= 5 && strings.EqualFold(src[:5], "data:") {
				name = "in a data: URI"
			}
			c.warn("image", "image "+name+" was replaced by its alt text: "+e.err.Error())
		}
		c.text(alt, &bc)
		return
	}
	if bx.display == "block" {
		c.flushPara()
	}

	// Requested size in CSS pixels; zero means not given.
	availPx := float64(max(bc.avail-bc.indent, 1440)) / 15
	dim := func(l css.Length, has bool, percentOf float64) float64 {
		if !has {
			return 0
		}
		if l.Unit == "%" {
			return percentOf * l.V / 100
		}
		if pt, ok := l.Points(st.emPt, c.rootPt); ok && pt > 0 {
			return pt / 0.75
		}
		return 0
	}
	w := dim(bx.width, bx.hasWidth, availPx)
	h := dim(bx.height, bx.hasHeight, 0)
	iw, ih := float64(e.w), float64(e.h)
	switch {
	case w > 0 && h > 0:
	case w > 0:
		h = w * ih / iw
	case h > 0:
		w = h * iw / ih
	default:
		w, h = iw, ih
	}
	// Never let a picture overflow the text area.
	if w > availPx {
		h, w = h*availPx/w, availPx
	}
	if maxH := float64(c.opt.TextHeight) / 15; maxH > 0 && h > maxH {
		w, h = w*maxH/h, maxH
	}

	img := &model.Image{
		Data:   e.data,
		Width:  max(int64(w*emuPerPx), emuPerPx),
		Height: max(int64(h*emuPerPx), emuPerPx),
		Alt:    strings.TrimSpace(alt),
	}
	c.ensurePara(&bc)
	c.para.items = append(c.para.items, item{kind: itemImage, img: img, link: bc.link})
	c.para.lastSpace = false
	if bx.display == "block" {
		c.flushPara()
	}
}

func (c *conv) loadImage(src string) *imageEntry {
	if e, ok := c.images[src]; ok {
		return e
	}
	e := &imageEntry{}
	c.images[src] = e
	raw, err := c.fetchImage(src)
	if err != nil {
		e.err = err
		return e
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		e.err = errors.New("not a PNG, JPEG or GIF image")
		return e
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		e.err = errors.New("image has no size")
		return e
	}
	e.w, e.h = cfg.Width, cfg.Height
	sum := sha256.Sum256(raw)
	if d, ok := c.imageData[sum]; ok {
		e.data = d
		return e
	}
	e.data = &model.ImageData{Bytes: raw, Ext: format, ContentType: "image/" + format}
	c.imageData[sum] = e.data
	return e
}

func (c *conv) fetchImage(src string) ([]byte, error) {
	if src == "" {
		return nil, errors.New("no src")
	}
	if len(src) >= 5 && strings.EqualFold(src[:5], "data:") {
		return decodeDataURI(src[5:])
	}
	if c.opt.Load == nil {
		return nil, errors.New("no resource loader is configured")
	}
	target, ok := c.resolve(src)
	if !ok {
		return nil, errors.New("invalid URL")
	}
	return c.opt.Load(c.ctx, target)
}

// decodeDataURI decodes the part of a data: URI after the scheme.
func decodeDataURI(s string) ([]byte, error) {
	meta, payload, ok := strings.Cut(s, ",")
	if !ok {
		return nil, errors.New("malformed data URI")
	}
	if n := len(meta); n >= 7 && strings.EqualFold(meta[n-7:], ";base64") {
		payload = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, payload)
		if u, err := url.PathUnescape(payload); err == nil {
			payload = u
		}
		payload = strings.TrimRight(payload, "=")
		b, err := base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			b, err = base64.RawURLEncoding.DecodeString(payload)
		}
		if err != nil {
			return nil, errors.New("malformed base64 in data URI")
		}
		return b, nil
	}
	u, err := url.PathUnescape(payload)
	if err != nil {
		return nil, errors.New("malformed data URI")
	}
	return []byte(u), nil
}
