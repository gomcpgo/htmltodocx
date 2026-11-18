package htmltodocx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// convert runs a conversion with reproducible settings and returns the parts
// of the resulting package by name.
func convert(t testing.TB, html string, opts *Options) map[string][]byte {
	t.Helper()
	var o Options
	if opts != nil {
		o = *opts
	}
	o.Created = fixedTime
	data, err := ConvertString(context.Background(), html, &o)
	if err != nil {
		t.Fatalf("ConvertString: %v", err)
	}
	return unzip(t, data)
}

func unzip(t testing.TB, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("output is not a zip file: %v", err)
	}
	parts := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = b
	}
	return parts
}

// body returns the contents of w:body, without the trailing section
// properties, which is the part most tests care about.
func body(t testing.TB, html string, opts *Options) string {
	t.Helper()
	doc := string(convert(t, html, opts)["word/document.xml"])
	start := strings.Index(doc, "<w:body>")
	end := strings.Index(doc, "<w:sectPr>")
	if start < 0 || end < 0 {
		t.Fatalf("unexpected document.xml: %s", doc)
	}
	return doc[start+len("<w:body>") : end]
}

// paragraphs returns the text of every paragraph in document order. Line
// breaks read as "\n" and tabs as "\t".
func paragraphs(t testing.TB, html string) []string {
	t.Helper()
	doc := convert(t, html, nil)["word/document.xml"]
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var out []string
	var cur strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("document.xml is not well formed: %v", err)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "p":
				cur.Reset()
			case "t":
				inText = true
			case "br":
				cur.WriteByte('\n')
			case "tab":
				cur.WriteByte('\t')
			}
		case xml.EndElement:
			switch v.Name.Local {
			case "p":
				out = append(out, cur.String())
			case "t":
				inText = false
			}
		case xml.CharData:
			if inText {
				cur.Write(v)
			}
		}
	}
	return out
}

func wellFormed(t testing.TB, name string, data []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("%s is not well-formed XML: %v", name, err)
		}
	}
}

// checkPackage verifies the structural rules every output must satisfy.
func checkPackage(t testing.TB, parts map[string][]byte) {
	t.Helper()
	for _, need := range []string{
		"[Content_Types].xml", "_rels/.rels", "word/document.xml",
		"word/_rels/document.xml.rels", "word/styles.xml", "word/settings.xml",
		"docProps/core.xml", "docProps/app.xml",
	} {
		if _, ok := parts[need]; !ok {
			t.Fatalf("package is missing %s", need)
		}
	}
	for name, data := range parts {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			wellFormed(t, name, data)
		}
	}
	doc := string(parts["word/document.xml"])
	rels := string(parts["word/_rels/document.xml.rels"])
	// Every relationship the document uses must be declared.
	for _, attr := range []string{`r:id="`, `r:embed="`} {
		rest := doc
		for {
			i := strings.Index(rest, attr)
			if i < 0 {
				break
			}
			rest = rest[i+len(attr):]
			id := rest[:strings.IndexByte(rest, '"')]
			if !strings.Contains(rels, `Id="`+id+`"`) {
				t.Fatalf("document uses undeclared relationship %s", id)
			}
		}
	}
	if strings.Contains(doc, "<w:numId") {
		if _, ok := parts["word/numbering.xml"]; !ok {
			t.Fatal("document has lists but no numbering.xml")
		}
	}
	// A table cell must end with a paragraph.
	if strings.Contains(doc, "</w:tbl></w:tc>") || strings.Contains(doc, "</w:tcPr></w:tc>") {
		t.Fatal("a table cell does not end with a paragraph")
	}
	if strings.Contains(doc, "</w:tbl><w:tbl>") {
		t.Fatal("two tables are adjacent with no paragraph between them")
	}
}

func testdataFiles(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test inputs found: %v", err)
	}
	return files
}

