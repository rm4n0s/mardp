//go:build darwin

package clipboard

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
#include "clipboard_darwin.h"
*/
import "C"

import (
	"unsafe"

	"github.com/rm4n0s/errors"
)

// Clipboard is a pasteboard of macOS: the general one, or, in the tests, a
// named one of their own.
type Clipboard struct {
	// name is the pasteboard's name as C has it: nil is the general one.
	name *C.char
}

// New is the Mac's clipboard: the general pasteboard.
func New() (*Clipboard, error) { return &Clipboard{}, nil }

// Access says what macOS does when mardp reads the clipboard, which it
// does without the user pasting on the Mac (macOS 15.4 and newer;
// System Settings › Privacy & Security › Paste from Other Apps): AccessAllowed,
// AccessAsks, AccessRefused, or AccessDefault, which is macOS's choice.
func (c *Clipboard) Access() Access {
	switch C.mardpClipAccess() {
	case 1:
		return AccessAsks
	case 2:
		return AccessAllowed
	case 3:
		return AccessRefused
	case 0:
		return AccessDefault
	}
	return AccessAllowed // a macOS without the setting
}

// Changes is the pasteboard's change count: it goes up whenever any app
// changes what the pasteboard holds.
func (c *Clipboard) Changes() int64 { return int64(C.mardpClipChanges(c.name)) }

// Text is the pasteboard's text now, in whichever text format the app
// that copied it wrote; ok is false when it holds none (a picture, files,
// nothing).
func (c *Clipboard) Text() (string, bool) {
	var n C.long
	p := C.mardpClipText(c.name, &n)
	if p == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoStringN(p, C.int(n)), true
}

// SetText puts text on the pasteboard, in the place of what it held.
func (c *Clipboard) SetText(text string) error {
	p := C.CString(text)
	defer C.free(unsafe.Pointer(p))
	if C.mardpClipSetText(c.name, p, C.long(len(text))) == 0 {
		return errors.New(TagClipboardWriteFailed, "the pasteboard did not take the text", "bytes", len(text))
	}
	return nil
}

// setData puts data on the pasteboard as the type named and nothing else,
// as an app that writes one format only; the tests use it.
func (c *Clipboard) setData(typ string, data []byte) bool {
	t := C.CString(typ)
	defer C.free(unsafe.Pointer(t))
	p := C.CBytes(data)
	defer C.free(p)
	return C.mardpClipSetData(c.name, t, p, C.long(len(data))) != 0
}

// named is a pasteboard of its own, called name, and the function that
// frees it and what it holds: the tests use one, so that the owner's
// clipboard is never read or changed.
func named(name string) (*Clipboard, func()) {
	p := C.CString(name)
	return &Clipboard{name: p}, func() {
		C.mardpClipRelease(p)
		C.free(unsafe.Pointer(p))
	}
}
