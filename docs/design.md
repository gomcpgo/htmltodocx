# Design notes

This file records how htmltodocx is built and why. It doubles as the record that the work is independent: every decision here was made from the public specifications and from looking at how documents behave, not from another converter's source.

## Clean-room rules

1. **Sources are the specifications:** ECMA-376 (Office Open XML), the WHATWG HTML Standard, the CSS specifications.
2. **Nothing other tools write is committed.** Files produced by other converters or word processors live only in the git-ignored `oracle/` folder.
3. **Test inputs are our own.** Everything in `testdata/` was written for this project. Golden files are produced by this library.
4. **Dependencies must be permissively licensed.** Today that is only `golang.org/x/net`, `golang.org/x/text`, `golang.org/x/image` and, through it on amd64, `golang.org/x/sys/cpu` (all BSD-3-Clause, from the Go project).
5. **The default stylesheet is ours.** `internal/ooxml/package.go` builds `styles.xml` and `numbering.xml` from the specification. Style *names* such as "heading 1" and "Table Grid" are Word's standard built-in names, used so that Word's navigation pane, style gallery and table of contents recognise them. Their definitions are our own.

## Pipeline

```
HTML bytes
  │ charset detection, golang.org/x/net/html (HTML5 parsing)
  ▼
DOM
  │ internal/dom: block/inline separation, white-space collapsing,
  │ style cascade (internal/css), list numbering, table grid
  ▼
internal/model: Blocks and Inlines with resolved formatting, in Word units
  │
  ▼
internal/ooxml: document.xml, styles.xml, numbering.xml, rels, media
  ▼
zip → .docx
```

The model is the seam. The reader knows nothing about XML and the writer knows nothing about HTML or CSS, so each can be tested and replaced alone. A future reference-docx feature only touches the writer; a future Markdown reader would only produce the model.

## Decisions

### Public API

- `Convert(ctx, io.Reader, io.Writer, *Options)` streams in and out. `nil` options mean defaults, and every zero value is a sensible default (A4, one inch margins, all CSS).
- **No I/O unless asked.** HTML is often untrusted. An `<img src>` or `<link href>` must not be able to make the library read a file or call a server. All such access goes through `Options.Resources`, which the caller supplies. `DirLoader` is the safe ready-made choice for files on disk; it uses `os.Root`, so paths cannot escape the directory even through symlinks.
- **Errors are for I/O, not for content.** Anything the converter cannot represent degrades to text and is reported through `Options.Warn`. The one exception is the HTML parser's limit of 512 nested elements, which surfaces as an error.

### Formatting model

- **Three-state toggles.** Bold, italic and friends are `Unset`, `On` or `Off`. `Unset` means "as the Word style says". A heading's runs are therefore not marked bold (the Heading style is), yet `font-weight: normal` inside a heading can still switch bold off explicitly. "Off" is only written where something would otherwise turn the property on.
- **Body styles become document defaults.** `body { font-family; color; font-size; line-height }` is extremely common. Writing it on every run would bloat the file and make it painful to restyle, so it is hoisted into `docDefaults`. Heading and code sizes scale with the body size to keep their proportions.
- **Font sizes.** Absolute units are converted exactly (1px = 0.75pt). `em` and `%` are relative to the parent. With no CSS, text is 11pt. `rem` and the size keywords scale from the root size, which is 11pt unless `html` sets one.
- **Page background and page margins are ignored.** `background` and `margin` on `html` and `body` describe the browser page, not paragraphs.
- **White backgrounds are dropped** when nothing behind them is coloured. Many pages set `background: white` on wrappers; shading every paragraph white would be noise.
- **Backgrounds are single colours.** Word shading cannot draw images or gradients. A gradient becomes the average of its stops (weighted by alpha, so transparent stops thin it rather than darken it), which is closest to the tone a reader sees; a gradient layer wins over a background colour in the same declaration because it is painted on top. Each element carries the colour drawn behind it (its backdrop), and translucent backgrounds and text colours are blended over that, not over white: a white 12% pill on a navy header is dark, not near-white.
- **Unreadable text gets the automatic colour.** If a run's colour has a WCAG contrast ratio below 1.3 against the backdrop actually drawn, the colour is dropped with a warning, and Word's automatic colour (black, or white on dark shading) is used. The threshold is low on purpose, so faint but visible text is left alone. The same check applies to a body colour moved into the document defaults, since the page background is never drawn. Before this, model-written pages with gradient banners or dark page backgrounds produced white text on a white page.

### White space and blocks

