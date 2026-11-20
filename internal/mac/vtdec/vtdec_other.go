//go:build !darwin

package vtdec

import (
	"image"

	"github.com/rm4n0s/errors"
)

// Decoder decodes nothing: VideoToolbox is only on macOS.
type Decoder struct{}

// New makes a Decoder whose Decode fails.
func New() *Decoder { return &Decoder{} }

// Decode fails: VideoToolbox is only on macOS.
func (d *Decoder) Decode(annexB []byte) (*image.RGBA, error) {
	return nil, errors.New(TagDecoderUnsupported, "the H.264 decoder runs only on macOS")
}

// Close does nothing.
func (d *Decoder) Close() {}
