# htmltodocx

Convert HTML to Word documents (`.docx`) in pure Go.

- **No external programs.** No office suite, converter or headless browser at runtime.
- **Real Word structure.** Headings use heading styles, lists are numbered lists, tables have merged cells, links are hyperlinks.
- **CSS aware.** Style attributes, `<style>` blocks and linked stylesheets are mapped onto Word formatting.
- **Safe on untrusted input.** Malformed HTML is parsed the way browsers parse it. The library never reads files or the network unless you hand it a loader.
- **One dependency:** `golang.org/x/net` (for the HTML5 parser).

Status: v0.1, first version. The API may still change.

## Install

```
go get github.com/gomcpgo/htmltodocx
```

Requires Go 1.26 or later.

## Library

```go
out, err := os.Create("report.docx")
if err != nil {
    return err
}
defer out.Close()

err = htmltodocx.Convert(ctx, strings.NewReader(html), out, &htmltodocx.Options{
    Page:      htmltodocx.Page{Size: htmltodocx.Letter},
    Resources: htmltodocx.DirLoader("./assets"), // where images and stylesheets may be read from
    Warn:      func(w htmltodocx.Warning) { log.Println(w) },
})
```

`ConvertString(ctx, html, opts)` returns the document as bytes. Passing `nil` options gives A4 with one inch margins and all CSS applied.

| Option | Meaning |
|---|---|
| `Page` | Paper size, orientation and margins. |
| `CSS` | `CSSAll` (default), `CSSInlineOnly` or `CSSNone`. |
| `BaseURL` | Resolves relative links, images and stylesheets. |
| `Resources` | Loads images and stylesheets. Without it only `data:` images are embedded and other images become their alt text. |
| `Warn` | Called for anything dropped or approximated. |
| `Created` | Creation time written to the file. Set it for byte-for-byte reproducible output. |

## Command line

```
go install github.com/gomcpgo/htmltodocx/cmd/html2docx@latest

html2docx report.html                  # writes report.docx
html2docx report.html -o out.docx -page letter -landscape
html2docx - -o out.docx < report.html
```

The command loads images and stylesheets only from the input file's directory (or `-resources DIR`) and never from the network. Run `html2docx -h` for all flags.

## What is converted

| HTML | Word |
|---|---|
| `h1`–`h6` | Heading 1–6 styles, with a bookmark for the `id` |
| `p`, `div`, sections | Paragraphs |
| `strong`, `em`, `u`, `s`, `sub`, `sup`, `small`, `mark`, `code`, `kbd`, `q`, `font` | Character formatting |
| `a href` | Hyperlinks; `#fragment` links become internal links to bookmarks |
| `ul`, `ol` | Real lists: nesting, `start`, `type`, `list-style-type`, restart per list |
| `<input type="checkbox">` | ☐ / ☒ |
| `dl`, `dt`, `dd` | Term and indented definition |
| `blockquote` | Quote style, nested by indent |
| `pre` | Source Code style, line breaks and tabs kept |
| `table` | Tables with `colspan`, `rowspan`, `thead` repeat-header rows, `caption`, column widths, borders and shading, nested tables |
| `img` | Embedded PNG, JPEG and GIF from `data:` URIs or the resource loader, sized from attributes, CSS or the image itself, and never wider than the page |
| `figure`, `figcaption` | Picture kept with its Caption-style paragraph |
| `hr` | Horizontal rule |
| `title`, `meta`, `html lang` | Document properties and language |
| `lang`, `dir="rtl"` | Run language and right-to-left paragraphs |

### CSS

Selectors: type, class, id, attribute (`[a]`, `=`, `~=`, `|=`, `^=`, `$=`, `*=`), descendant, child `>`, sibling `+` and `~`, `:first-child`, `:last-child`, `:only-child`, `:nth-child()`, `:first-of-type`, `:last-of-type`, `:not()`, `:root`, `:link`, `:empty`. Specificity, source order and `!important` are honoured. `@media print` and `@media all` blocks apply; screen-only and width-dependent blocks do not.

Properties: `color`, `background-color`, `background`, `background-image`, `font-family`, `font-size`, `font-weight`, `font-style`, `font` , `font-variant: small-caps`, `text-transform`, `text-decoration`, `text-align`, `vertical-align`, `line-height`, `text-indent`, `margin-left`, `white-space`, `display` (`none`, block, inline, `flex`, `grid`, `inline-block`), `flex-direction`, `justify-content`, `grid-template-columns`, `gap`, `visibility`, `direction`, `list-style-type`, `page-break-before/after`, `break-before/after`, `width`, `height`, and `border` in all its forms on tables and cells.

Flex and grid layouts are approximated, since Word paragraphs cannot sit side by side. Items always stay separate. A row of short items stays on one line: spread items (`justify-content: space-between`) are split by tabs with a right tab stop at the margin, packed items by a space. A grid of short items gives one line per grid row with tab stops at the columns. Items holding block content (cards, panels) become one block each. `inline-block` boxes keep a space from the words around them.

Backgrounds become shading. A gradient is drawn as the average of its colours, and translucent colours are blended over whatever is behind them, as on screen. Text whose colour would be practically invisible on the background actually drawn (for example light text meant for a page background or image, neither of which Word can show) falls back to Word's automatic colour, with a warning.

Custom properties (`--name: value`) and `var(--name, fallback)` work in all of these. Variables cascade and inherit as in a browser and are resolved where they are defined. A `var()` that cannot be resolved drops its declaration; browsers would reset the property instead.

Font, colour, size and line height set on `body` become the document's defaults rather than being repeated on every run, so the result stays small and easy to restyle in Word.

## Not yet supported

Footnotes, syntax highlighting, a reference `.docx` for custom styles, table of contents fields, SVG, MathML (its text is kept), floats and positioning, `calc()`, cell padding. Unsupported markup degrades to its text and is reported through `Warn`.

HTML nested more than 512 elements deep is rejected by the parser and `Convert` returns an error.

## Untrusted input

- The library opens no files and makes no network requests by itself. `Resources` is the only way anything outside the HTML is read, and `DirLoader` confines reads to one directory (no absolute paths, no `..`, no escaping symlinks).
- `javascript:` and other script-like link schemes are dropped.
- Work is bounded for hostile input (selector matching, nesting depth), and `ctx` cancellation is honoured. For input from strangers, set a deadline on `ctx` and limit the input size.

## Testing

```
go test ./...                              # unit, golden, schema and round-trip tests
go test -update .                          # rewrite golden files after an intended change
go test -fuzz FuzzConvert -fuzztime 1m .   # fuzzing
go test -bench . .
```

Two tests use tools when they are installed and skip otherwise: `xmllint` validates the output against the ECMA-376 schema, and an independent DOCX reader reads the output back as a round-trip check. See [docs/design.md](docs/design.md).

## Licence and provenance

Apache-2.0. See [LICENSE](LICENSE).

This is a clean-room implementation written from the public specifications (ECMA-376, the HTML Standard, CSS). It contains no code, data or style definitions from any other converter. The rules that keep it that way are in [docs/design.md](docs/design.md).
