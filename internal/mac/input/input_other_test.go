//go:build !darwin

package input

import "testing"

func TestUnsupported(t *testing.T) {
	in, err := New(Options{})
	if in != nil {
		t.Error("an input")
	}
	assertRoute(t, err, "New.InputUnsupported")
}

func TestPointerUnsupported(t *testing.T) {
	p, err := NewPointer(PointerOptions{})
	if p != nil {
		t.Error("a pointer")
	}
	assertRoute(t, err, "NewPointer.InputUnsupported")
}
