package input

/*
#include <stdlib.h>
#include "input_darwin.h"
*/
import "C"

import "unsafe"

// NewPointer makes a Pointer that reads macOS's.
func NewPointer(opts PointerOptions) (*Pointer, error) {
	return newPointer(opts, macCursor{}), nil
}

// macCursor reads the pointer with AppKit and Core Graphics.
type macCursor struct{}

func (macCursor) read(uuid string, buf []byte) (pointerInfo, error) {
	cs := C.CString(uuid)
	defer C.free(unsafe.Pointer(cs))
	r := C.mardpPtrRead(cs, (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	info := pointerInfo{
		visible: r.visible != 0, onDisplay: r.onDisplay != 0, x: float64(r.x), y: float64(r.y),
		width: int(r.width), height: int(r.height), hotX: int(r.hotX), hotY: int(r.hotY),
	}
	return checkRead(int(r.status), info)
}
