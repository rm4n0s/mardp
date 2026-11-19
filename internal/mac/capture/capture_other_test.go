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

func TestSoundUnsupported(t *testing.T) {
	s, err := NewSound(Options{})
	if s != nil {
		t.Error("a sound source")
	}
	assertRoute(t, err, "NewSound.CaptureUnsupported")
}
