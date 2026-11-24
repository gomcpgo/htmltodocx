package css

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Selector is one complex selector: compound selectors joined by
// combinators. parts[len-1] is the subject.
type Selector struct {
	parts []compound
	combs []byte // combs[i] joins parts[i] and parts[i+1]: ' ', '>', '+' or '~'
	spec  uint32
	// pseudoEl is Before or After for a selector of a pseudo-element, which
	// matches the generated box rather than the element itself.
	pseudoEl int
}

// Pseudo-elements that rules can select.
const (
	Before = 1
	After  = 2
)

type compound struct {
	tag      string // "" matches any element
	id       string
	classes  []string
	attrs    []attrSel
	pseudos  []pseudo
	pseudoEl int
}

type attrSel struct {
	name string
	op   byte // 0 for presence, else '=', '~', '|', '^', '$' or '*'
	val  string
}

const (
	psFirstChild = iota
	psLastChild
	psOnlyChild
	psNthChild
	psFirstOfType
	psLastOfType
	psRoot
	psLink
	psEmpty
	psNot
)

type pseudo struct {
	kind int
	a, b int       // nth-child(an+b)
	not  *compound // :not(...)
}

// matchBudget bounds the work of matching one selector against one element,
// so a hostile stylesheet full of descendant combinators cannot make matching
// exponential.
const matchBudget = 4096

const maxCompounds = 16

type selParser struct {
	s string
	i int
}

func parseSelector(s string) (*Selector, bool) {
	p := &selParser{s: strings.TrimSpace(s)}
	if p.s == "" {
		return nil, false
	}
	sel := &Selector{}
	for {
		space := p.skipSpace()
		if p.i >= len(p.s) {
			break
		}
		if len(sel.parts) > 0 {
			comb := byte(' ')
			if c := p.s[p.i]; c == '>' || c == '+' || c == '~' {
				comb = c
				p.i++
				p.skipSpace()
			} else if !space {
				return nil, false
			}
			sel.combs = append(sel.combs, comb)
		}
		c, ok := p.compound()
		if !ok || len(sel.parts) >= maxCompounds {
			return nil, false
		}
		sel.parts = append(sel.parts, c)
		sel.spec += c.specificity()
		if c.pseudoEl != 0 {
			// A pseudo-element ends the selector.
			p.skipSpace()
			if p.i < len(p.s) {
				return nil, false
			}
			sel.pseudoEl = c.pseudoEl
			sel.spec++
		}
	}
	if len(sel.parts) == 0 || len(sel.combs) != len(sel.parts)-1 {
		return nil, false
	}
	return sel, true
}

func (c *compound) specificity() uint32 {
	var s uint32
	if c.id != "" {
		s += 1 << 16
	}
	s += uint32(len(c.classes)+len(c.attrs)) << 8
	for _, p := range c.pseudos {
		if p.kind == psNot {
			s += p.not.specificity()
		} else {
			s += 1 << 8
		}
	}
	if c.tag != "" {
		s++
	}
	return s
}

func (p *selParser) skipSpace() bool {
	start := p.i
	for p.i < len(p.s) && isSpace(p.s[p.i]) {
		p.i++
	}
	return p.i > start
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func (p *selParser) ident() string {
	var b []byte
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '\\' && p.i+1 < len(p.s):
			b = append(b, p.s[p.i+1])
			p.i += 2
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c >= 0x80:
			b = append(b, c)
			p.i++
		default:
			return string(b)
		}
	}
	return string(b)
}

func (p *selParser) compound() (compound, bool) {
	var c compound
	start := p.i
	for p.i < len(p.s) {
		ch := p.s[p.i]
		switch {
		case ch == '*':
			if p.i != start {
				return c, false
			}
			p.i++
		case ch == '#':
			p.i++
			if c.id = p.ident(); c.id == "" {
				return c, false
			}
		case ch == '.':
			p.i++
			cls := p.ident()
			if cls == "" {
				return c, false
			}
			c.classes = append(c.classes, cls)
		case ch == '[':
			a, ok := p.attr()
			if !ok {
				return c, false
			}
			c.attrs = append(c.attrs, a)
		case ch == ':':
			if pe, ok := p.pseudoElement(); ok {
				if pe == 0 {
					return c, false
				}
				c.pseudoEl = pe
				// Nothing may follow a pseudo-element in a compound.
				if p.i < len(p.s) && !isSpace(p.s[p.i]) {
					return c, false
				}
				return c, true
			}
			ps, ok := p.pseudo()
			if !ok {
				return c, false
			}
			c.pseudos = append(c.pseudos, ps)
		case isSpace(ch) || ch == '>' || ch == '+' || ch == '~':
			return c, p.i > start
		default:
			if p.i != start {
				return c, false
			}
			tag := p.ident()
			if tag == "" {
				return c, false
			}
			c.tag = strings.ToLower(tag)
		}
	}
	return c, p.i > start
}

