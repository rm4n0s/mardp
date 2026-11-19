//go:build !darwin

package vtenc

import "github.com/rm4n0s/errors"

// New fails: VideoToolbox is only on macOS.
func New(opts Options, frame func(*Frame)) (*Encoder, error) {
	return nil, errors.New(TagEncoderUnsupported, "the H.264 encoder runs only on macOS")
}

// SubmitPattern does nothing: there is no encoder on other systems.
func (e *Encoder) SubmitPattern(n uint32) int { return -1 }
