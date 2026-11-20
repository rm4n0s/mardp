//go:build darwin

#import <Foundation/Foundation.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <VideoToolbox/VideoToolbox.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "vtdec_darwin.h"

struct mardpDec {
	VTDecompressionSessionRef vt;
	CMVideoFormatDescriptionRef format;
	int width, height;
};

mardpDecCreated mardpDecCreate(const uint8_t *sps, size_t spsLen, const uint8_t *pps, size_t ppsLen) {
	mardpDecCreated r = {0};
	mardpDec *d = calloc(1, sizeof *d);
	if (d == NULL) {
		r.status = -1;
		snprintf(r.step, sizeof r.step, "calloc");
		return r;
	}
	const uint8_t *sets[] = {sps, pps};
	size_t lens[] = {spsLen, ppsLen};
	OSStatus st = CMVideoFormatDescriptionCreateFromH264ParameterSets(NULL, 2, sets, lens, 4, &d->format);
	if (st != noErr) {
		free(d);
		r.status = st;
		snprintf(r.step, sizeof r.step, "CMVideoFormatDescriptionCreateFromH264ParameterSets");
		return r;
	}
	CMVideoDimensions dim = CMVideoFormatDescriptionGetDimensions(d->format);
	d->width = dim.width;
	d->height = dim.height;
	NSDictionary *out = @{
		(__bridge NSString *)kCVPixelBufferPixelFormatTypeKey: @(kCVPixelFormatType_32BGRA),
	};
	st = VTDecompressionSessionCreate(NULL, d->format, NULL, (__bridge CFDictionaryRef)out, NULL, &d->vt);
	if (st != noErr) {
		CFRelease(d->format);
		free(d);
		r.status = st;
		snprintf(r.step, sizeof r.step, "VTDecompressionSessionCreate");
		return r;
	}
	r.dec = d;
	return r;
}

int mardpDecWidth(mardpDec *d) { return d->width; }
int mardpDecHeight(mardpDec *d) { return d->height; }

long mardpDecDecode(mardpDec *d, const uint8_t *avcc, size_t len, uint8_t *out, size_t stride) {
	CMBlockBufferRef bb = NULL;
	OSStatus st = CMBlockBufferCreateWithMemoryBlock(NULL, NULL, len, NULL, NULL, 0, len, kCMBlockBufferAssureMemoryNowFlag, &bb);
	if (st != kCMBlockBufferNoErr) {
		return st;
	}
	st = CMBlockBufferReplaceDataBytes(avcc, bb, 0, len);
	if (st != kCMBlockBufferNoErr) {
		CFRelease(bb);
		return st;
	}
	CMSampleBufferRef sb = NULL;
	size_t size = len;
	st = CMSampleBufferCreateReady(NULL, bb, d->format, 1, 0, NULL, 1, &size, &sb);
	CFRelease(bb);
	if (st != noErr) {
		return st;
	}
	__block long result = -1;
	int width = d->width, height = d->height;
	// With no flags the frame is decoded before the call returns.
	st = VTDecompressionSessionDecodeFrameWithOutputHandler(d->vt, sb, 0, NULL,
		^(OSStatus status, VTDecodeInfoFlags flags, CVImageBufferRef image, CMTime pts, CMTime duration) {
			if (status != noErr || image == NULL) {
				result = status != noErr ? status : -1;
				return;
			}
			CVPixelBufferLockBaseAddress(image, kCVPixelBufferLock_ReadOnly);
			const uint8_t *base = CVPixelBufferGetBaseAddress(image);
			size_t rowBytes = CVPixelBufferGetBytesPerRow(image);
			for (int y = 0; y < height; y++) {
				const uint8_t *src = base + (size_t)y * rowBytes;
				uint8_t *dst = out + (size_t)y * stride;
				for (int x = 0; x < width; x++) {
					dst[4 * x] = src[4 * x + 2];
					dst[4 * x + 1] = src[4 * x + 1];
					dst[4 * x + 2] = src[4 * x];
					dst[4 * x + 3] = 0xff;
				}
			}
			CVPixelBufferUnlockBaseAddress(image, kCVPixelBufferLock_ReadOnly);
			result = 0;
		});
	CFRelease(sb);
	if (st != noErr) {
		return st;
	}
	return result;
}

void mardpDecClose(mardpDec *d) {
	VTDecompressionSessionInvalidate(d->vt);
	CFRelease(d->vt);
	CFRelease(d->format);
	free(d);
}
