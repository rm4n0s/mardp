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
