//go:build !darwin

package input

import "github.com/rm4n0s/errors"

// NewPointer fails: AppKit is only on macOS.
func NewPointer(opts PointerOptions) (*Pointer, error) {
	return nil, errors.New(TagInputUnsupported, "reading the pointer runs only on macOS")
}
