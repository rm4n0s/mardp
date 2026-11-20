//go:build darwin

package vtenc

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics -framework CoreMedia -framework CoreText -framework CoreVideo -framework VideoToolbox
#include "vtenc_darwin.h"
*/
import "C"

import (
	"image"
	"runtime/cgo"
	"time"
	"unsafe"
)

// New makes an Encoder whose frames go to frame (see the package
// documentation).
func New(opts Options, frame func(*Frame)) (*Encoder, error) {
	return newEncoder(opts, frame, videoToolbox{})
}

// videoToolbox is the backend of VideoToolbox.
type videoToolbox struct{}

func (videoToolbox) create(cfg config, enc *Encoder) (session, *createFailure) {
	h := cgo.NewHandle(enc)
	r := C.mardpEncCreate(C.uintptr_t(h), C.int(cfg.width), C.int(cfg.height), cBool(cfg.fullRange),
		C.int(cfg.profile), C.int(cfg.bitrate), C.int(cfg.fps))
	if r.enc == nil {
		// mardpEncCreate freed what it made: no callback holds h.
		h.Delete()
		return nil, &createFailure{step: C.GoString(&r.step[0]), status: int(r.status)}
	}
	return &vtSession{enc: r.enc, h: h}, nil
}

func cBool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// vtSession is a running VTCompressionSession.
type vtSession struct {
	enc *C.mardpEnc
	h   cgo.Handle
}

func (s *vtSession) setBitrate(bitsPerSecond int) int {
	return int(C.mardpEncSetBitrate(s.enc, C.int(bitsPerSecond)))
}

func (s *vtSession) forceKeyFrame() { C.mardpEncForceKey(s.enc) }

func (s *vtSession) setSkip(skip bool) { C.mardpEncSetSkip(s.enc, cBool(skip)) }

func (s *vtSession) repeat() int { return int(C.mardpEncRepeat(s.enc)) }

func (s *vtSession) captured() uint32 { return uint32(C.mardpEncCaptured(s.enc)) }

func (s *vtSession) stale() bool { return C.mardpEncStale(s.enc) != 0 }

func (s *vtSession) submitter() (fn, arg unsafe.Pointer) {
	return C.mardpEncSubmit, unsafe.Pointer(s.enc)
}

// SubmitPattern encodes picture n of a moving test pattern, made in C, as
// if a capture had just handed it over (`mardp -test-pattern`, SPEC
// §15.5). It returns VideoToolbox's OSStatus, 1 when the encoder skipped
// the picture, and -1 when it had no buffer for it.
func (e *Encoder) SubmitPattern(n uint32) int {
	return int(C.mardpEncSubmitPattern(e.s.(*vtSession).enc, C.uint32_t(n)))
}

// SubmitText encodes page of the text pattern, as SubmitPattern encodes a
// picture: lines of text as a desktop shows them, each page differing from
// the others in a few letters of every line (`mardp -test-pattern -text`,
// SPEC §15.5).
func (e *Encoder) SubmitText(page uint32) int {
	return int(C.mardpEncSubmitText(e.s.(*vtSession).enc, C.uint32_t(page)))
}

// TextPage is page of the text pattern, width by height: what SubmitText
// gives the encoder, for comparing with what a client decodes. It is nil
// when the page cannot be drawn.
func TextPage(width, height int, page uint32) *image.RGBA {
	if width <= 0 || height <= 0 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if C.mardpTextPage(C.int(width), C.int(height), C.uint32_t(page), (*C.uint8_t)(unsafe.Pointer(&img.Pix[0]))) != 0 {
		return nil
	}
	return img
}

func (s *vtSession) close() {
	C.mardpEncClose(s.enc)
	// No output can reach Go any more.
	s.h.Delete()
}

//export mardpEncOutput
func mardpEncOutput(h C.uintptr_t, status C.int, dropped C.int, key C.int, repeat C.int, sets **C.uint8_t, setLens *C.size_t, nsets C.int,
	nalLen C.int, data *C.uint8_t, n C.size_t, pts C.int64_t, age C.int64_t) {
	e := cgo.Handle(h).Value().(*Encoder)
	o := output{status: int(status), dropped: dropped != 0, key: key != 0, repeat: repeat != 0, nalLen: int(nalLen), pts: time.Duration(pts), age: time.Duration(age)}
	var buf [4][]byte
	if nsets > 0 {
		ps := unsafe.Slice(sets, int(nsets))
		ls := unsafe.Slice(setLens, int(nsets))
		for i := range min(len(buf), int(nsets)) {
			buf[i] = unsafe.Slice((*byte)(unsafe.Pointer(ps[i])), int(ls[i]))
		}
		o.sets = buf[:min(len(buf), int(nsets))]
	}
	if data != nil && n > 0 {
		o.avcc = unsafe.Slice((*byte)(unsafe.Pointer(data)), int(n))
	}
	e.deliver(o)
}