func (p *selParser) attr() (attrSel, bool) {
	var a attrSel
	end := scanTo(p.s, p.i+1, "]")
	if end >= len(p.s) {
		return a, false
	}
	body := strings.TrimSpace(p.s[p.i+1 : end])
	p.i = end + 1
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		a.name = strings.ToLower(body)
		return a, a.name != ""
	}
	name := body[:eq]
	a.op = '='
	if n := len(name); n > 0 && strings.IndexByte("~|^$*", name[n-1]) >= 0 {
		a.op = name[n-1]
		name = name[:n-1]
	}
	a.name = strings.ToLower(strings.TrimSpace(name))
	val := strings.TrimSpace(body[eq+1:])
	// Drop a trailing case flag: [type="a" i].
	if n := len(val); n > 2 && isSpace(val[n-2]) && (val[n-1] == 'i' || val[n-1] == 's') {
		val = strings.TrimSpace(val[:n-2])
	}
	if n := len(val); n >= 2 && (val[0] == '"' || val[0] == '\'') && val[n-1] == val[0] {
		val = val[1 : n-1]
	}
	a.val = val
	return a, a.name != ""
}

// pseudoElement parses "::before" and "::after", and the older one-colon
// forms. ok is false when the text is not a pseudo-element; pe is 0 for one
// that is not supported, such as ::marker.
func (p *selParser) pseudoElement() (pe int, ok bool) {
	start := p.i
	p.i++ // ':'
	double := p.i < len(p.s) && p.s[p.i] == ':'
	if double {
		p.i++
	}
	name := strings.ToLower(p.ident())
	if p.i < len(p.s) && p.s[p.i] == '(' {
		name = ""
	}
	switch {
	case name == "before":
		return Before, true
	case name == "after":
		return After, true
	case double:
		return 0, true
	}
	p.i = start
	return 0, false
}

func (p *selParser) pseudo() (pseudo, bool) {
	var ps pseudo
	p.i++ // ':'
	if p.i < len(p.s) && p.s[p.i] == ':' {
		return ps, false // pseudo-element
	}
	name := strings.ToLower(p.ident())
	arg := ""
	if p.i < len(p.s) && p.s[p.i] == '(' {
		end := scanTo(p.s, p.i+1, ")")
		if end >= len(p.s) {
			return ps, false
		}
		arg = strings.TrimSpace(p.s[p.i+1 : end])
		p.i = end + 1
	}
	switch name {
	case "first-child":
		ps.kind = psFirstChild
	case "last-child":
		ps.kind = psLastChild
	case "only-child":
		ps.kind = psOnlyChild
	case "first-of-type":
		ps.kind = psFirstOfType
	case "last-of-type":
		ps.kind = psLastOfType
	case "root":
		ps.kind = psRoot
	case "link", "any-link":
		ps.kind = psLink
	case "empty":
		ps.kind = psEmpty
	case "nth-child":
		ps.kind = psNthChild
		var ok bool
		if ps.a, ps.b, ok = parseNth(arg); !ok {
			return ps, false
		}
	case "not":
		ps.kind = psNot
		inner := &selParser{s: arg}
		c, ok := inner.compound()
		if !ok || inner.i != len(arg) || c.pseudoEl != 0 {
			return ps, false
		}
		ps.not = &c
	default:
		// State pseudo-classes (:hover, :focus, ...) never apply to a
		// document, and pseudo-elements create no element to style.
		return ps, false
	}
	return ps, true
}

// parseNth parses the argument of :nth-child: "odd", "even", "3", "2n+1".
func parseNth(s string) (a, b int, ok bool) {
	s = strings.ToLower(strings.Join(strings.Fields(s), ""))
	switch s {
	case "odd":
		return 2, 1, true
	case "even":
		return 2, 0, true
	case "":
		return 0, 0, false
	}
	n := strings.IndexByte(s, 'n')
	if n < 0 {
		v, err := strconv.Atoi(s)
		return 0, v, err == nil
	}
	switch as := s[:n]; as {
	case "", "+":
		a = 1
	case "-":
		a = -1
	default:
		v, err := strconv.Atoi(as)
		if err != nil {
			return 0, 0, false
		}
		a = v
	}
	if rest := s[n+1:]; rest != "" {
		v, err := strconv.Atoi(rest)
		if err != nil {
			return 0, 0, false
		}
		b = v
	}
	return a, b, true
}

func parentElement(n *html.Node) *html.Node {
	if p := n.Parent; p != nil && p.Type == html.ElementNode {
		return p
	}
	return nil
}

