//go:build darwin && mardp_freerdp

package vtenc

/*
#include <string.h>
#include "vtenc_darwin.h"

// mardpEncSubmitNV12 copies an NV12 picture (y: width×height, uv:
// width×height/2, rows packed) into a pixel buffer of the encoder's pool
// and submits it at ptsNs.
static long mardpEncSubmitNV12(mardpEnc *e, const uint8_t *y, const uint8_t *uv, int width, int height, int64_t ptsNs) {
	CVPixelBufferPoolRef pool = mardpEncPool(e);
	CVPixelBufferRef pb = NULL;
	if (pool == NULL || CVPixelBufferPoolCreatePixelBuffer(NULL, pool, &pb) != kCVReturnSuccess) {
		return -1;
	}
	CVPixelBufferLockBaseAddress(pb, 0);
	const uint8_t *src[2] = {y, uv};
	for (size_t plane = 0; plane < 2; plane++) {
		uint8_t *base = CVPixelBufferGetBaseAddressOfPlane(pb, plane);
		size_t stride = CVPixelBufferGetBytesPerRowOfPlane(pb, plane);
		size_t rows = plane == 0 ? (size_t)height : (size_t)height / 2;
		for (size_t row = 0; row < rows; row++) {
			memcpy(base + row * stride, src[plane] + row * (size_t)width, (size_t)width);
		}
	}
	CVPixelBufferUnlockBaseAddress(pb, 0);
	long st = mardpEncSubmit(e, pb, CMTimeMake(ptsNs, 1000000000));
	CVPixelBufferRelease(pb);
	return st;
}
*/
import "C"

import (
	"time"
	"unsafe"
)

// SubmitNV12 encodes a picture made in Go, for the tests against a real
// client (build tag mardp_freerdp): y is the luma, Width×Height bytes, and
// uv the interleaved chroma, Width×Height/2 bytes, of the encoder's size
// and range. It returns VideoToolbox's OSStatus, 1 when the encoder skipped
// the picture, and -1 for a picture of another size.
func (e *Encoder) SubmitNV12(y, uv []byte, pts time.Duration) int {
	w, h := e.cfg.width, e.cfg.height
	if len(y) != w*h || len(uv) != w*(h/2) || h%2 != 0 {
		return -1
	}
	return int(C.mardpEncSubmitNV12(e.s.(*vtSession).enc, (*C.uint8_t)(unsafe.Pointer(&y[0])), (*C.uint8_t)(unsafe.Pointer(&uv[0])),
		C.int(w), C.int(h), C.int64_t(pts)))
}
