package svg

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestEncodeKeepsDrawing(t *testing.T) {
	im := parseInline(t, `<svg viewBox="0 0 24 24" width="24" height="24" fill="none" class="icon">
		<title>Logo &amp; mark</title>
		<defs><linearGradient id="g"><stop offset="0" stop-color="#fff"/></linearGradient></defs>
		<path d="M4 17L10 7" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/>
		<rect width="4" height="4" fill="url(#g)" style="opacity:.5"/>
		<use href="#g"/>
		<text x="1" y="20" font-size="4">Q&lt;3</text>
	</svg>`)
	out := string(im.Encode(48, 48, "1E293B"))
	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="48" height="48" viewBox="0 0 24 24" color="#1E293B" fill="none">`,
		`<title>Logo &amp; mark</title>`,
		`<linearGradient id="g"><stop offset="0" stop-color="#fff"/></linearGradient>`,
		`<path d="M4 17L10 7" stroke="currentColor" stroke-linecap="round" stroke-width="2.4"/>`,
		`<rect fill="url(#g)" height="4" opacity=".5" width="4"/>`,
		`<use xlink:href="#g"/>`,
		`<text font-size="4" x="1" y="20">Q&lt;3</text>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "class=") {
		t.Errorf("class should be dropped: %s", out)
	}
	wellFormedXML(t, out)
}

// Untrusted SVG must not carry scripts, event handlers, external references
// or embedded HTML into the document.
func TestEncodeRemovesActiveContent(t *testing.T) {
	im := parseInline(t, `<svg viewBox="0 0 10 10" onload="alert(1)">
		<script>alert(1)</script>
		<style>@import url(http://evil.example/x.css);</style>
		<foreignObject><div>html</div></foreignObject>
		<image href="http://evil.example/track.png" width="1" height="1"/>
		<a href="javascript:alert(1)"><rect width="1" height="1" onclick="alert(1)"/></a>
		<rect width="2" height="2" fill="url(http://evil.example/p.svg#x)" filter="url(#f)"/>
		<use href="http://evil.example/sprite.svg#icon"/>
		<animate attributeName="href" to="javascript:alert(1)"/>
		<set attributeName="fill" to="red"/>
	</svg>`)
	out := string(im.Encode(10, 10, ""))
	for _, bad := range []string{"script", "alert", "evil", "@import", "foreignObject", "<image", "onclick", "onload", "animate", "<set", "javascript", "filter"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q:\n%s", bad, out)
		}
	}
	// The link's content is still drawn.
	if !strings.Contains(out, `<g><rect height="1" width="1"/></g>`) {
		t.Errorf("link content missing:\n%s", out)
	}
	wellFormedXML(t, out)
}

func TestEncodeSizesDrawingWithoutViewBox(t *testing.T) {
	out := string(parseInline(t, `<svg width="20" height="10"><rect width="20" height="10"/></svg>`).Encode(40, 20, ""))
	if !strings.Contains(out, `width="40" height="20" viewBox="0 0 20 10"`) {
		t.Errorf("got %s", out)
	}
	out = string(parseInline(t, `<svg><rect width="20" height="10"/></svg>`).Encode(30, 15, ""))
	if !strings.Contains(out, `width="30" height="15" viewBox="0 0 30 15"`) {
		t.Errorf("got %s", out)
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	src := `<svg viewBox="0 0 1 1"><rect a="1" b="2" fill="red" stroke="blue" x="0" y="0" width="1" height="1" rx="0" ry="0"/></svg>`
	first := string(parseInline(t, src).Encode(1, 1, ""))
	for i := 0; i < 20; i++ {
		if got := string(parseInline(t, src).Encode(1, 1, "")); got != first {
			t.Fatalf("output differs between runs:\n%s\n%s", first, got)
		}
	}
}

func wellFormedXML(t *testing.T, s string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("not well formed: %v\n%s", err, s)
		}
	}
}
