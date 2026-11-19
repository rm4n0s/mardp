//go:build darwin && mardp_mac

package vtenc

/*
#include "vtenc_darwin.h"

// mardpEncTestFrame fills a pixel buffer of the encoder's pool with a
// picture of seed, a moving gradient, with fresh noise of ±16 on it when
// noise is set, and submits it at ptsNs.
static long mardpEncTestFrame(mardpEnc *e, uint32_t seed, int noise, int64_t ptsNs) {
	CVPixelBufferPoolRef pool = mardpEncPool(e);
	CVPixelBufferRef pb = NULL;
	if (pool == NULL || CVPixelBufferPoolCreatePixelBuffer(NULL, pool, &pb) != kCVReturnSuccess) {
		return -1;
	}
	CVPixelBufferLockBaseAddress(pb, 0);
	uint32_t x = seed * 2654435761u + 1;
	for (size_t plane = 0; plane < 2; plane++) {
		uint8_t *base = CVPixelBufferGetBaseAddressOfPlane(pb, plane);
		size_t stride = CVPixelBufferGetBytesPerRowOfPlane(pb, plane);
		size_t w = CVPixelBufferGetWidthOfPlane(pb, plane) * (plane + 1);
		size_t h = CVPixelBufferGetHeightOfPlane(pb, plane);
		for (size_t row = 0; row < h; row++) {
			uint8_t *line = base + row * stride;
			for (size_t col = 0; col < w; col++) {
				int v = plane == 0 ? (int)((col + row + seed * 8) & 0xff) : 128 + (int)((col / 2 + seed) % 64) - 32;
				if (noise) {
					x ^= x << 13;
					x ^= x >> 17;
					x ^= x << 5;
					v += (int)(x & 31) - 16;
					v = v < 0 ? 0 : v > 255 ? 255 : v;
				}
				line[col] = (uint8_t)v;
			}
		}
	}
	CVPixelBufferUnlockBaseAddress(pb, 0);
	long st = mardpEncSubmit(e, pb, CMTimeMake(ptsNs, 1000000000));
	CVPixelBufferRelease(pb);
	return st;
}
*/
import "C"

import "time"

// encodeTest submits a generated picture at pts, as the capture will: it
// returns the OSStatus, or 1 when the encoder skipped it.
func (e *Encoder) encodeTest(seed uint32, noise bool, pts time.Duration) int {
	return int(C.mardpEncTestFrame(e.s.(*vtSession).enc, C.uint32_t(seed), cBool(noise), C.int64_t(pts)))
}