The reader follows the CSS model: runs of collapsible white space become one space, including across inline element boundaries; space at the start and end of a block disappears; a `<br>` at the end of a block adds no line unless it is the block's only content. Inline content that is a direct child of a block container forms an implicit paragraph. A block element inside an inline one splits the paragraph, and the inline formatting continues on both sides.

Paragraphs are collected as a flat list of items and trimmed when the block ends. Text is appended to a byte slice, not concatenated, so thousands of small text nodes stay linear.

### Boxes: borders and padding

A block with a border, or with a background and padding, becomes paragraph borders (`w:pBdr`) on every paragraph inside it (`boxBorders` in `internal/dom/convert.go`). Word draws adjacent paragraphs with identical borders and indents as one box, so all paragraphs of a CSS box share one set of borders and one pair of indents; a callout of three paragraphs is one box in Word.

- **Padding becomes the border's distance from the text** (`w:space`, in points). Word caps it at 31pt, so larger padding is capped.
- **Indents grow by each border's width plus its spacing.** Word draws a paragraph border outside the indent, so this puts the border where the CSS box starts rather than in the margin. The right inset also narrows the width that tables, images and tab stops are fitted to. However deep boxes nest, at least an inch is left for the text.
- **Padding on a shaded side without a border** is drawn as a hairline border in the background colour. Shading fills a paragraph only up to its borders, so this is what makes the background cover the padding, as in a browser. With no background, padding on a side without a border draws nothing and is ignored, so wrappers such as `.container { padding: 20px }` do not indent the whole document.
- **Sibling boxes are kept apart.** Two cards in a row, or list items that each have a rule under them, would otherwise merge into one box, with one rule under the last item. When a box's first paragraph follows a paragraph with equal borders from another box, the lowest bit of its border colours is flipped: invisible, but enough to make the borders differ.
- **A nested box replaces the outer box's borders** for its own paragraphs. A paragraph has one set of borders, and an inner box drawn at its own indent reads better than the outer box's borders around it.
- **Rounded corners and shadows are dropped silently.** They are decoration, and Word cannot draw them on paragraphs.
- **Not attempted:** borders on `html`, `body` and inline elements; percentage padding; vertical padding on a side that draws nothing.

Limitation: a box whose content has different indents, such as a list or a nested box, is drawn by Word as several boxes stacked, since Word only joins paragraphs whose indents match. A table also splits a box, and is not inside its border. A single-cell table would hold any content, but it would turn every bordered section of a document, and often the whole page wrapper, into a table, which is worse to edit and to break across pages.

### Generated content

`::before` and `::after` (and the older one-colon forms) are supported because models use them for text the reader sees: checkbox and arrow bullets, "Note:" labels, and clause numbers built from counters. The engine keeps rules for each pseudo-element in their own index (`internal/css/css.go`), so an ordinary element never tests them. A pseudo-element inherits from its element, gets its own declarations through the same cascade, and its text is added at the start or end of the element's content with its own run formatting (`internal/dom/generated.go`).

- **Content:** strings with CSS escapes, `counter()`, `counters()`, `attr()`, `open-quote` and `close-quote`. `url()` images are skipped with a warning. Empty content draws nothing, which covers the many decorative bars and dots built from `content: ""` with a background or border.
- **Counters** follow CSS scoping: a reset applies to the element, its following siblings and their descendants; a reset on a sibling replaces the sibling's counter; using a counter that is not in scope creates one. Reset, increment and set run in that order. Elements that are not displayed do not count. Counter styles are decimal, decimal-leading-zero, the latin, alpha and roman styles, disc, circle, square and none.
- **Display:** a `display: block` pseudo-element is its own paragraph; any other is inline. One that is absolutely positioned (a marker placed in the padding) is kept a space apart from the text, since in a browser it does not touch it.
- **Not supported:** pseudo-elements on lists and table cells, the built-in `list-item` counter, and `::marker`, `::first-line` and `::first-letter`.
- **Bounds:** generated text is limited to 4 MB per conversion, since `counters()` on a deeply nested counter read by many elements is quadratic. Counter lookups only check the innermost instance, because instances are pushed in document order and each scope lies inside the one below it.

### SVG

Inline `<svg>` and SVG files in `<img>` become pictures with two parts: the drawing itself, attached through the `svgBlip` extension that Word 2016 introduced, and a PNG rendering that the picture names as its image. Word shows the SVG, sharp at any size; readers that do not know the extension show the PNG. This is the only way DOCX carries SVG, and how Word itself saves it. The code is in `internal/svg`, and `internal/dom/svg.go` connects it.

