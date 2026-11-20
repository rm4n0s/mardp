//go:build !darwin

package clipboard

import (
	"testing"

	"github.com/rm4n0s/errors"
)

func TestUnsupported(t *testing.T) {
	_, err := New()
	appErr, ok := errors.FromError(err)
	if !ok || !appErr.HasRoute("New."+TagClipboardUnsupported) {
		t.Errorf("route of %v", err)
	}
	c := &Clipboard{}
	if _, ok := c.Text(); ok || c.Changes() != 0 {
		t.Error("the stand-in has a clipboard")
	}
	err = c.SetText("x")
	appErr, ok = errors.FromError(err)
	if !ok || !appErr.HasRoute("Clipboard.SetText."+TagClipboardUnsupported) {
		t.Errorf("route of %v", err)
	}
}