func prevElement(n *html.Node) *html.Node {
	for s := n.PrevSibling; s != nil; s = s.PrevSibling {
		if s.Type == html.ElementNode {
			return s
		}
	}
	return nil
}

func nextElement(n *html.Node) *html.Node {
	for s := n.NextSibling; s != nil; s = s.NextSibling {
		if s.Type == html.ElementNode {
			return s
		}
	}
	return nil
}

// match reports whether parts[0..i] match with parts[i] on n.
func (s *Stylesheet) match(sel *Selector, i int, n *html.Node, budget *int) bool {
	*budget--
	if *budget < 0 || !s.matchCompound(&sel.parts[i], n) {
		return false
	}
	if i == 0 {
		return true
	}
	switch sel.combs[i-1] {
	case '>':
		p := parentElement(n)
		return p != nil && s.match(sel, i-1, p, budget)
	case '+':
		p := prevElement(n)
		return p != nil && s.match(sel, i-1, p, budget)
	case '~':
		for p := prevElement(n); p != nil; p = prevElement(p) {
			if s.match(sel, i-1, p, budget) {
				return true
			}
		}
	default:
		for p := parentElement(n); p != nil; p = parentElement(p) {
			if s.match(sel, i-1, p, budget) {
				return true
			}
		}
	}
	return false
}

func attrValue(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name && a.Namespace == "" {
			return a.Val, true
		}
	}
	return "", false
}

func hasClass(v, cls string) bool {
	for {
		i := strings.Index(v, cls)
		if i < 0 {
			return false
		}
		end := i + len(cls)
		if (i == 0 || isSpace(v[i-1])) && (end == len(v) || isSpace(v[end])) {
			return true
		}
		v = v[i+1:]
	}
}

func (s *Stylesheet) matchCompound(c *compound, n *html.Node) bool {
	if c.tag != "" && c.tag != n.Data {
		return false
	}
	if c.id != "" {
		if v, _ := attrValue(n, "id"); v != c.id {
			return false
		}
	}
	if len(c.classes) > 0 {
		v, _ := attrValue(n, "class")
		for _, cls := range c.classes {
			if !hasClass(v, cls) {
				return false
			}
		}
	}
	for i := range c.attrs {
		if !matchAttr(&c.attrs[i], n) {
			return false
		}
	}
	for i := range c.pseudos {
		if !s.matchPseudo(&c.pseudos[i], n) {
			return false
		}
	}
	return true
}

func matchAttr(a *attrSel, n *html.Node) bool {
	v, ok := attrValue(n, a.name)
	if !ok {
		return false
	}
	switch a.op {
	case 0:
		return true
	case '=':
		return v == a.val
	case '~':
		return a.val != "" && hasClass(v, a.val)
	case '|':
		return v == a.val || strings.HasPrefix(v, a.val+"-")
	case '^':
		return a.val != "" && strings.HasPrefix(v, a.val)
	case '$':
		return a.val != "" && strings.HasSuffix(v, a.val)
	case '*':
		return a.val != "" && strings.Contains(v, a.val)
	}
	return false
}

func (s *Stylesheet) matchPseudo(p *pseudo, n *html.Node) bool {
	switch p.kind {
	case psFirstChild:
		return prevElement(n) == nil
	case psLastChild:
		return nextElement(n) == nil
	case psOnlyChild:
		return prevElement(n) == nil && nextElement(n) == nil
	case psFirstOfType:
		for x := prevElement(n); x != nil; x = prevElement(x) {
			if x.Data == n.Data {
				return false
			}
		}
		return true
	case psLastOfType:
		for x := nextElement(n); x != nil; x = nextElement(x) {
			if x.Data == n.Data {
				return false
			}
		}
		return true
	case psRoot:
		return parentElement(n) == nil
	case psLink:
		_, ok := attrValue(n, "href")
		return ok && (n.Data == "a" || n.Data == "area")
	case psEmpty:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode || (c.Type == html.TextNode && c.Data != "") {
				return false
			}
		}
		return true
	case psNthChild:
		idx := s.childIndex(n)
		if p.a == 0 {
			return idx == p.b
		}
		d := idx - p.b
		return d%p.a == 0 && d/p.a >= 0
	case psNot:
		return !s.matchCompound(p.not, n)
	}
	return false
}

// childIndex returns the one-based position of n among its element siblings.
// Positions are cached for all siblings at once so that striping a long table
// with :nth-child stays linear.
func (s *Stylesheet) childIndex(n *html.Node) int {
	if idx, ok := s.nthCache[n]; ok {
		return idx
	}
	if n.Parent == nil {
		return 1
	}
	i := 0
	for c := n.Parent.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			i++
			s.nthCache[c] = i
		}
	}
	return s.nthCache[n]
}
