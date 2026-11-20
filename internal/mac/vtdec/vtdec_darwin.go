//go:build darwin

package vtdec

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreMedia -framework CoreVideo -framework VideoToolbox
#include "vtdec_darwin.h"
*/
import "C"

import (
	"bytes"
	"image"
	"unsafe"

	"github.com/rm4n0s/errors"
)

// Decoder decodes one H.264 stream. It is not safe for use by more than
// one goroutine.
type Decoder struct {
	dec      *C.mardpDec
	sps, pps []byte
}

// New makes a Decoder; its session is made at the first key frame.
func New() *Decoder { return &Decoder{} }

// Decode decodes one access unit in Annex B, as the host sends it (the
// parameter sets in front of a key frame), and returns the picture a
// client shows after it.
func (d *Decoder) Decode(annexB []byte) (*image.RGBA, error) {
	u := parse(annexB)
	if u.sps != nil && u.pps != nil && (!bytes.Equal(u.sps, d.sps) || !bytes.Equal(u.pps, d.pps)) {
		d.Close()
		r := C.mardpDecCreate((*C.uint8_t)(unsafe.Pointer(&u.sps[0])), C.size_t(len(u.sps)),
			(*C.uint8_t)(unsafe.Pointer(&u.pps[0])), C.size_t(len(u.pps)))
		if r.dec == nil {
			return nil, errors.New(TagDecoderCreateFailed, "VideoToolbox refused the decoder", "step", C.GoString(&r.step[0]), "status", int(r.status))
		}
		d.dec = r.dec
		d.sps, d.pps = bytes.Clone(u.sps), bytes.Clone(u.pps)
	}
	if d.dec == nil {
		return nil, errors.New(TagDecoderNoParameters, "a frame before the stream's first key frame", "length", len(annexB))
	}
	if len(u.avcc) == 0 {
		return nil, errors.New(TagDecoderFrameInvalid, "an access unit without a picture", "length", len(annexB))
	}
	w, h := int(C.mardpDecWidth(d.dec)), int(C.mardpDecHeight(d.dec))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	st := C.mardpDecDecode(d.dec, (*C.uint8_t)(unsafe.Pointer(&u.avcc[0])), C.size_t(len(u.avcc)),
		(*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), C.size_t(img.Stride))
	if st != 0 {
		return nil, errors.New(TagDecoderFailed, "VideoToolbox failed to decode a frame", "status", int(st), "length", len(annexB))
	}
	return img, nil
}

// Close ends the decoder's session. It may be called more than once.
func (d *Decoder) Close() {
	if d.dec != nil {
		C.mardpDecClose(d.dec)
		d.dec = nil
	}
	d.sps, d.pps = nil, nil
}
