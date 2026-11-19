//go:build !darwin

package aacenc

import "testing"

func TestUnsupported(t *testing.T) {
	e, err := New()
	if e != nil {
		t.Error("an encoder")
	}
	assertRoute(t, err, "New.AACEncoderUnsupported")
}
