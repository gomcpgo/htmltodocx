package htmltodocx

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPathologicalInputs feeds the converter inputs built to provoke
// quadratic behaviour. Each must finish quickly; run with -v to see timings.
func TestPathologicalInputs(t *testing.T) {
	cases := map[string]string{
		"fragments":  "<p>" + strings.Repeat("a<x></x>", 30000),
		"spans":      "<p>" + strings.Repeat("<b>a</b><i>b</i>", 11000),
		"brs":        "<p>" + strings.Repeat("a <br> ", 20000),
		"paragraphs": strings.Repeat("<p>hello world</p>", 20000),
		"badids": func() string {
			var b strings.Builder
			for i := 0; i < 6000; i++ {
				fmt.Fprintf(&b, "<h2 id='%s'>x</h2>", strings.Repeat("é", i%500+1)+fmt.Sprint(i%7))
			}
			return b.String()
		}(),
		"links": func() string {
			var b strings.Builder
			for i := 0; i < 6000; i++ {
				fmt.Fprintf(&b, "<a href='#i%d'>x</a><i id='i%d'>y</i> ", i, i)
			}
			return b.String()
		}(),
		"rules":      "<style>" + strings.Repeat("div div div div p span, .a .b .c * {color:red}\n", 300) + "</style>" + strings.Repeat("<div class='a'><div class='b'><div class='c'><p><span>x</span></p></div></div></div>", 1000),
		"lists":      strings.Repeat("<ol><li>a<ul><li>b</li></ul></li></ol>", 5000),
		"cells":      "<table>" + strings.Repeat("<tr>"+strings.Repeat("<td rowspan=3 colspan=2>x</td>", 40)+"</tr>", 1000) + "</table>",
		"pre":        "<pre>" + strings.Repeat("line\n<b>x</b>\t", 20000),
		"entities":   strings.Repeat("&amp;&lt;\x01\xff", 50000),
		"styleattrs": strings.Repeat(`<p style="color:red;border:1px solid #000;font:italic bold 12px/1.5 Arial;margin:1px 2px 3px 4px">x</p>`, 5000),
		"comments":   "<style>" + strings.Repeat("/*", 20000) + strings.Repeat("{", 20000) + "</style><p>x",
		// Each variable doubles the previous one, so naive substitution grows
		// as 2^60.
		"varbomb": func() string {
			var b strings.Builder
			b.WriteString("<style>:root{--v0:xxxxxxxx;")
			for i := 1; i <= 60; i++ {
				fmt.Fprintf(&b, "--v%d:var(--v%d) var(--v%d);", i, i-1, i-1)
			}
			b.WriteString("} p{font-family:var(--v60)}</style>")
			b.WriteString(strings.Repeat("<p>x</p>", 2000))
			return b.String()
		}(),
		// Variables that double on every element: only the conversion-wide
		// substitution budget keeps this from redoing megabytes per element.
		"varstar": func() string {
			var b strings.Builder
			b.WriteString("<style>*{--v0:xxxxxxxxxxxxxxxx;")
			for i := 1; i <= 200; i++ {
				fmt.Fprintf(&b, "--v%d:var(--v%d) var(--v%d);", i, i-1, i-1)
			}
			b.WriteString("color:var(--v200, red)}</style>")
			b.WriteString(strings.Repeat("<p>x</p>", 3000))
			return b.String()
		}(),
		// Many variables redefined at every level of a deep tree, each read.
		"vardeep": "<style>div{--a:var(--b, red);--b:var(--c, blue);--c:#123456;color:var(--a);background:var(--b)}</style>" +
			strings.Repeat("<div>x", 500) + strings.Repeat("<p style='color:var(--a)'>y</p>", 5000),
		// Long rows of flex items, spaced (tabs) and packed (spaces).
		"flexrow": strings.Repeat(`<div style="display:flex;justify-content:space-between">`+strings.Repeat("<span>a</span><span></span>", 2000)+"</div>", 5) +
			strings.Repeat(`<div style="display:flex">`+strings.Repeat("<b>a</b>", 5000)+"</div>", 5),
		// Grids with absurd repeat() counts, and many rows of a real grid.
		"gridrows": `<div style="display:grid;grid-template-columns:repeat(99999999, 1fr) repeat(3, repeat(40, 1px))">x</div>` +
			strings.Repeat(`<div style="display:grid;grid-template-columns:100px 1fr 2fr">`+strings.Repeat("<i>a</i>", 3000)+"</div>", 5),
		// Nested flex containers, each item a container again.
		"flexnest":  strings.Repeat(`<div style="display:flex">`, 400) + "x" + strings.Repeat("</div>", 400),
		"selectors": "<style>" + strings.Repeat("a", 10000) + strings.Repeat(" > b", 5000) + "{color:red}" + strings.Repeat("p,", 20000) + "p{color:red}</style><p>x",
	}
	for name, html := range cases {
		start := time.Now()
		data, err := ConvertString(context.Background(), html, nil)
		d := time.Since(start)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if d > 5*time.Second {
			t.Errorf("%s took %v", name, d)
		}
		checkPackage(t, unzip(t, data))
		t.Logf("%-12s in %7.2f MB  out %7.2f MB  %v", name, float64(len(html))/1e6, float64(len(data))/1e6, d.Round(time.Millisecond))
	}
}
