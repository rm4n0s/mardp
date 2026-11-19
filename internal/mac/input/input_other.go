//go:build !darwin

package input

import "github.com/rm4n0s/errors"

// New fails: Core Graphics is only on macOS.
func New(opts Options) (*Input, error) {
	return nil, errors.New(TagInputUnsupported, "keyboard and mouse input runs only on macOS")
}