- **The embedded copy is rebuilt, not passed through** (`internal/svg/encode.go`). SVG is a document format with scripts, event handlers, external images and stylesheets. The copy keeps an allowlist of drawing elements and presentation attributes, links only to ids inside the drawing (`url(#id)`, `href="#id"`), and folds CSS that the page applied to the drawing into attributes. `<a>` becomes `<g>`. Attributes are written in name order so output stays reproducible.
- **The PNG is drawn by a small renderer** on `golang.org/x/image/vector`: paths (all commands, arcs included), basic shapes, `<use>` and `<symbol>`, transforms, viewBox and `preserveAspectRatio`, fill and stroke colours, opacity and `currentColor`. Fills use the nonzero rule. Strokes are built as quads and round joins wound the same way, so a translucent stroke is painted once where pieces overlap. Gradients become the average of their stops, as backgrounds do. Not drawn: text (it needs fonts), clip paths, masks, filters, patterns, markers and dashes. The PNG is twice the CSS size, at most 1024 pixels a side.
- **Size** comes from CSS, then the `width` and `height` attributes, then the viewBox, then the browser default of 300×150. `currentColor` is the text colour around an inline drawing; an SVG file in `<img>` does not inherit it, as in a browser.
- **Alt text** is the drawing's `<title>` or `aria-label`; `aria-hidden="true"` makes it decorative. Drawings with nothing to draw, such as sprite sheets of `<symbol>`s with zero size, are skipped silently.
- **Bounds:** 20,000 nodes and 128 levels per drawing, 200,000 points after flattening, 50,000 elements drawn counting `<use>` again, `<use>` nested 8 deep, 64 megapixels of fill work per drawing, and 32 megapixels of PNG per conversion. Past the last, drawings get a one-pixel PNG with a warning; Word still shows the SVG.

### Flex and grid

Word has no side-by-side layout for paragraphs, but flex and grid items must at least stay apart: in a browser each item is its own box, so `<span>Core</span><span>Go</span>` in a grid reads as two cells, while the plain white-space rules would glue it into "CoreGo". The converter follows CSS in treating every item as a separate box (text runs that are only white space are not items, empty items are skipped) and approximates the arrangement in `internal/dom/layout.go`:

- If any item is block-level, or the flex direction is a column, or a grid has one column (the CSS default) or a width-dependent column count (`auto-fill`, `auto-fit`), every item becomes its own block. CSS "blockifies" flex and grid items, so this is the faithful reading when there is no horizontal layout.
- A flex row of inline items stays one paragraph. With `justify-content: space-between`, `space-around` or `space-evenly`, items are separated by tabs; the last stop is right-aligned at the right edge and any in between are centred, which reproduces the common "title … date" line. Otherwise items are separated by a space, standing in for `gap`, and `justify-content: center` or `flex-end` aligns the paragraph.
- A grid of inline items becomes one paragraph per grid row, items separated by tabs, with left tab stops where the columns start. Column widths come from `grid-template-columns` (lengths, percentages, `fr`, `minmax()` by its maximum, `repeat()` with a fixed count) and the column gap. At most 32 columns are taken from `repeat()`.
- `inline-block`, `inline-flex` and `inline-grid` boxes stay inline, with a space inserted when a letter or digit would otherwise touch them, so badges and pills do not run into the next word ("1 Summary", not "1Summary").

Tab stops are positions from the start of the text area, so they are computed from the paragraph indent and the width available in the current context (page or table cell).

### Lists

Each HTML list becomes its own `w:num`, pointing at an abstract definition shared by every list of the same format. A separate `w:num` with a start override is how Word restarts numbering, so two `<ol>` elements never run on from each other, and `start` works. Bullet lists of one format share a single `w:num`.

The first paragraph of an `<li>` carries the number. Further blocks in the same item (paragraphs, code, tables) are indented to the item's text but not numbered. An item with no text of its own, such as one that starts with a nested list, still gets its marker, as in a browser.

### Tables

Tables are laid out in two passes. The first places every cell on a grid, resolving `colspan` and `rowspan` the way the HTML table model does; the second converts cell contents once column widths are known (images need the width to fit). Word needs every row to cover every grid column, so cells hidden by a rowspan are written as `w:vMerge` continuation cells and short rows are padded with empty cells. A colspan that would run into a rowspan from above is cut short. Tables wider than Word's 63 columns are truncated with a warning.

Borders:

