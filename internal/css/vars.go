package css

import "strings"

// MaxVarLength caps the length of a value after var() substitution. Variables
// that refer to each other can double in size at every step ("--b: var(--a)
// var(--a)"), so without a cap a few hundred bytes of CSS could ask for
// gigabytes. Real values (colours, lengths, font lists) are far shorter.
const MaxVarLength = 8 << 10

// maxVarNesting caps how deeply fallbacks may nest: var(--a, var(--b, ...)).
const maxVarNesting = 32

// IsCustomProperty reports whether a property name is a custom property
// ("--name"), the kind var() refers to.
func IsCustomProperty(prop string) bool {
	return len(prop) > 2 && prop[0] == '-' && prop[1] == '-'
}

// HasVar reports whether val contains a var() reference.
func HasVar(val string) bool { return indexVar(val, 0) >= 0 }

// SubstituteVars replaces every var(--name) and var(--name, fallback) in val.
// lookup returns the value of a custom property and whether it is defined.
// The fallback is used only when the property is not defined. ok is false
// when a reference can be resolved neither way, when var() is malformed, or
// when the result would be longer than MaxVarLength; the CSS specification
// calls such a declaration invalid at computed-value time.
func SubstituteVars(val string, lookup func(name string) (string, bool)) (string, bool) {
	return substitute(val, lookup, 0)
}

func substitute(val string, lookup func(string) (string, bool), depth int) (string, bool) {
	start := indexVar(val, 0)
	if start < 0 {
		return val, true
	}
	if depth >= maxVarNesting {
		return "", false
	}
	var b strings.Builder
	for start >= 0 {
		b.WriteString(val[:start])
		open := start + len("var(")
		end := closeParen(val, open)
		if end < 0 {
			return "", false
		}
		args := val[open:end]
		name, fallback, hasFallback := args, "", false
		if comma := scanTo(args, 0, ","); comma < len(args) {
			name, fallback, hasFallback = args[:comma], strings.TrimSpace(args[comma+1:]), true
		}
		name = strings.TrimSpace(name)
		if !IsCustomProperty(name) {
			return "", false
		}
		sub, ok := lookup(name)
		if !ok {
			if !hasFallback {
				return "", false
			}
			if sub, ok = substitute(fallback, lookup, depth+1); !ok {
				return "", false
			}
		}
		if b.Len()+len(sub) > MaxVarLength {
			return "", false
		}
		b.WriteString(sub)
		val = val[end+1:]
		start = indexVar(val, 0)
	}
	if b.Len()+len(val) > MaxVarLength {
		return "", false
	}
	b.WriteString(val)
	return b.String(), true
}

// indexVar returns the index of the next "var(" at or after i that is a
// function call (not inside a string, not the tail of a longer name), or -1.
// Function names are not case sensitive.
func indexVar(s string, i int) int {
	var quote byte
	for ; i+4 <= len(s); i++ {
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
		case (c == 'v' || c == 'V') && strings.EqualFold(s[i:i+4], "var(") &&
			(i == 0 || !isNameByte(s[i-1])):
			return i
		}
	}
	return -1
}

func isNameByte(c byte) bool {
	return c == '-' || c == '_' || c >= 0x80 ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// closeParen returns the index of the ")" that closes a function whose
// arguments start at i, skipping nested parentheses and strings, or -1.
func closeParen(s string, i int) int {
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
		case c == '(':
			depth++
		case c == ')':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}
