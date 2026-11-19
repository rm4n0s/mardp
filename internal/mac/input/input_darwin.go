package input

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include <stdlib.h>
#include "input_darwin.h"
*/
import "C"

import (
	"time"
	"unsafe"

	"github.com/rm4n0s/errors"
)

// New makes an Input that posts to macOS.
func New(opts Options) (*Input, error) {
	return newInput(opts, macOS{}, time.Now), nil
}

// macOS posts with Core Graphics.
type macOS struct{}

func (macOS) trusted() bool { return C.mardpInTrusted() != 0 }

func (macOS) origin(uuid string) (x, y float64, ok bool) {
	cs := C.CString(uuid)
	defer C.free(unsafe.Pointer(cs))
	var cx, cy C.double
	if C.mardpInOrigin(cs, &cx, &cy) == 0 {
		return 0, 0, false
	}
	return float64(cx), float64(cy), true
}

func (macOS) post(e *event) error {
	c := C.mardpInEvent{
		kind:   C.int(e.kind),
		flags:  C.uint64_t(e.flags),
		key:    C.uint16_t(e.key),
		mouse:  C.uint32_t(e.mouse),
		button: C.uint32_t(e.button),
		x:      C.double(e.x),
		y:      C.double(e.y),
		clicks: C.int(e.clicks),
		dy:     C.int32_t(e.dy),
		dx:     C.int32_t(e.dx),
		text:   [2]C.uint16_t{C.uint16_t(e.text[0]), C.uint16_t(e.text[1])},
		n:      C.int(e.n),
	}
	if e.down {
		c.down = 1
	}
	if e.repeat {
		c.repeat = 1
	}
	if C.mardpInPost(&c) == 0 {
		return errors.New(TagInputEventFailed, "Core Graphics did not make the event", "kind", int(e.kind))
	}
	return nil
}
