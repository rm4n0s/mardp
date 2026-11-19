//go:build darwin

package aacenc

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AudioToolbox
#include "aacenc_darwin.h"
*/
import "C"

import (
	"unsafe"

	"github.com/rm4n0s/errors"
)

// New makes an Encoder of AudioToolbox's.
func New() (*Encoder, error) { return newAt(Bitrate) }

// newAt makes an Encoder of AudioToolbox's at bitrate bits a second.
func newAt(bitrate int) (*Encoder, error) {
	r := C.mardpAACCreate(C.int(bitrate))
	if r.enc == nil {
		return nil, errors.New(TagEncoderCreateFailed, "AudioToolbox refused the AAC encoder",
			"step", C.GoString(&r.step[0]), "status", int32(r.status))
	}
	cookie := make([]byte, int(r.cookieLen))
	for i := range cookie {
		cookie[i] = byte(r.cookie[i])
	}
	conv := &audioConverter{enc: r.enc, bitrate: int(r.bitrate), maxPacket: int(r.maxPacket), cookie: cookie}
	return newEncoder(conv, int(r.priming)), nil
}

// audioConverter is AudioToolbox's AudioConverter.
type audioConverter struct {
	enc *C.mardpAAC
	// bitrate, maxPacket and cookie are what AudioToolbox states: the
	// bitrate it encodes at, its largest packet and its magic cookie.
	bitrate   int
	maxPacket int
	cookie    []byte
	// status is where the C side writes a failure's status.
	status C.int32_t
}

func (a *audioConverter) encode(left, right []float32, out []byte, sizes []int32) (int, error) {
	n := C.mardpAACEncode(a.enc, (*C.float)(unsafe.Pointer(&left[0])), (*C.float)(unsafe.Pointer(&right[0])),
		C.int(len(left)), (*C.uint8_t)(unsafe.Pointer(&out[0])), C.int(len(out)),
		(*C.int32_t)(unsafe.Pointer(&sizes[0])), C.int(len(sizes)), &a.status)
	if n < 0 {
		return 0, errors.New(TagEncodeFailed, "AudioToolbox failed to encode", "status", int32(a.status))
	}
	return int(n), nil
}

func (a *audioConverter) reset() error {
	if st := C.mardpAACReset(a.enc); st != 0 {
		return errors.New(TagEncodeFailed, "AudioToolbox failed to reset the encoder", "status", int32(st))
	}
	return nil
}

func (a *audioConverter) close() { C.mardpAACClose(a.enc) }
