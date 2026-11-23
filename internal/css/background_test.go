package css

import (
	"math"
	"testing"
)

func TestGradientColor(t *testing.T) {
	tests := []struct {
		in   string
		want string // Hex of the result; "" means not a usable gradient
		a    float64
	}{
		{"linear-gradient(#000000, #0000fe)", "00007F", 1},
		{"linear-gradient(135deg, #0b1f3a 0%, #16345c 55%, #0f766e 140%)", "104357", 1},
		{"linear-gradient(to right, red, blue)", "800080", 1},
		{"LINEAR-GRADIENT(#ffffff, #ffffff)", "FFFFFF", 1},
		{"radial-gradient(circle at top left, #ff0000 10%, #00ff00)", "808000", 1},
		{"repeating-linear-gradient(45deg, #000 0 10px, #fff 10px 20px)", "808080", 1},
		{"conic-gradient(from 90deg, #ff0000, #0000ff)", "800080", 1},
		{"linear-gradient(rgba(0,0,0,0.5), rgba(0,0,0,0.5))", "000000", 0.5},
		{"linear-gradient(to right, transparent, transparent)", "", 0},
		// Transparent stops thin the colour rather than darkening it.
		{"linear-gradient(transparent, #ff0000)", "FF0000", 0.5},
		{"linear-gradient(to right)", "", 0},
		{"url(x.png)", "", 0},
		{"#ff0000", "", 0},
	}
	for _, tt := range tests {
		got, ok := GradientColor(tt.in)
		if (tt.want != "") != ok {
			t.Errorf("GradientColor(%q) ok = %v, want %v", tt.in, ok, tt.want != "")
			continue
		}
		if ok && (got.Hex() != tt.want || math.Abs(got.A-tt.a) > 1e-9) {
			t.Errorf("GradientColor(%q) = %s/%v, want %s/%v", tt.in, got.Hex(), got.A, tt.want, tt.a)
		}
	}
}

func TestColorOver(t *testing.T) {
	navy := Color{0x0B, 0x1F, 0x3A, 1}
	if got := (Color{255, 255, 255, 0.12}).Over(navy).Hex(); got != "283A52" {
		t.Errorf("white at 12%% over navy = %s, want 283A52", got)
	}
	if got := (Color{255, 0, 0, 1}).Over(navy).Hex(); got != "FF0000" {
		t.Errorf("an opaque colour should not change, got %s", got)
	}
	if got := (Color{0, 0, 0, 0.5}).Over(White).Hex(); got != "808080" {
		t.Errorf("black at 50%% over white = %s, want 808080", got)
	}
}

func TestContrast(t *testing.T) {
	black, white := Color{0, 0, 0, 1}, White
	if c := black.Contrast(white); math.Abs(c-21) > 0.01 {
		t.Errorf("black on white = %v, want 21", c)
	}
	if c := white.Contrast(white); c != 1 {
		t.Errorf("white on white = %v, want 1", c)
	}
	if c := white.Contrast(black); math.Abs(c-21) > 0.01 {
		t.Errorf("contrast should be symmetric, got %v", c)
	}
	grey := Color{0x77, 0x77, 0x77, 1}
	if c := grey.Contrast(white); c < 4 || c > 5 {
		t.Errorf("#777 on white = %v, want about 4.5", c)
	}
}
