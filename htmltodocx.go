// Package htmltodocx converts HTML to Word documents (.docx).
//
// It is pure Go and runs no external programs. The conversion maps HTML
// structure onto real Word structure (heading styles, numbered lists,
// tables with merged cells, hyperlinks, bookmarks, embedded images) and maps
// a practical subset of CSS onto Word formatting.
//
//	var out bytes.Buffer
//	err := htmltodocx.Convert(ctx, strings.NewReader(html), &out, nil)
//
// HTML is treated as untrusted input: the package never reads files or
// opens network connections on its own. Images referenced by URL or path
// are embedded only if the caller supplies a [ResourceLoader].
package htmltodocx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/gomcpgo/htmltodocx/internal/dom"
	"github.com/gomcpgo/htmltodocx/internal/ooxml"
)

// Twips is a length in twentieths of a point, the unit Word uses for page
// geometry. There are 1440 twips to the inch.
type Twips int

// Inch is one inch in twips, for building lengths: 2 * htmltodocx.Inch.
const Inch Twips = 1440

// Mm converts millimetres to twips.
func Mm(mm float64) Twips { return Twips(mm*1440/25.4 + 0.5) }

// PageSize is the size of the paper in portrait orientation.
type PageSize struct {
	Width  Twips
	Height Twips
}

// Common paper sizes.
var (
	A4     = PageSize{11906, 16838}
	Letter = PageSize{12240, 15840}
	Legal  = PageSize{12240, 20160}
)

// Margins are the page margins.
type Margins struct {
	Top, Right, Bottom, Left Twips
}

// Page describes the page setup. The zero value is A4 portrait with one
// inch margins.
type Page struct {
	Size      PageSize // zero means A4
	Landscape bool
	Margins   Margins // all zero means one inch on every side
}

// CSSMode selects how much CSS is honoured.
type CSSMode int

const (
	// CSSAll applies <style> elements, linked stylesheets (when a
	// ResourceLoader is set) and style attributes. This is the default.
	CSSAll CSSMode = iota
	// CSSInlineOnly applies style attributes only.
	CSSInlineOnly
	// CSSNone ignores CSS; only HTML semantics are converted.
	CSSNone
)

// ResourceLoader fetches an image or stylesheet the document refers to.
// The URL has been resolved against Options.BaseURL when one is set;
// otherwise it is passed as written in the HTML. data: URIs are decoded by
// the package and never reach the loader.
//
// A loader decides what the conversion may read, so it should refuse
// anything it does not want fetched on behalf of untrusted HTML.
type ResourceLoader func(ctx context.Context, url string) ([]byte, error)

// Warning reports something that could not be converted faithfully.
type Warning struct {
	// Code is a short stable category such as "image" or "table".
	Code string
	// Message describes what happened.
	Message string
}

func (w Warning) String() string { return w.Code + ": " + w.Message }

// Options configures a conversion. A nil *Options means all defaults.
type Options struct {
	// Page is the page setup.
	Page Page

	// CSS selects how much CSS is applied.
	CSS CSSMode

	// BaseURL resolves relative links, image sources and stylesheet
	// references. When it is empty a <base href> in the document is used if
	// present; otherwise references are left as written.
	BaseURL string

	// Resources loads images and stylesheets. When it is nil only data:
	// URIs are embedded; other images are replaced by their alt text.
	Resources ResourceLoader

	// Warn, if set, is called for each thing that was dropped or
	// approximated. Identical warnings are reported once.
	Warn func(Warning)

	// Created is the creation time recorded in the document. The zero value
	// means the current time; set it to get byte-for-byte reproducible
	// output.
	Created time.Time
}

