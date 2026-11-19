//go:build !darwin

package capture

import "testing"

func TestUnsupported(t *testing.T) {
	s, err := New(Options{})
	if s != nil {
		t.Error("a source")
	}
	assertRoute(t, err, "New.CaptureUnsupported")
}
