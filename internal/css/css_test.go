package css

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestParseColor(t *testing.T) {
	tests := []struct {
		in   string
		want string // RRGGBB, or "" when the value is not a colour
	}{
		{"#fff", "FFFFFF"},
		{"#1a2B3c", "1A2B3C"},
		{"#1a2b3c80", "1A2B3C"},
		{"RED", "FF0000"},
		{"rebeccapurple", "663399"},
		{"rgb(255, 0, 10)", "FF000A"},
		{"rgb(100% 0% 50%)", "FF0080"},
		{"rgba(0,0,0,0.5)", "000000"},
		{"rgb(300, -5, 0)", "FF0000"},
		{"hsl(120, 100%, 50%)", "00FF00"},
		{"hsl(0deg 100% 50% / 50%)", "FF0000"},
		{"hsl(240, 100%, 25%)", "000080"},
		{"", ""}, {"#12", ""}, {"#ggg", ""}, {"inherit", ""}, {"currentcolor", ""},
		{"rgb(1,2)", ""}, {"rgb(a,b,c)", ""}, {"notacolour", ""}, {"hsl(nan, 1%, 1%)", ""},
	}
	for _, tt := range tests {
		c, ok := ParseColor(tt.in)
		got := ""
		if ok {
			got = c.Hex()
		}
		if got != tt.want {
			t.Errorf("ParseColor(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if c, ok := ParseColor("transparent"); !ok || c.A != 0 {
		t.Errorf("transparent should parse with zero alpha, got %+v %v", c, ok)
	}
	if got := (Color{255, 0, 0, 0.5}).OnWhite().Hex(); got != "FF8080" {
		t.Errorf("OnWhite = %s, want FF8080", got)
	}
}

func TestParseLength(t *testing.T) {
	tests := []struct {
		in  string
		pt  float64
		ok  bool // parses
		abs bool // converts to points
	}{
		{"12pt", 12, true, true},
		{"16px", 12, true, true},
		{"1in", 72, true, true},
		{"2.54cm", 72, true, true},
		{"25.4mm", 72, true, true},
		{"1pc", 12, true, true},
		{"2em", 20, true, true},
		{"2rem", 22, true, true},
		{"0", 0, true, true},
		{"-4px", -3, true, true},
		{".5in", 36, true, true},
		{"50%", 0, true, false},
		{"1.5", 0, true, false},
		{"", 0, false, false}, {"px", 0, false, false}, {"12foo", 0, false, false},
		{"inf", 0, false, false}, {"nan", 0, false, false}, {"0x10px", 0, false, false},
		{"1e3px", 0, false, false}, {"calc(1px + 2px)", 0, false, false},
	}
	for _, tt := range tests {
		l, ok := ParseLength(tt.in)
		if ok != tt.ok {
			t.Errorf("ParseLength(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		pt, abs := l.Points(10, 11)
		if abs != tt.abs || (abs && (pt-tt.pt > 1e-9 || tt.pt-pt > 1e-9)) {
			t.Errorf("ParseLength(%q).Points = %v, %v; want %v, %v", tt.in, pt, abs, tt.pt, tt.abs)
		}
	}
}

func TestParseDeclarations(t *testing.T) {
	got := ParseDeclarations(`COLOR: Red ; font-family: "A;B", serif; background: url("x;y.png") #fff;
		margin:0 !IMPORTANT; --custom: 1; width: var(--w); broken; : nothing; empty: ;`)
	want := []Decl{
		{"color", "Red", false},
		{"font-family", `"A;B", serif`, false},
		{"background", `url("x;y.png") #fff`, false},
		{"margin", "0", true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d declarations %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("declaration %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFields(t *testing.T) {
	got := Fields(`1px  solid rgb(1, 2, 3) "a b" )`)
	want := []string{"1px", "solid", "rgb(1, 2, 3)", `"a b"`, ")"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// find returns the first element with the given id.
func find(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode {
		if v, _ := attrValue(n, "id"); v == id {
			return n
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := find(c, id); r != nil {
			return r
		}
	}
	return nil
}

const selectorDoc = `<html><body class="page">
<div id="d1" class="box big"><p id="p1" class="x">a</p><p id="p2" lang="en-GB" data-k="hello world">b</p><span id="s1">c</span></div>
<ul id="u1"><li id="l1">1</li><li id="l2" class="x">2</li><li id="l3">3</li><li id="l4">4</li></ul>
<a id="a1" href="https://example.com/x.pdf">l</a><a id="a2">n</a><p id="e1"></p>
</body></html>`

func TestSelectors(t *testing.T) {
	root, err := html.Parse(strings.NewReader(selectorDoc))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		sel  string
		id   string
		want bool
	}{
		{"p", "p1", true}, {"P", "p1", true}, {"div", "p1", false}, {"*", "p1", true},
		{".x", "p1", true}, {".x", "p2", false}, {"p.x", "p1", true}, {"li.x", "p1", false},
		{".box.big", "d1", true}, {".box.small", "d1", false}, {".bo", "d1", false},
		{"#p1", "p1", true}, {"p#p1.x", "p1", true}, {"#p1", "p2", false},
		{"div p", "p1", true}, {"body p", "p1", true}, {"ul p", "p1", false},
		{".page div p", "p1", true}, {"html > body > div > p", "p1", true}, {"body > p", "p1", false},
		{"p + p", "p2", true}, {"p + p", "p1", false}, {"p ~ span", "s1", true}, {"p + span", "s1", true},
		{"#p1 ~ span", "s1", true}, {"span ~ p", "p1", false},
		{"[lang]", "p2", true}, {"[lang]", "p1", false}, {`[lang="en-GB"]`, "p2", true}, {"[lang=en-GB]", "p2", true},
		{`[lang|="en"]`, "p2", true}, {`[data-k~="world"]`, "p2", true}, {`[data-k^="hell"]`, "p2", true},
		{`[data-k$="rld"]`, "p2", true}, {`[data-k*="lo wo"]`, "p2", true}, {`[data-k="hello"]`, "p2", false},
		{`a[href$=".pdf"]`, "a1", true}, {`a[href^="http"]`, "a2", false},
		{"li:first-child", "l1", true}, {"li:first-child", "l2", false}, {"li:last-child", "l4", true},
		{"li:nth-child(2)", "l2", true}, {"li:nth-child(odd)", "l3", true}, {"li:nth-child(even)", "l3", false},
		{"li:nth-child(2n+1)", "l1", true}, {"li:nth-child(n+3)", "l2", false}, {"li:nth-child(n+3)", "l4", true},
		{"li:nth-child(-n+2)", "l2", true}, {"li:nth-child(-n+2)", "l3", false}, {"li:nth-child( 2n + 1 )", "l3", true},
		{"li:not(.x)", "l1", true}, {"li:not(.x)", "l2", false}, {"li:not(:first-child)", "l1", false},
		{"p:first-of-type", "p1", true}, {"p:last-of-type", "p2", true}, {"span:only-child", "s1", false},
		{"a:link", "a1", true}, {"a:link", "a2", false}, {"p:empty", "e1", true}, {"p:empty", "p1", false},
		{"html:root", "d1", false}, {":root body div", "d1", true},
		// Things that can never match a static document, or are not valid.
		{"a:hover", "a1", false}, {"p::before", "p1", false}, {"p:before", "p1", false},
		{"p >", "p1", false}, {"> p", "p1", false}, {"p..x", "p1", false}, {"", "p1", false}, {"p[", "p1", false},
	}
	for _, tt := range tests {
		n := find(root, tt.id)
		if n == nil {
			t.Fatalf("no element with id %s", tt.id)
		}
		s := NewStylesheet()
		s.Add(tt.sel + " { color: red }")
		got := len(s.Match(n, nil)) > 0
		if got != tt.want {
			t.Errorf("selector %q on #%s: got %v, want %v", tt.sel, tt.id, got, tt.want)
		}
	}
}

func TestCascadeOrder(t *testing.T) {
	root, _ := html.Parse(strings.NewReader(selectorDoc))
	s := NewStylesheet()
	s.Add(`#p1 { v: id }  p { v: type1 }  .x { v: class }  p { v: type2 }  div p.x { v: compound }  * { v: any }`)
	var got []string
	for _, m := range s.Match(find(root, "p1"), nil) {
		got = append(got, m.Decls[0].Value)
	}
	want := "any type1 type2 class compound id"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

func TestStylesheetParsing(t *testing.T) {
	root, _ := html.Parse(strings.NewReader(selectorDoc))
	s := NewStylesheet()
	s.Add(`@charset "utf-8"; @import url("a.css");
		/* comment { } */ <!-- p { v: plain } -->
		@font-face { font-family: X; src: url(x) }
		@media screen { p { v: screen } }
		@media (min-width: 10px) { p { v: feature } }
		@media print { p { v: print } @media all { p { v: nested } } }
		@media only print, screen { p { v: list } }
		@supports (display: grid) { p { v: supports } }
		@keyframes k { from { v: kf } to { v: kf } }
		@page { margin: 1in }
		p, , h1 { v: group }
		p { content: "}"; v: brace-in-string }
		p:hover, p { v: partly-valid }
		} stray
		p { v: swallowed-by-stray-text }
		p { v: recovered }
		p { v: unterminated`)
	var got []string
	for _, m := range s.Match(find(root, "p1"), nil) {
		for _, d := range m.Decls {
			if d.Prop == "v" {
				got = append(got, d.Value)
			}
		}
	}
	want := "plain print nested list supports group brace-in-string partly-valid recovered unterminated"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %v\nwant %s", got, want)
	}
}

// A selector made of many descendant combinators must not take exponential
// time on a deep tree.
func TestSelectorBudget(t *testing.T) {
	doc := strings.Repeat("<div>", 300) + `<p id="p">x</p>`
	root, _ := html.Parse(strings.NewReader(doc))
	s := NewStylesheet()
	s.Add(strings.Repeat("div ", 15) + "b p { color: red }")
	if len(s.Match(find(root, "p"), nil)) != 0 {
		t.Error("selector should not match")
	}
}

func TestMediaMatches(t *testing.T) {
	for q, want := range map[string]bool{
		"": true, "all": true, "print": true, "only print": true, "screen, print": true,
		"screen": false, "print and (min-width: 1px)": false, "not print": false, "(max-width: 600px)": false,
	} {
		if got := MediaMatches(q); got != want {
			t.Errorf("MediaMatches(%q) = %v, want %v", q, got, want)
		}
	}
}