// Convert reads HTML from r and writes a .docx document to w.
//
// The input may be a full document or a fragment, and need not be well
// formed: it is parsed the way browsers parse HTML. Its character encoding
// is detected from a byte order mark or <meta charset>, defaulting to UTF-8.
//
// Convert returns an error only if reading, writing or ctx fails. Markup it
// cannot represent is degraded to plain text rather than rejected.
func Convert(ctx context.Context, r io.Reader, w io.Writer, opts *Options) error {
	var o Options
	if opts != nil {
		o = *opts
	}

	utf8r, err := charset.NewReader(r, "")
	if err != nil && err != io.EOF {
		return fmt.Errorf("htmltodocx: reading input: %w", err)
	}
	if utf8r == nil {
		utf8r = strings.NewReader("")
	}
	// Scripting is treated as disabled so that <noscript> content, the
	// fallback meant for static readers, is parsed as markup.
	root, err := html.ParseWithOptions(utf8r, html.ParseOptionEnableScripting(false))
	if err != nil {
		return fmt.Errorf("htmltodocx: parsing HTML: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	wopt := pageOptions(o.Page)
	wopt.Created = o.Created
	if wopt.Created.IsZero() {
		wopt.Created = time.Now()
	}

	dopt := dom.Options{
		CSS:        int(o.CSS),
		TextWidth:  wopt.PageWidth - wopt.MarginLeft - wopt.MarginRight,
		TextHeight: wopt.PageHeight - wopt.MarginTop - wopt.MarginBottom,
	}
	if o.BaseURL != "" {
		u, err := url.Parse(o.BaseURL)
		if err != nil {
			return fmt.Errorf("htmltodocx: invalid BaseURL: %w", err)
		}
		dopt.BaseURL = u
	}
	if o.Resources != nil {
		dopt.Load = o.Resources
	}
	if o.Warn != nil {
		dopt.Warn = func(code, msg string) { o.Warn(Warning{code, msg}) }
	}

	doc, err := dom.Convert(ctx, root, dopt)
	if err != nil {
		return err
	}
	if err := ooxml.Write(w, doc, wopt); err != nil {
		return fmt.Errorf("htmltodocx: writing document: %w", err)
	}
	return nil
}

// ConvertString converts an HTML string and returns the .docx bytes.
func ConvertString(ctx context.Context, htmlText string, opts *Options) ([]byte, error) {
	var out bytes.Buffer
	if err := Convert(ctx, strings.NewReader(htmlText), &out, opts); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

const minTextArea = 1440 // never let margins squeeze the text below an inch

func pageOptions(p Page) ooxml.Options {
	size := p.Size
	if size.Width <= 0 || size.Height <= 0 {
		size = A4
	}
	w, h := int(size.Width), int(size.Height)
	if p.Landscape {
		w, h = h, w
	}
	m := p.Margins
	if m == (Margins{}) {
		m = Margins{Inch, Inch, Inch, Inch}
	}
	o := ooxml.Options{
		PageWidth:    w,
		PageHeight:   h,
		Landscape:    p.Landscape,
		MarginTop:    max(int(m.Top), 0),
		MarginRight:  max(int(m.Right), 0),
		MarginBottom: max(int(m.Bottom), 0),
		MarginLeft:   max(int(m.Left), 0),
	}
	if w-o.MarginLeft-o.MarginRight < minTextArea {
		o.MarginLeft, o.MarginRight = 0, 0
	}
	if h-o.MarginTop-o.MarginBottom < minTextArea {
		o.MarginTop, o.MarginBottom = 0, 0
	}
	return o
}

// maxResourceBytes caps what DirLoader will read for one resource.
const maxResourceBytes = 64 << 20

// DirLoader returns a ResourceLoader that serves files from dir and nothing
// else. References are taken as paths relative to dir; absolute paths,
// paths that climb out of dir, symbolic links that lead outside it and
// URLs with a scheme are all refused. Use it for HTML saved on disk next to
// its images and stylesheets.
func DirLoader(dir string) ResourceLoader {
	return func(ctx context.Context, ref string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		u, err := url.Parse(ref)
		if err != nil {
			return nil, fmt.Errorf("invalid reference")
		}
		if u.Scheme != "" || u.Host != "" {
			return nil, fmt.Errorf("only local files are loaded")
		}
		name := filepath.FromSlash(u.Path)
		if name == "" || filepath.IsAbs(name) {
			return nil, fmt.Errorf("only paths relative to the document are loaded")
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		f, err := root.Open(name)
		if err != nil {
			return nil, fmt.Errorf("cannot open file")
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxResourceBytes+1))
		if err != nil {
			return nil, fmt.Errorf("cannot read file")
		}
		if len(data) > maxResourceBytes {
			return nil, fmt.Errorf("file is too large")
		}
		return data, nil
	}
}
