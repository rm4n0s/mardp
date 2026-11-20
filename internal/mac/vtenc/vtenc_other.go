//go:build !darwin

package vtenc

import (
	"image"

	"github.com/rm4n0s/errors"
)

// New fails: VideoToolbox is only on macOS.
func New(opts Options, frame func(*Frame)) (*Encoder, error) {
	return nil, errors.New(TagEncoderUnsupported, "the H.264 encoder runs only on macOS")
}

// SubmitPattern does nothing: there is no encoder on other systems.
func (e *Encoder) SubmitPattern(n uint32) int { return -1 }

// SubmitText does nothing: there is no encoder on other systems.
func (e *Encoder) SubmitText(page uint32) int { return -1 }

// TextPage is nil: the text pattern is drawn only on macOS.
func TextPage(width, height int, page uint32) *image.RGBA { return nil }