// TestGolden converts every file in testdata and compares document.xml with
// the stored result. Run "go test -update" after an intended change and
// review the diff.
func TestGolden(t *testing.T) {
	for _, file := range testdataFiles(t) {
		name := strings.TrimSuffix(filepath.Base(file), ".html")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			parts := convert(t, string(src), &Options{Resources: DirLoader("testdata")})
			checkPackage(t, parts)
			// One element per line keeps diffs of the golden files readable.
			got := strings.ReplaceAll(string(parts["word/document.xml"]), "><", ">\n<") + "\n"
			golden := filepath.Join("testdata", "golden", name+".xml")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("document.xml differs from %s; run go test -update and review the diff\n%s",
					golden, firstDiff(got, string(want)))
			}
		})
	}
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) && i < len(w); i++ {
		if g[i] != w[i] {
			return "line " + strconv.Itoa(i+1) + ":\n  got:  " + g[i] + "\n  want: " + w[i]
		}
	}
	return "lengths differ: got " + strconv.Itoa(len(g)) + " lines, want " + strconv.Itoa(len(w))
}

func TestReproducible(t *testing.T) {
	src, err := os.ReadFile("testdata/kitchen-sink.html")
	if err != nil {
		t.Fatal(err)
	}
	opts := &Options{Created: fixedTime}
	a, err := ConvertString(context.Background(), string(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ConvertString(context.Background(), string(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two conversions of the same input differ")
	}
}

func TestWhitespace(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{"collapse", "<p>  a   b \n\t c  </p>", []string{"a b c"}},
		{"across inline", "<p>a <b> b </b> c</p>", []string{"a b c"}},
		{"space only between inlines", "<p><b>a</b> <i>b</i></p>", []string{"a b"}},
		{"no space invented", "<p><b>a</b><i>b</i></p>", []string{"ab"}},
		{"between blocks", "<div>a</div>   <div>b</div>", []string{"a", "b"}},
		{"implicit paragraphs", "text <p>para</p> more", []string{"text", "para", "more"}},
		{"nbsp kept", "<p>a&nbsp;&nbsp;b</p>", []string{"a\u00a0\u00a0b"}},
		{"nbsp only", "<p>&nbsp;</p>", []string{"\u00a0"}},
		{"empty paragraph dropped", "<p></p><p>   </p><p>x</p>", []string{"x"}},
		{"br", "<p>a <br> b</p>", []string{"a\nb"}},
		{"trailing br", "<p>a<br></p><p>b</p>", []string{"a", "b"}},
		{"double br", "<p>a<br><br>b</p>", []string{"a\n\nb"}},
		{"lone br", "<p>a</p><br><p>b</p>", []string{"a", "", "b"}},
		{"pre", "<pre>\n  a\n\tb\n</pre>", []string{"  a\n\tb"}},
		{"pre keeps blank line", "<pre>a\n\nb</pre>", []string{"a\n\nb"}},
		{"block in inline", "<span>a<div>b</div>c</span>", []string{"a", "b", "c"}},
		{"q", "<p>say <q>hi</q></p>", []string{"say \u201Chi\u201D"}},
		{"entities", "<p>&lt;a&gt; &amp; &quot;b&quot;</p>", []string{`<a> & "b"`}},
		{"unknown tag", "<p>a <frob>b</frob> c</p>", []string{"a b c"}},
		{"script and style skipped", "<p>a</p><script>x</script><style>p{}</style><p>b</p>", []string{"a", "b"}},
		{"hidden", `<p>a</p><p hidden>b</p><p style="display:none">c</p>`, []string{"a"}},
		{"noscript shown", "<noscript><p>fallback</p></noscript>", []string{"fallback"}},
		{"fragment", "just text", []string{"just text"}},
		{"empty input", "", nil},
		{"alt for missing image", `<p>x <img src="a.png" alt="ALT"> y</p>`, []string{"x ALT y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paragraphs(t, tt.html)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") || len(got) != len(tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStructure(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		want    []string // substrings that must appear, in order
		wantNot []string
	}{
		{
			name: "heading style and bookmark",
			html: `<h2 id="intro">Intro</h2>`,
			want: []string{`<w:pStyle w:val="Heading2"/>`, `<w:bookmarkStart w:id="1" w:name="intro"/>`, `<w:t>Intro</w:t>`},
		},
		{
			name: "bold is not repeated on headings",
			html: `<h1>Title</h1>`,
			want: []string{`<w:r><w:t>Title</w:t></w:r>`},
		},
		{
			name: "external link",
			html: `<a href="https://example.com/">x</a>`,
			want: []string{`<w:hyperlink r:id="rId3" w:history="1">`, `<w:rStyle w:val="Hyperlink"/>`},
		},
		{
			name: "internal link",
			html: `<a href="#t">go</a><p id="t">target</p>`,
			want: []string{`<w:hyperlink w:anchor="t" w:history="1">`, `<w:bookmarkStart w:id="1" w:name="t"/>`},
		},
		{
			name:    "dangling internal link is plain text",
			html:    `<a href="#missing">go</a>`,
			want:    []string{`<w:t>go</w:t>`},
			wantNot: []string{`<w:hyperlink`},
		},
		{
			name:    "javascript link is plain text",
			html:    `<a href="javascript:alert(1)">go</a>`,
			wantNot: []string{`<w:hyperlink`},
		},
		{
			name:    "ids nobody links to get no bookmark",
			html:    `<p id="x">a</p>`,
			wantNot: []string{`bookmarkStart`},
		},
		{
			name: "bullet list",
			html: `<ul><li>a</li><li>b</li></ul>`,
			want: []string{`<w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`, `<w:t>a</w:t>`, `<w:numId w:val="1"/>`, `<w:t>b</w:t>`},
		},
		{
			name: "nested list level",
			html: `<ul><li>a<ol><li>b</li></ol></li></ul>`,
			want: []string{`<w:ilvl w:val="0"/><w:numId w:val="1"/>`, `<w:ilvl w:val="1"/><w:numId w:val="2"/>`},
		},
		{
			name: "separate ordered lists restart",
			html: `<ol><li>a</li></ol><ol><li>b</li></ol>`,
			want: []string{`<w:numId w:val="1"/>`, `<w:numId w:val="2"/>`},
		},
		{
			name: "second paragraph of an item is indented, not numbered",
			html: `<ul><li><p>a</p><p>b</p></li></ul>`,
			want: []string{`<w:numPr>`, `<w:t>a</w:t>`, `<w:pStyle w:val="ListParagraph"/><w:ind w:left="720"/></w:pPr><w:r><w:t>b</w:t>`},
		},
		{
			name: "colspan and rowspan",
			html: `<table><tr><td colspan="2">a</td><td rowspan="2">b</td></tr><tr><td>c</td><td>d</td></tr></table>`,
			want: []string{`<w:gridSpan w:val="2"/>`, `<w:vMerge w:val="restart"/>`, `<w:t>c</w:t>`, `<w:t>d</w:t>`, `<w:vMerge/>`},
		},
		{
			name: "short rows are padded",
			html: `<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>`,
			want: []string{`<w:t>c</w:t></w:r></w:p></w:tc><w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr><w:p/></w:tc></w:tr>`},
		},
		{
			name: "header row repeats",
			html: `<table><thead><tr><th>h</th></tr></thead><tr><td>d</td></tr></table>`,
			want: []string{`<w:trPr><w:tblHeader/></w:trPr>`, `<w:b/>`},
		},
		{
			name: "default table has the grid style",
			html: `<table><tr><td>a</td></tr></table>`,
			want: []string{`<w:tblStyle w:val="TableGrid"/>`},
		},
		{
			name: "border=0 table is plain",
			html: `<table border="0"><tr><td>a</td></tr></table>`,
			want: []string{`<w:tblStyle w:val="TablePlain"/>`},
		},
		{
			name: "css cell borders",
			html: `<table><tr><td style="border-bottom: 1px solid #ff0000">a</td></tr></table>`,
			want: []string{`<w:tblStyle w:val="TablePlain"/>`, `<w:tcBorders><w:bottom w:val="single" w:sz="6" w:space="0" w:color="FF0000"/></w:tcBorders>`},
		},
		{
			name: "inline css",
			html: `<p style="text-align:center"><span style="color:#f00;font-weight:bold;font-size:18pt;background-color:yellow">x</span></p>`,
			want: []string{`<w:jc w:val="center"/>`, `<w:b/><w:bCs/><w:color w:val="FF0000"/><w:sz w:val="36"/><w:szCs w:val="36"/><w:shd w:val="clear" w:color="auto" w:fill="FFFF00"/>`},
		},
		{
			name: "explicit normal weight inside bold",
			html: `<b>a<span style="font-weight:normal">b</span></b>`,
			want: []string{`<w:b/><w:bCs/></w:rPr><w:t>a</w:t>`, `<w:b w:val="0"/><w:bCs w:val="0"/></w:rPr><w:t>b</w:t>`},
		},
		{
			name: "inline code and code block",
			html: `<p><code>x</code></p><pre>y</pre>`,
			want: []string{`<w:rStyle w:val="VerbatimChar"/>`, `<w:pStyle w:val="SourceCode"/></w:pPr><w:r><w:t>y</w:t>`},
		},
		{
			name: "page break",
			html: `<p>a</p><p style="page-break-before:always">b</p><div style="break-after:page">c</div><p>d</p>`,
			want: []string{`<w:pageBreakBefore/></w:pPr><w:r><w:t>b</w:t>`, `<w:pageBreakBefore/></w:pPr><w:r><w:t>d</w:t>`},
		},
		{
			name: "rtl",
			html: `<p dir="rtl">x</p>`,
			want: []string{`<w:bidi/>`, `<w:rtl/>`},
		},
		{
			name: "text needing preserved space",
			html: `<p>a <b>b</b></p>`,
			want: []string{`<w:t xml:space="preserve">a </w:t>`},
		},
		{
			name: "data uri image",
			html: `<img width="10" height="20" alt="dot &amp; &quot;co&quot;" src="` + pngDataURI + `">`,
			want: []string{`<wp:extent cx="95250" cy="190500"/>`, `descr="dot &amp; &quot;co&quot;"`, `r:embed="rId3"`},
		},
		{
			name: "image keeps aspect ratio and fits the page",
			html: `<img width="5000" src="` + pngDataURI + `">`,
			want: []string{`<wp:extent cx="5731510" cy="5731510"/>`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := body(t, tt.html, nil)
			rest := got
			for _, w := range tt.want {
				i := strings.Index(rest, w)
				if i < 0 {
					t.Fatalf("missing (or out of order) %q in:\n%s", w, got)
				}
				rest = rest[i+len(w):]
			}
			for _, w := range tt.wantNot {
				if strings.Contains(got, w) {
					t.Fatalf("unexpected %q in:\n%s", w, got)
				}
			}
		})
	}
}

const pngDataURI = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAUAAAAFCAYAAACNbyblAAAAHElEQVQI12P4//8/w38GIAXDIBKE0DHxgljNBAAO9TXL0Y4OHwAAAABJRU5ErkJggg=="

func TestCSSModes(t *testing.T) {
	const html = `<style>p { color: #00ff00 }</style><p style="font-style: italic">x</p>`
	all := body(t, html, &Options{CSS: CSSAll})
	if !strings.Contains(all, `w:val="00FF00"`) || !strings.Contains(all, `<w:i/>`) {
		t.Errorf("CSSAll should apply both sources: %s", all)
	}
	inline := body(t, html, &Options{CSS: CSSInlineOnly})
	if strings.Contains(inline, `w:val="00FF00"`) || !strings.Contains(inline, `<w:i/>`) {
		t.Errorf("CSSInlineOnly should apply only the style attribute: %s", inline)
	}
	none := body(t, html, &Options{CSS: CSSNone})
	if strings.Contains(none, `w:val="00FF00"`) || strings.Contains(none, `<w:i/>`) {
		t.Errorf("CSSNone should apply no CSS: %s", none)
	}
}

func TestBodyStylesBecomeDefaults(t *testing.T) {
	parts := convert(t, `<body style="font-family: Georgia; color: #123456; font-size: 22pt"><p>x</p><h1>y</h1></body>`, nil)
	doc, styles := string(parts["word/document.xml"]), string(parts["word/styles.xml"])
	if strings.Contains(doc, "Georgia") || strings.Contains(doc, "123456") || strings.Contains(doc, "<w:sz") {
		t.Errorf("body formatting should not be repeated on runs: %s", doc)
	}
	for _, want := range []string{`w:ascii="Georgia"`, `<w:color w:val="123456"/>`, `<w:sz w:val="44"/>`, `<w:sz w:val="80"/>`} {
		if !strings.Contains(styles, want) {
			t.Errorf("styles.xml is missing %s", want)
		}
	}
}

func TestMetadata(t *testing.T) {
	parts := convert(t, `<html lang="fr"><head><title> A  &amp; B </title><meta name="author" content="Zoë">
		<meta name="description" content="d"><meta name="keywords" content="k1, k2"></head><body>x</body></html>`, nil)
	core := string(parts["docProps/core.xml"])
	for _, want := range []string{
		"<dc:title>A &amp; B</dc:title>", "<dc:creator>Zoë</dc:creator>", "<dc:description>d</dc:description>",
		"<cp:keywords>k1, k2</cp:keywords>", "<dc:language>fr</dc:language>", "2026-01-02T03:04:05Z",
	} {
		if !strings.Contains(core, want) {
			t.Errorf("core.xml is missing %q:\n%s", want, core)
		}
	}
	if !strings.Contains(string(parts["word/styles.xml"]), `<w:lang w:val="fr"/>`) {
		t.Error("document language was not applied to the styles")
	}
}

func TestPageSetup(t *testing.T) {
	doc := string(convert(t, "x", &Options{Page: Page{Size: Letter, Landscape: true, Margins: Margins{720, 720, 720, 720}}})["word/document.xml"])
	want := `<w:pgSz w:w="15840" w:h="12240" w:orient="landscape"/><w:pgMar w:top="720" w:right="720" w:bottom="720" w:left="720"`
	if !strings.Contains(doc, want) {
		t.Errorf("got %s", doc[strings.Index(doc, "<w:sectPr>"):])
	}
	doc = string(convert(t, "x", nil)["word/document.xml"])
	if !strings.Contains(doc, `<w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440"`) {
		t.Errorf("default page should be A4 with one inch margins: %s", doc[strings.Index(doc, "<w:sectPr>"):])
	}
}

func TestInvalidCharactersAreDropped(t *testing.T) {
	parts := convert(t, "<p title='\x01'>a\x01b\x0bc\uFFFEd \xff e</p>", nil)
	checkPackage(t, parts)
	got := paragraphs(t, "<p>a\x01b\x0bc\uFFFEd</p>")
	if len(got) != 1 || got[0] != "abcd" {
		t.Errorf("got %q", got)
	}
}

func TestEncodingDetection(t *testing.T) {
	// "café" in Latin-1, declared by a meta element.
	src := "<meta charset=\"iso-8859-1\"><p>caf\xe9</p>"
	got := paragraphs(t, src)
	if len(got) != 1 || got[0] != "café" {
		t.Errorf("got %q", got)
	}
}

func TestWarnings(t *testing.T) {
	var got []string
	opts := &Options{Warn: func(w Warning) { got = append(got, w.Code) }}
	convert(t, `<img src="a.png"><img src="a.png"><svg></svg><math><mi>x</mi></math><ol reversed><li>a</li></ol>`, opts)
	want := "image,svg,math,list"
	if strings.Join(got, ",") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

func TestResourceLoader(t *testing.T) {
	var asked []string
	opts := &Options{
		BaseURL: "https://example.com/docs/page.html",
		Resources: func(ctx context.Context, url string) ([]byte, error) {
			asked = append(asked, url)
			if strings.HasSuffix(url, ".css") {
				return []byte("p { color: #abcdef }"), nil
			}
			return os.ReadFile("testdata/dot.png")
		},
	}
	got := body(t, `<link rel="stylesheet" href="../s.css"><p><img src="img/dot.png"> <a href="next.html">n</a></p>`, opts)
	if strings.Join(asked, " ") != "https://example.com/s.css https://example.com/docs/img/dot.png" {
		t.Errorf("loader was asked for %v", asked)
	}
	if !strings.Contains(got, "ABCDEF") || !strings.Contains(got, "<w:drawing>") {
		t.Errorf("stylesheet or image was not used: %s", got)
	}
	rels := string(convert(t, `<a href="next.html">n</a>`, opts)["word/_rels/document.xml.rels"])
	if !strings.Contains(rels, `Target="https://example.com/docs/next.html" TargetMode="External"`) {
		t.Errorf("relative link was not resolved: %s", rels)
	}
}

func TestDirLoader(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "site")
	if err := os.MkdirAll(filepath.Join(inner, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "img", "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "secret.txt"), filepath.Join(inner, "link.txt")); err != nil {
		t.Skip("symlinks are not available")
	}
	load := DirLoader(inner)
	ctx := context.Background()
	if b, err := load(ctx, "img/a.txt?v=1#frag"); err != nil || string(b) != "ok" {
		t.Errorf("file inside the directory: %q, %v", b, err)
	}
	for _, ref := range []string{
		"../secret.txt", "img/../../secret.txt", filepath.Join(dir, "secret.txt"), "link.txt",
		"file:///etc/passwd", "https://example.com/x.png", "//example.com/x.png", "", "missing.png",
	} {
		if b, err := load(ctx, ref); err == nil {
			t.Errorf("loading %q should fail, got %q", ref, b)
		}
	}
}

func TestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ConvertString(ctx, "<p>x</p>", nil); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

func TestDeepNesting(t *testing.T) {
	html := strings.Repeat("<div>", 150) + "deep" + strings.Repeat("</div>", 150) +
		strings.Repeat("<ul><li>", 100) + "list" +
		strings.Repeat("<table><tr><td>", 50) + "cell"
	checkPackage(t, convert(t, html, nil))

	// The HTML parser limits nesting to 512 open elements. Beyond that the
	// conversion fails cleanly instead of exhausting the stack.
	tooDeep := strings.Repeat("<div>", 5000) + "x"
	if _, err := ConvertString(context.Background(), tooDeep, nil); err == nil {
		t.Error("expected an error for input nested thousands of levels deep")
	}
}

func TestLargeTable(t *testing.T) {
	var b strings.Builder
	b.WriteString("<style>tr:nth-child(even) td { background: #eee }</style><table>")
	for i := 0; i < 2000; i++ {
		b.WriteString("<tr><td>a</td><td>b</td><td>c</td></tr>")
	}
	b.WriteString("</table><table><tr>")
	for i := 0; i < 200; i++ {
		b.WriteString(`<td colspan="3" rowspan="2">wide</td>`)
	}
	b.WriteString("</tr></table>")
	parts := convert(t, b.String(), nil)
	checkPackage(t, parts)
	if n := strings.Count(string(parts["word/document.xml"]), `w:fill="EEEEEE"`); n != 3000 {
		t.Errorf("striped %d cells, want 3000", n)
	}
}

// TestPandocReadsOutput uses the pandoc program, if installed, as an
// independent reader: it must open our files and find the same structure.
// pandoc is only ever run as a separate program on our own output.
func TestPandocReadsOutput(t *testing.T) {
	pandoc, err := exec.LookPath("pandoc")
	if err != nil {
		t.Skip("pandoc is not installed")
	}
	src, err := os.ReadFile("testdata/kitchen-sink.html")
	if err != nil {
		t.Fatal(err)
	}
	data, err := ConvertString(context.Background(), string(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "out.docx")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(pandoc, file, "-t", "markdown", "--wrap=none").CombinedOutput()
	if err != nil {
		t.Fatalf("pandoc could not read the output: %v\n%s", err, out)
	}
	md := string(out)
	for _, want := range []string{
		"# Quarterly Report",
		"## Lists & things",
		"**bold**", "*emphatic*", "H~2~O", "E=mc^2^", "`inline code`",
		`[link](https://example.com/a%20b?q=1&r=2 "Example")`,
		"[internal link](#lists-things)",
		"- One",
		"c.  Nested c",
		"1.  First",
		"1.  Restarted",
		"- [x] Done",
		`fmt.Println("hi & bye")`,
		": Table 1. Sales",
		"| North",
		"![Figure 1. A dot](media/image1.png)",
		"New page.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("pandoc output is missing %q", want)
		}
	}
	for _, bad := range []string{"You should not see this", "Nor this"} {
		if strings.Contains(md, bad) {
			t.Errorf("pandoc output contains hidden text %q", bad)
		}
	}
}

// TestSchema validates the generated WordprocessingML against the ECMA-376
// transitional schema with xmllint. The schema files are not part of the
// repository; see docs/design.md for how to fetch them into oracle/xsd. The
// test is skipped when they or xmllint are missing.
func TestSchema(t *testing.T) {
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		t.Skip("xmllint is not installed")
	}
	schema := filepath.Join("oracle", "xsd", "wml.xsd")
	if _, err := os.Stat(schema); err != nil {
		t.Skip("ECMA-376 schema not found in oracle/xsd")
	}
	dir := t.TempDir()
	var files []string
	for _, file := range testdataFiles(t) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parts := convert(t, string(src), &Options{Resources: DirLoader("testdata")})
		base := strings.TrimSuffix(filepath.Base(file), ".html")
		for _, part := range []string{"document", "styles", "numbering", "settings"} {
			data, ok := parts["word/"+part+".xml"]
			if !ok {
				continue
			}
			name := filepath.Join(dir, base+"-"+part+".xml")
			if err := os.WriteFile(name, data, 0o644); err != nil {
				t.Fatal(err)
			}
			files = append(files, name)
		}
	}
	args := append([]string{"--noout", "--schema", schema}, files...)
	if out, err := exec.Command(xmllint, args...).CombinedOutput(); err != nil {
		var bad []string
		for _, line := range strings.Split(string(out), "\n") {
			if line != "" && !strings.HasSuffix(line, " validates") {
				bad = append(bad, line)
			}
		}
		t.Fatalf("schema validation failed: %v\n%s", err, strings.Join(bad, "\n"))
	}
}

func FuzzConvert(f *testing.F) {
	for _, file := range testdataFiles(f) {
		src, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(src))
	}
	f.Add(`<table><tr><td rowspan=0 colspan=999>x<td rowspan=3><tr><td colspan=-1>`)
	f.Add(`<style>a b c d e f g h{color:red} @media print{p{}} x:nth-child(-n+3){}</style><p style="font:bold 1e9px/0 x;margin:-1px">`)
	f.Add(`<ol start=99999999999><li value=x><ul><li><ol><li></ol></ul></ol><a href="#%zz">`)
	f.Fuzz(func(t *testing.T, html string) {
		data, err := ConvertString(context.Background(), html, &Options{Created: fixedTime})
		if err != nil {
			t.Fatalf("ConvertString: %v", err)
		}
		checkPackage(t, unzip(t, data))
	})
}

func BenchmarkConvert(b *testing.B) {
	src, err := os.ReadFile("testdata/kitchen-sink.html")
	if err != nil {
		b.Fatal(err)
	}
	doc := strings.Repeat(string(src), 20)
	b.SetBytes(int64(len(doc)))
	b.ReportAllocs()
	for b.Loop() {
		if err := Convert(context.Background(), strings.NewReader(doc), io.Discard, nil); err != nil {
			b.Fatal(err)
		}
	}
}
