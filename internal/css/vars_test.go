package css

import (
	"strings"
	"testing"
)

func TestSubstituteVars(t *testing.T) {
	vars := map[string]string{
		"--a":     "red",
		"--c":     "#fff",
		"--Mixed": "1px",
		"--empty": "",
	}
	lookup := func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"var(--a)", "red", true},
		{"1px solid var(--c)", "1px solid #fff", true},
		{"var( --a )", "red", true},
		{"VAR(--a)", "red", true},
		{"calc(var(--Mixed) * 2)", "calc(1px * 2)", true},
		{"var(--a) var(--c)", "red #fff", true},
		{"var(--missing, blue)", "blue", true},
		{"var(--missing,blue)", "blue", true},
		{"var(--missing, Arial, sans-serif)", "Arial, sans-serif", true},
		{"var(--missing, var(--a))", "red", true},
		{"var(--missing, var(--also-missing, 2px))", "2px", true},
		{"var(--a, var(--missing))", "red", true}, // an unused fallback does not matter
		{"var(--empty, blue)", "", true},          // defined but empty is still defined
		{"var(--mixed)", "", false},               // names are case sensitive
		{"var(--missing)", "", false},
		{"var(--missing, var(--also-missing))", "", false},
		{"var(--a", "", false},
		{"var(a)", "", false},
		{"var()", "", false},
		{`"var(--a)" var(--a)`, `"var(--a)" red`, true}, // not a function inside a string
		{"no variables", "no variables", true},
	}
	for _, tt := range tests {
		got, ok := SubstituteVars(tt.in, lookup)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("SubstituteVars(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSubstituteVarsLimitsLength(t *testing.T) {
	big := strings.Repeat("x", MaxVarLength/2+1)
	lookup := func(string) (string, bool) { return big, true }
	if _, ok := SubstituteVars("var(--a) var(--a)", lookup); ok {
		t.Error("a substituted value longer than MaxVarLength should be invalid")
	}
	if got, ok := SubstituteVars("var(--a)", lookup); !ok || got != big {
		t.Error("a value within MaxVarLength should be substituted")
	}
}
