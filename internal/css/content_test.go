package css

import (
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestPseudoElementSelectors(t *testing.T) {
	root, err := html.Parse(strings.NewReader(selectorDoc))
	if err != nil {
		t.Fatal(err)
	}
	p1 := find(root, "p1")
	tests := []struct {
		sel          string
		before, elem bool // matches p1::before, matches p1 itself
		after        bool
	}{
		{"p::before", true, false, false},
		{"p:before", true, false, false},
		{"P::BEFORE", true, false, false},
		{"p::after", false, false, true},
		{"p:after", false, false, true},
		{".x::before", true, false, false},
		{"div > p.x::after", false, false, true},
		{"li::before", false, false, false},
		{"::before", true, false, false},
		// A pseudo-element must come last, and only these two are supported.
		{"p::before span", false, false, false},
		{"p::before.x", false, false, false},
		{"p::before::after", false, false, false},
		{"p::marker", false, false, false},
		{"p::first-line", false, false, false},
		{"p", false, true, false},
	}
	for _, tt := range tests {
		s := NewStylesheet()
		s.Add(tt.sel + " { color: red }")
		if got := len(s.MatchPseudo(p1, Before, nil)) > 0; got != tt.before {
			t.Errorf("%q matches p1::before = %v, want %v", tt.sel, got, tt.before)
		}
		if got := len(s.MatchPseudo(p1, After, nil)) > 0; got != tt.after {
			t.Errorf("%q matches p1::after = %v, want %v", tt.sel, got, tt.after)
		}
		if got := len(s.Match(p1, nil)) > 0; got != tt.elem {
			t.Errorf("%q matches p1 = %v, want %v", tt.sel, got, tt.elem)
		}
		if tt.before && !s.HasPseudo(Before) {
			t.Errorf("%q: HasPseudo(Before) is false", tt.sel)
		}
	}
	s := NewStylesheet()
	s.Add(`.x::before { v: class } p::before { v: type } p::before { v: type2 }`)
	var got []string
	for _, m := range s.MatchPseudo(p1, Before, nil) {
		got = append(got, m.Decls[0].Value)
	}
	if strings.Join(got, " ") != "type type2 class" {
		t.Errorf("cascade order = %v", got)
	}
	if s.HasPseudo(After) {
		t.Error("HasPseudo(After) with no ::after rules")
	}
}

func TestParseContent(t *testing.T) {
	tests := []struct {
		in   string
		want []ContentItem
		ok   bool
	}{
		{`"Note: "`, []ContentItem{{Kind: ContentText, Text: "Note: "}}, true},
		{`'it\'s'`, []ContentItem{{Kind: ContentText, Text: "it's"}}, true},
		{`"\2610 "`, []ContentItem{{Kind: ContentText, Text: "☐"}}, true},
		{`"\2610  x"`, []ContentItem{{Kind: ContentText, Text: "☐ x"}}, true},
		{`"\00a0\00a0"`, []ContentItem{{Kind: ContentText, Text: "  "}}, true},
		{`"\"q\"" "\\"`, []ContentItem{{Kind: ContentText, Text: `"q"`}, {Kind: ContentText, Text: `\`}}, true},
		{`"\0"`, []ContentItem{{Kind: ContentText, Text: "�"}}, true},
		{`"\110000"`, []ContentItem{{Kind: ContentText, Text: "�"}}, true},
		{`"▸"`, []ContentItem{{Kind: ContentText, Text: "▸"}}, true},
		{`""`, []ContentItem{{Kind: ContentText}}, true},
		{`counter(item)`, []ContentItem{{Kind: ContentCounter, Name: "item"}}, true},
		{`counter( item , upper-roman )`, []ContentItem{{Kind: ContentCounter, Name: "item", Style: "upper-roman"}}, true},
		{`"5." counters(item, ".") "\00a0"`, []ContentItem{
			{Kind: ContentText, Text: "5."},
			{Kind: ContentCounters, Name: "item", Text: "."},
			{Kind: ContentText, Text: " "},
		}, true},
		{`counters(a, "-", lower-alpha)`, []ContentItem{{Kind: ContentCounters, Name: "a", Text: "-", Style: "lower-alpha"}}, true},
		{`attr(data-label) ": "`, []ContentItem{{Kind: ContentAttr, Name: "data-label"}, {Kind: ContentText, Text: ": "}}, true},
		{`open-quote "x" close-quote no-open-quote`, []ContentItem{{Kind: ContentOpenQuote}, {Kind: ContentText, Text: "x"}, {Kind: ContentCloseQuote}}, true},
		{`url(icon.png)`, []ContentItem{{Kind: ContentURL}}, true},
		{`url("a.png") "x"`, []ContentItem{{Kind: ContentURL}, {Kind: ContentText, Text: "x"}}, true},
		{`none`, nil, true},
		{`normal`, nil, true},
		// Invalid values are ignored, as in a browser.
		{`counter()`, nil, false},
		{`counters(item)`, nil, false},
		{`"unterminated`, []ContentItem{{Kind: ContentText, Text: "unterminated"}}, true},
		{`bogus`, nil, false},
		{`"a" / "alt"`, []ContentItem{{Kind: ContentText, Text: "a"}}, true},
	}
	for _, tt := range tests {
		got, ok := ParseContent(tt.in)
		if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseContent(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseCounterOps(t *testing.T) {
	tests := []struct {
		in   string
		def  int
		want []CounterOp
	}{
		{"item", 1, []CounterOp{{"item", 1}}},
		{"item 2 other", 1, []CounterOp{{"item", 2}, {"other", 1}}},
		{"a -1 b", 0, []CounterOp{{"a", -1}, {"b", 0}}},
		{"none", 0, nil},
		{"", 0, nil},
		{"3", 0, nil},
		{"a 99999999999", 0, []CounterOp{{"a", 0}}},
	}
	for _, tt := range tests {
		if got := ParseCounterOps(tt.in, tt.def); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseCounterOps(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestFormatCounter(t *testing.T) {
	tests := []struct {
		v     int
		style string
		want  string
	}{
		{3, "", "3"}, {3, "decimal", "3"}, {-2, "decimal", "-2"},
		{7, "decimal-leading-zero", "07"}, {12, "decimal-leading-zero", "12"},
		{1, "lower-alpha", "a"}, {27, "lower-latin", "aa"}, {28, "upper-alpha", "AB"}, {0, "lower-alpha", "0"},
		{4, "lower-roman", "iv"}, {1994, "upper-roman", "MCMXCIV"}, {5000, "upper-roman", "5000"},
		{1, "disc", "•"}, {1, "circle", "◦"}, {1, "square", "▪"}, {1, "none", ""},
		{9, "unknown-style", "9"},
	}
	for _, tt := range tests {
		if got := FormatCounter(tt.v, tt.style); got != tt.want {
			t.Errorf("FormatCounter(%d, %q) = %q, want %q", tt.v, tt.style, got, tt.want)
		}
	}
}
