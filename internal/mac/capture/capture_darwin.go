//go:build darwin

package capture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework CoreMedia -framework CoreVideo -framework ScreenCaptureKit
#include <stdlib.h>
#include "capture_darwin.h"
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"

	"github.com/rm4n0s/mardp/internal/host"
)

// New returns a Source of the display in opts.
func New(opts Options) (*Source, error) {
	return newSource(opts, macOS{}), nil
}

// macOS is the backend of ScreenCaptureKit and Core Graphics.
type macOS struct{}

func (macOS) granted() bool { return C.mardpCapGranted() == 1 }

func (macOS) find(uuid string) (rawDisplay, bool) {
	cu := C.CString(uuid)
	defer C.free(unsafe.Pointer(cu))
	var d C.mardpCapDisplay
	if C.mardpCapFind(cu, &d) == 0 {
		return rawDisplay{}, false
	}
	name := C.GoString(&d.name[0])
	if name == "" {
		name = "Display"
	}
	return rawDisplay{id: uint32(d.id), info: host.Display{
		ID:          C.GoString(&d.uuid[0]),
		Name:        name,
		Width:       int(d.pixelWidth),
		Height:      int(d.pixelHeight),
		PointWidth:  int(d.pointWidth),
		PointHeight: int(d.pointHeight),
		Main:        d.main == 1,
	}}, true
}

func (macOS) alive(id uint32, uuid string) bool {
	cu := C.CString(uuid)
	defer C.free(unsafe.Pointer(cu))
	return C.mardpCapAlive(C.uint32_t(id), cu) == 1
}

func (macOS) start(id uint32, cfg host.ScreenConfig, sk *sink, mode streamMode) (stream, *startFailure) {
	h := cgo.NewHandle(sk)
	var video, full C.int
	if mode.video {
		video = 1
	}
	if mode.fullRange {
		full = 1
	}
	r := C.mardpCapStart(C.uintptr_t(h), C.uint32_t(id), C.int(cfg.Width), C.int(cfg.Height), C.int(cfg.MaxFPS),
		video, full, mode.fn, mode.arg, C.uint32_t(mode.window))
	if r.code != 0 {
		// mardpCapStart tore down what it made: no callback holds h.
		h.Delete()
		return nil, &startFailure{code: int(r.code), message: C.GoString(&r.message[0])}
	}
	return &macStream{st: r.stream, out: r.output, h: h}, nil
}

// macStream is a running SCStream.
type macStream struct {
	st, out unsafe.Pointer
	h       cgo.Handle
}

func (m *macStream) stop() VideoStats {
	var c C.mardpCapCounts
	C.mardpCapStop(m.st, m.out, &c)
	// 4. No callback can reach Go any more.
	m.h.Delete()
	return VideoStats{Encoded: int64(c.encoded), Skipped: int64(c.skipped), Refused: int64(c.refused), Crossings: int64(c.crossings)}
}

//export mardpCapFrame
func mardpCapFrame(h C.uintptr_t, base unsafe.Pointer, stride, width, height C.int, rects *C.int32_t, n C.int) {
	sk := cgo.Handle(h).Value().(*sink)
	if base == nil || height <= 0 {
		return
	}
	pix := unsafe.Slice((*byte)(base), int(stride)*int(height))
	var rs []int32
	if n >= 0 {
		rs = unsafe.Slice((*int32)(unsafe.Pointer(rects)), 4*int(n))
		if rs == nil {
			rs = []int32{}
		}
	}
	sk.deliver(pix, int(stride), int(width), int(height), rs)
}

//export mardpCapStopped
func mardpCapStopped(h C.uintptr_t, code C.long, message *C.char) {
	cgo.Handle(h).Value().(*sink).stop(int(code), C.GoString(message))
}
