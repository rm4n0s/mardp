//go:build !darwin

package capture

import "github.com/rm4n0s/errors"

// New fails: ScreenCaptureKit is only on macOS.
func New(opts Options) (*Source, error) {
	return nil, errors.New(TagCaptureUnsupported, "screen capture runs only on macOS")
}
