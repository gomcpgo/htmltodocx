package css

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ContentKind is the kind of one item of a content property.
type ContentKind uint8

const (
	ContentText       ContentKind = iota // Text
	ContentCounter                       // counter(Name, Style)
	ContentCounters                      // counters(Name, Text, Style)
	ContentAttr                          // attr(Name)
	ContentOpenQuote                     // open-quote
	ContentCloseQuote                    // close-quote
	ContentURL                           // url(...): an image, which is not supported
)

// ContentItem is one item of the content property of ::before or ::after.
// For counters(), Text is the separator.
type ContentItem struct {
	Kind  ContentKind
	Text  string
	Name  string
	Style string
}

// ParseContent parses the value of the content property. "none" and
// "normal" give no items. ok is false for a value that is not valid, which
// a browser would ignore. Alternative text after a "/" is skipped.
func ParseContent(v string) (items []ContentItem, ok bool) {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "none", "normal":
		return nil, true
	}
	i := 0
	for {
		for i < len(v) && isSpace(v[i]) {
			i++
		}
		if i >= len(v) || v[i] == '/' {
			break
		}
		if c := v[i]; c == '"' || c == '\'' {
			var text string
			text, i = readString(v, i)
			items = append(items, ContentItem{Kind: ContentText, Text: text})
			continue
		}
		start := i
		for i < len(v) && isNameByte(v[i]) {
			i++
		}
		name := strings.ToLower(v[start:i])
		if name == "" {
			return nil, false
		}
		if i >= len(v) || v[i] != '(' {
			switch name {
			case "open-quote":
				items = append(items, ContentItem{Kind: ContentOpenQuote})
			case "close-quote":
				items = append(items, ContentItem{Kind: ContentCloseQuote})
			case "no-open-quote", "no-close-quote":
			default:
				return nil, false
			}
			continue
		}
		end := closeParen(v, i+1)
		if end < 0 {
			end = len(v)
		}
		args := SplitComma(v[i+1 : end])
		i = min(end+1, len(v))
		item, ok := contentFunction(name, args)
		if !ok {
			return nil, false
		}
		items = append(items, item)
	}
	return items, len(items) > 0
}

func contentFunction(name string, args []string) (ContentItem, bool) {
	ident := func(k int) string {
		if k >= len(args) || !isIdent(args[k]) {
			return ""
		}
		return args[k]
	}
	switch name {
	case "url":
		return ContentItem{Kind: ContentURL}, true
	case "attr":
		if n := ident(0); n != "" && len(args) == 1 {
			return ContentItem{Kind: ContentAttr, Name: strings.ToLower(n)}, true
		}
	case "counter":
		n := ident(0)
		if n == "" || len(args) > 2 {
			break
		}
		item := ContentItem{Kind: ContentCounter, Name: n}
		if len(args) == 2 {
			if item.Style = strings.ToLower(ident(1)); item.Style == "" {
				break
			}
		}
		return item, true
	case "counters":
		n := ident(0)
		if n == "" || len(args) < 2 || len(args) > 3 || args[1] == "" || (args[1][0] != '"' && args[1][0] != '\'') {
			break
		}
		sep, _ := readString(args[1], 0)
		item := ContentItem{Kind: ContentCounters, Name: n, Text: sep}
		if len(args) == 3 {
			if item.Style = strings.ToLower(ident(2)); item.Style == "" {
				break
			}
		}
		return item, true
	}
	return ContentItem{}, false
}

func isIdent(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isNameByte(s[i]) {
			return false
		}
	}
	return true
}

// readString reads the quoted string that starts at s[i] and returns its
// value with escapes resolved, and the index after the closing quote. An
// unterminated string runs to the end of s.
func readString(s string, i int) (string, int) {
	quote := s[i]
	var b strings.Builder
	i++
	for i < len(s) {
		c := s[i]
		switch {
		case c == quote:
			return b.String(), i + 1
		case c == '\\' && i+1 < len(s):
			i++
			if s[i] == '\n' {
				i++ // a line continuation
				continue
			}
			j := i
			for j < len(s) && j-i < 6 && isHex(s[j]) {
				j++
			}
			if j == i {
				r, size := utf8.DecodeRuneInString(s[i:])
				b.WriteRune(r)
				i += size
				continue
			}
			cp, _ := strconv.ParseUint(s[i:j], 16, 32)
			if cp == 0 || cp > utf8.MaxRune || (cp >= 0xD800 && cp <= 0xDFFF) {
				cp = utf8.RuneError
			}
			b.WriteRune(rune(cp))
			i = j
			if i < len(s) && isSpace(s[i]) {
				i++ // one white space ends the escape
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), i
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// CounterOp is one counter of counter-reset, counter-set or
// counter-increment, with its value.
type CounterOp struct {
	Name  string
	Value int
}

// ParseCounterOps parses counter-reset, counter-set or counter-increment.
// def is the value of a counter given without one.
func ParseCounterOps(v string, def int) []CounterOp {
	var out []CounterOp
	for _, f := range Fields(v) {
		if n, err := strconv.ParseInt(f, 10, 32); err == nil || isNumber(f) {
			if len(out) > 0 && err == nil {
				out[len(out)-1].Value = int(n)
			}
			continue
		}
		if !isIdent(f) {
			continue
		}
		switch strings.ToLower(f) {
		case "none", "inherit", "initial", "unset", "revert":
			continue
		}
		out = append(out, CounterOp{Name: f, Value: def})
	}
	return out
}

func isNumber(s string) bool {
	if s != "" && (s[0] == '-' || s[0] == '+') {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// FormatCounter writes a counter value in a list style. Unknown styles are
// decimal, and so are values an alphabetic or roman style cannot show.
func FormatCounter(v int, style string) string {
	switch style {
	case "none":
		return ""
	case "disc":
		return "•"
	case "circle":
		return "◦"
	case "square":
		return "▪"
	case "decimal-leading-zero":
		if v >= 0 && v < 10 {
			return "0" + strconv.Itoa(v)
		}
	case "lower-alpha", "lower-latin", "upper-alpha", "upper-latin":
		if v > 0 {
			var b []byte
			for n := v; n > 0; n = (n - 1) / 26 {
				b = append(b, byte('a'+(n-1)%26))
			}
			for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
				b[i], b[j] = b[j], b[i]
			}
			if strings.HasPrefix(style, "upper") {
				return strings.ToUpper(string(b))
			}
			return string(b)
		}
	case "lower-roman", "upper-roman":
		if v > 0 && v < 4000 {
			r := roman(v)
			if style == "lower-roman" {
				return strings.ToLower(r)
			}
			return r
		}
	}
	return strconv.Itoa(v)
}

func roman(v int) string {
	vals := [...]int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := [...]string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var b strings.Builder
	for i, n := range vals {
		for v >= n {
			b.WriteString(syms[i])
			v -= n
		}
	}
	return b.String()
}
