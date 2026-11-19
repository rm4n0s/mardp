package main

import (
	"bytes"
	"image/png"
	"testing"
)

// TestIcon: a square PNG of macOS's size, clear outside the rounded body,
// opaque inside it, with the white screen frame.
func TestIcon(t *testing.T) {
	var buf bytes.Buffer
	if err := encode(&buf); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
		t.Fatalf("size %v", b)
	}
	tests := []struct {
		name       string
		x, y       int
		r, g, b, a uint32
	}{
		{"the margin", 10, 10, 0, 0, 0, 0},
		{"the corner outside the body", 110, 110, 0, 0, 0, 0},
		{"the screen's frame", 512, 290, 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF},
		{"the foot", 512, 730, 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF},
	}
	for _, tt := range tests {
		r, g, b, a := img.At(tt.x, tt.y).RGBA()
		if r != tt.r || g != tt.g || b != tt.b || a != tt.a {
			t.Errorf("%s (%d, %d): %x %x %x %x", tt.name, tt.x, tt.y, r, g, b, a)
		}
	}
	if _, _, b, a := img.At(512, 150).RGBA(); a != 0xFFFF || b < 0x8000 {
		t.Errorf("the body is not opaque blue at the top: blue %x alpha %x", b, a)
	}
}
