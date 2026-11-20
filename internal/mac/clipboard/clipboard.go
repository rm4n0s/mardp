// Package clipboard is the Mac's clipboard, its text alone (SPEC §10.6):
// the general pasteboard of AppKit, which every app copies to and pastes
// from. It implements host.Clipboard.
package clipboard

import "github.com/rm4n0s/mardp/internal/host"

// Tags of the errors this package returns.
const (
	// TagClipboardUnsupported: not macOS.
	TagClipboardUnsupported = "ClipboardUnsupported"
	// TagClipboardWriteFailed: the pasteboard did not take the text.
	TagClipboardWriteFailed = "ClipboardWriteFailed"
)

// Access is what macOS does when the app reads the clipboard without the
// user pasting.
type Access string

// The values of Access.
const (
	AccessDefault Access = "default"
	AccessAllowed Access = "allowed"
	AccessAsks    Access = "asks"
	AccessRefused Access = "refused"
)

// Clipboard implements host.Clipboard.
var _ host.Clipboard = (*Clipboard)(nil)
