//go:build !darwin

package vtenc

import "testing"

func TestUnsupported(t *testing.T) {
	e, err := New(Options{Width: 64, Height: 64}, func(*Frame) {})
	if e != nil {
		t.Error("an encoder")
	}
	assertRoute(t, err, "New.EncoderUnsupported")
}
