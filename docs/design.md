# Design notes

This file records how htmltodocx is built and why. It doubles as the record that the work is independent: every decision here was made from the public specifications and from looking at how documents behave, not from another converter's source.

## Clean-room rules

1. **Sources are the specifications:** ECMA-376 (Office Open XML), the WHATWG HTML Standard, the CSS specifications.
2. **Nothing other tools write is committed.** Files produced by other converters or word processors live only in the git-ignored `oracle/` folder.
3. **Test inputs are our own.** Everything in `testdata/` was written for this project. Golden files are produced by this library.
4. **Dependencies must be permissively licensed.** Today that is only `golang.org/x/net` and `golang.org/x/text` (BSD-3-Clause).
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

### White space and blocks

The reader follows the CSS model: runs of collapsible white space become one space, including across inline element boundaries; space at the start and end of a block disappears; a `<br>` at the end of a block adds no line unless it is the block's only content. Inline content that is a direct child of a block container forms an implicit paragraph. A block element inside an inline one splits the paragraph, and the inline formatting continues on both sides.

Paragraphs are collected as a flat list of items and trimmed when the block ends. Text is appended to a byte slice, not concatenated, so thousands of small text nodes stay linear.

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
- **Later:** SVG with a raster fallback, MathML to OMML, tracked changes from `<ins>`/`<del>`, an HTTP resource loader with limits.
