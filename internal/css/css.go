// Package css is a small CSS engine: a declaration parser, a stylesheet
// parser and a selector matcher that works on golang.org/x/net/html nodes.
//
// It is written from the CSS specifications and deliberately covers only what
// a document converter can use. It does not build a full cascade; callers ask
// which rules match an element and apply the declarations themselves.
package css

import (
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// Decl is one "property: value" declaration. Prop is lower case; Value keeps
// its original case because font names and URLs are case sensitive.
type Decl struct {
	Prop      string
	Value     string
	Important bool
}

// ParseDeclarations parses the contents of a style attribute or of a rule
// body. Custom properties ("--name") keep the case of their name, which is
// significant; values that use var() are kept as written and resolved by the
// caller with SubstituteVars.
func ParseDeclarations(s string) []Decl {
	var out []Decl
	for len(s) > 0 {
		end := scanTo(s, 0, ";")
		part := s[:end]
		if end < len(s) {
			s = s[end+1:]
		} else {
			s = ""
		}
		colon := strings.IndexByte(part, ':')
		if colon <= 0 {
			continue
		}
		prop := strings.TrimSpace(part[:colon])
		if !IsCustomProperty(prop) {
			prop = strings.ToLower(prop)
		}
		val := strings.TrimSpace(part[colon+1:])
		important := false
		if bang := strings.LastIndexByte(val, '!'); bang >= 0 &&
			strings.EqualFold(strings.TrimSpace(val[bang+1:]), "important") {
			important = true
			val = strings.TrimSpace(val[:bang])
		}
		if prop == "" || val == "" {
			continue
		}
		out = append(out, Decl{prop, val, important})
	}
	return out
}

// scanTo returns the index of the first byte at or after i that is in stops
// and is not inside a string, parentheses or square brackets. It returns
// len(s) if there is none.
func scanTo(s string, i int, stops string) int {
	depth := 0
	var quote byte
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case depth == 0 && strings.IndexByte(stops, c) >= 0:
			return i
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return len(s)
}

// matchBrace returns the index of the "}" that closes the "{" at open, or
// len(s) if the block is not closed.
func matchBrace(s string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote || c == '\n' {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s)
}

func stripComments(s string) string {
	if !strings.Contains(s, "/*") && !strings.Contains(s, "<!--") && !strings.Contains(s, "-->") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		b.WriteByte(' ')
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			break
		}
		s = s[i+2+j+2:]
	}
	out := strings.ReplaceAll(b.String(), "<!--", " ")
	return strings.ReplaceAll(out, "-->", " ")
}

type ruleSel struct {
	sel   *Selector
	decls []Decl
	order int
}

// Stylesheet is a set of style rules, indexed for matching.
type Stylesheet struct {
	byID      map[string][]*ruleSel
	byClass   map[string][]*ruleSel
	byTag     map[string][]*ruleSel
	universal []*ruleSel
	order     int
	nthCache  map[*html.Node]int
}

// NewStylesheet returns an empty stylesheet.
func NewStylesheet() *Stylesheet {
	return &Stylesheet{
		byID:     map[string][]*ruleSel{},
		byClass:  map[string][]*ruleSel{},
		byTag:    map[string][]*ruleSel{},
		nthCache: map[*html.Node]int{},
	}
}

// Len returns the number of selector/rule pairs in the sheet.
func (s *Stylesheet) Len() int { return s.order }

// Add parses CSS text and appends its rules. Later rules win over earlier
// ones of equal specificity, across calls to Add.
func (s *Stylesheet) Add(text string) {
	s.parse(stripComments(text), 0)
}

const maxAtRuleDepth = 8

func (s *Stylesheet) parse(t string, depth int) {
	i := 0
	for i < len(t) {
		j := scanTo(t, i, "{;}")
		if j >= len(t) {
			return
		}
		if t[j] != '{' {
			i = j + 1 // statement at-rule, or stray text
			continue
		}
		prelude := strings.TrimSpace(t[i:j])
		end := matchBrace(t, j)
		body := t[j+1 : end]
		i = end + 1

		if strings.HasPrefix(prelude, "@") {
			name, rest := prelude, ""
			if k := strings.IndexAny(prelude, " \t\n\r\f("); k > 0 {
				name, rest = prelude[:k], prelude[k:]
			}
			if depth >= maxAtRuleDepth {
				continue
			}
			switch strings.ToLower(name) {
			case "@media":
				if mediaMatches(rest) {
					s.parse(body, depth+1)
				}
			case "@supports", "@layer":
				s.parse(body, depth+1)
			}
			continue
		}

		decls := ParseDeclarations(body)
		if len(decls) == 0 {
			continue
		}
		for _, text := range SplitComma(prelude) {
			sel, ok := parseSelector(text)
			if !ok {
				continue
			}
			s.order++
			rs := &ruleSel{sel, decls, s.order}
			last := &sel.parts[len(sel.parts)-1]
			switch {
			case last.id != "":
				s.byID[last.id] = append(s.byID[last.id], rs)
			case len(last.classes) > 0:
				s.byClass[last.classes[0]] = append(s.byClass[last.classes[0]], rs)
			case last.tag != "":
				s.byTag[last.tag] = append(s.byTag[last.tag], rs)
			default:
				s.universal = append(s.universal, rs)
			}
		}
	}
}

// mediaMatches reports whether a media query list applies to a printed
// document. Only the plain "all" and "print" types match; anything with a
// feature test (min-width and friends) is written for screens and is skipped.
func mediaMatches(q string) bool {
	for _, part := range strings.Split(q, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		part = strings.TrimSpace(strings.TrimPrefix(part, "only "))
		if part == "" || part == "all" || part == "print" {
			return true
		}
	}
	return false
}

// MediaMatches is mediaMatches for the media attribute of <style> and <link>.
func MediaMatches(q string) bool { return mediaMatches(q) }

// Matched is the declarations of one rule that matched an element.
type Matched struct {
	Decls []Decl
	spec  uint32
	order int
}

// Match appends to buf the rules that match n, from least to most specific,
// and returns the extended slice.
func (s *Stylesheet) Match(n *html.Node, buf []Matched) []Matched {
	if s.order == 0 {
		return buf
	}
	base := len(buf)
	try := func(list []*ruleSel) {
		for _, rs := range list {
			budget := matchBudget
			if s.match(rs.sel, len(rs.sel.parts)-1, n, &budget) {
				buf = append(buf, Matched{rs.decls, rs.sel.spec, rs.order})
			}
		}
	}
	for _, a := range n.Attr {
		if a.Namespace != "" {
			continue
		}
		switch a.Key {
		case "id":
			if len(s.byID) > 0 {
				try(s.byID[a.Val])
			}
		case "class":
			if len(s.byClass) > 0 {
				eachClass(a.Val, func(c string) { try(s.byClass[c]) })
			}
		}
	}
	try(s.byTag[n.Data])
	try(s.universal)
	if len(buf)-base > 1 {
		slices.SortFunc(buf[base:], func(a, b Matched) int {
			if a.spec != b.spec {
				if a.spec < b.spec {
					return -1
				}
				return 1
			}
			return a.order - b.order
		})
	}
	return buf
}

func eachClass(v string, fn func(string)) {
	start := -1
	for i := 0; i <= len(v); i++ {
		if i == len(v) || v[i] == ' ' || v[i] == '\t' || v[i] == '\n' || v[i] == '\r' || v[i] == '\f' {
			if start >= 0 {
				fn(v[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
}
