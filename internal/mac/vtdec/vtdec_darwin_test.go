//go:build darwin

package vtdec

import (
	"testing"

	"github.com/rm4n0s/errors"
)

// assertRoute fails t unless err is an *errors.Error with route.
func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s, want %s", e.Route(), route)
	}
}

// TestDecodeRefuses: what Decode refuses before VideoToolbox decodes.
func TestDecodeRefuses(t *testing.T) {
	for _, tt := range []struct {
		name  string
		in    []byte
		route string
	}{
		{"a frame before any key frame", []byte{0, 0, 0, 1, 0x41, 1, 2}, "Decoder.Decode.DecoderNoParameters"},
		{"nothing", nil, "Decoder.Decode.DecoderNoParameters"},
		{"parameter sets that are none", []byte{0, 0, 0, 1, 0x67, 0xff, 0, 0, 0, 1, 0x68, 0xff, 0, 0, 0, 1, 0x65, 1}, "Decoder.Decode.DecoderCreateFailed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := New()
			defer d.Close()
			img, err := d.Decode(tt.in)
			if img != nil {
				t.Error("a picture")
			}
			assertRoute(t, err, tt.route)
		})
	}
}

// TestClose: Close may be called at any time, more than once.
func TestClose(t *testing.T) {
	d := New()
	d.Close()
	d.Close()
	if _, err := d.Decode([]byte{0, 0, 0, 1, 0x41, 1}); err == nil {
		t.Error("a closed decoder decoded")
	}
}
