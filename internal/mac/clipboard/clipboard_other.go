//go:build !darwin

package clipboard

import "github.com/rm4n0s/errors"

// Clipboard is the stand-in on systems that are not macOS: New fails.
type Clipboard struct{}

// New fails: mardp's host runs only on macOS.
func New() (*Clipboard, error) {
	return nil, errors.New(TagClipboardUnsupported, "mardp's host runs only on macOS")
}

// Changes is always 0.
func (c *Clipboard) Changes() int64 { return 0 }

// Text has none.
func (c *Clipboard) Text() (string, bool) { return "", false }

// SetText fails.
func (c *Clipboard) SetText(string) error {
	return errors.New(TagClipboardUnsupported, "mardp's host runs only on macOS")
}
