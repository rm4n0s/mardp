//go:build !darwin

package aacenc

import "github.com/rm4n0s/errors"

// New fails: AudioToolbox is only on macOS.
func New() (*Encoder, error) {
	return nil, errors.New(TagEncoderUnsupported, "the AAC encoder runs only on macOS")
}
