//go:build !darwin

package vtdec

import (
	"testing"

	"github.com/rm4n0s/errors"
)

func TestUnsupported(t *testing.T) {
	d := New()
	defer d.Close()
	img, err := d.Decode([]byte{0, 0, 0, 1, 0x65, 1})
	if img != nil {
		t.Error("a picture")
	}
	e, ok := errors.FromError(err)
	if !ok || !e.HasRoute("Decoder.Decode.DecoderUnsupported") {
		t.Errorf("error %v", err)
	}
}