- If CSS puts a border on the table or any cell, borders are written exactly as CSS says, per side.
- `border="0"`, or CSS that removes the table's border, gives a borderless table.
- Otherwise the table gets a thin grid. A browser would draw an unstyled table with no borders at all, but an invisible grid is rarely what someone wants in a Word document. This is a deliberate departure.

Width: tables shrink to fit their content, as in a browser, unless a width is given.

### Links and bookmarks

`#fragment` links become internal Word links only when the target id exists; otherwise the text is kept and the link dropped. Bookmarks are written for headings and for ids that something links to, not for every id, since real pages carry hundreds. Ids are mapped to names Word accepts (letters, digits, underscore; 40 characters; unique ignoring case).

### CSS engine

`internal/css` is a small engine written for this job: a tolerant stylesheet parser, selectors with specificity, and a matcher over the parser's nodes. Rules are indexed by the id, class or tag of their last compound selector, so an element only tests rules that could match it. Matching one selector against one element has a fixed work budget, which stops stylesheets built from long chains of descendant combinators from taking exponential time.

CSS variables follow the Custom Properties specification. `--name` declarations take part in the cascade like any property (specificity, `!important`) and inherit. Each element that defines variables gets an immutable scope pointing at its nearest defining ancestor; values are resolved when the scope is built, so a descendant inherits the resolved value rather than re-resolving the reference in its own context. Variables in a cycle are invalid, and an invalid variable still hides the one it would inherit. Other declarations have `var()` substituted just before they are applied.

One deliberate departure: a declaration whose `var()` cannot be resolved should make the property `unset`. The converter does not implement `inherit`, `initial` or `unset` for any property yet, so such a declaration is dropped and the previous winner stays. Variables matter here because of what models write: a print rule such as `th { background:#fff !important; color:var(--primary) !important }`, with variables dropped, left the screen rule's white text on a white cell.

Substitution is bounded for hostile input: one substituted value may not exceed 8 KB (`css.MaxVarLength`), fallbacks nest at most 32 deep, and one conversion produces at most 32 MB of substituted text before further `var()` references are ignored with a warning. Variables are resolved in name order so output stays reproducible.

The document is treated as print media: `@media print` and `@media all` apply; screen-only blocks and anything with a feature query (`min-width` and so on) do not, because those are written for viewport sizes that mean nothing on paper.

### OOXML writing

XML is written directly into a buffer rather than through `encoding/xml`. The schema fixes the order of child elements almost everywhere, and writing by hand makes that order explicit and the writer fast. Characters XML cannot carry are removed. Output is deterministic: the same input and `Options.Created` give identical bytes.

## Checking the output

| Check | What it proves | How |
|---|---|---|
| Golden files | Output did not change by accident | `go test`; `go test -update .` after an intended change, then review the diff |
| Structural rules | Package is complete, XML well formed, relationships declared, cells end with a paragraph | `checkPackage` in the tests, also run by the fuzzer |
| Schema | Parts are valid WordprocessingML | `TestSchema`, needs `xmllint` and the schema (below) |
| Independent reader | Structure survives a round trip through another DOCX reader | Round-trip test, skips when the reader is not installed |
| Fuzzing | No panics, always a valid package | `go test -fuzz FuzzConvert .` |
| Hostile input | No quadratic blow-ups | `TestPathologicalInputs` |

### Fetching the schema

The ECMA-376 schema is published by Ecma International and is not redistributed here.

```
mkdir -p oracle/xsd && cd oracle/xsd
curl -LO https://ecma-international.org/wp-content/uploads/ECMA-376-4_5th_edition_december_2016.zip
unzip ECMA-376-4_5th_edition_december_2016.zip OfficeOpenXML-XMLSchema-Transitional.zip
unzip OfficeOpenXML-XMLSchema-Transitional.zip
```

The schema imports the `xml:` namespace without saying where to find it. Add a small `xml.xsd` declaring the `xml:space` and `xml:lang` attributes and point the two imports (in `wml.xsd` and `shared-math.xsd`) at it with `schemaLocation="xml.xsd"`.

### Not yet checked automatically

Opening the files in Microsoft Word, Google Docs and LibreOffice. Schema validity and a clean round trip are strong evidence, but they are not the same as Word rendering the file as intended. Do this by hand before a release.

## Roadmap

- **v0.2:** footnotes, syntax highlighting, reference `.docx` (styles, numbering, headers and footers from a user's file), cell padding, table of contents field, `@page` size and margins.
- **Later:** text in the SVG fallback rendering, MathML to OMML, tracked changes from `<ins>`/`<del>`, an HTTP resource loader with limits.
