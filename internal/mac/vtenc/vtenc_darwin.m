//go:build darwin

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreText/CoreText.h>
#import <CoreVideo/CoreVideo.h>
#import <VideoToolbox/VideoToolbox.h>
#include <os/lock.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "vtenc_darwin.h"
#include "_cgo_export.h"

struct mardpEnc {
	VTCompressionSessionRef vt;
	uintptr_t handle;
	atomic_int skip, forceKey;
	// lock orders the submissions and guards last and lastNs: the newest
	// picture handed over, kept for mardpEncRepeat, and the time of the
	// last frame given to VideoToolbox.
	os_unfair_lock lock;
	CVPixelBufferRef last;
	int64_t lastNs;
	// captured counts the pictures handed over; stale says the newest was
	// not encoded.
	atomic_uint captured;
	atomic_int stale;
};

// The most parameter sets passed on with a key frame (H.264 has an SPS
// and a PPS; VideoToolbox writes one of each).
enum { maxSets = 4 };

// encoded is VideoToolbox's output callback: it hands each frame to Go in
// one call.
static void encoded(void *ref, void *frameRef, OSStatus st, VTEncodeInfoFlags flags, CMSampleBufferRef sb) {
	mardpEnc *e = ref;
	// The frame's own reference says it is a repeat of the newest picture.
	int repeat = frameRef != NULL;
	if (st != noErr || sb == NULL) {
		mardpEncOutput(e->handle, st != noErr ? st : -1, (flags & kVTEncodeInfo_FrameDropped) != 0, 0, repeat, NULL, NULL, 0, 4, NULL, 0, 0, -1);
		return;
	}
	int key = 1;
	CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (atts != NULL && CFArrayGetCount(atts) > 0) {
		CFDictionaryRef a = CFArrayGetValueAtIndex(atts, 0);
		CFBooleanRef notSync = CFDictionaryGetValue(a, kCMSampleAttachmentKey_NotSync);
		key = !(notSync != NULL && CFBooleanGetValue(notSync));
	}
	CMFormatDescriptionRef fd = CMSampleBufferGetFormatDescription(sb);
	size_t count = 0;
	int nalLen = 4;
	OSStatus fs = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fd, 0, NULL, NULL, &count, &nalLen);
	if (fs != noErr) {
		mardpEncOutput(e->handle, fs, 0, 0, repeat, NULL, NULL, 0, 4, NULL, 0, 0, -1);
		return;
	}
	uint8_t *sets[maxSets];
	size_t setLens[maxSets];
	int nsets = 0;
	for (size_t i = 0; key && i < count && nsets < maxSets; i++) {
		const uint8_t *ps = NULL;
		size_t psz = 0;
		fs = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fd, i, &ps, &psz, NULL, NULL);
		if (fs != noErr) {
			mardpEncOutput(e->handle, fs, 0, 0, repeat, NULL, NULL, 0, 4, NULL, 0, 0, -1);
			return;
		}
		sets[nsets] = (uint8_t *)ps;
		setLens[nsets] = psz;
		nsets++;
	}
	CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sb);
	CMBlockBufferRef flat = NULL;
	size_t total = bb != NULL ? CMBlockBufferGetDataLength(bb) : 0;
	if (bb != NULL && !CMBlockBufferIsRangeContiguous(bb, 0, total)) {
		fs = CMBlockBufferCreateContiguous(NULL, bb, NULL, NULL, 0, total, 0, &flat);
		if (fs != kCMBlockBufferNoErr) {
			mardpEncOutput(e->handle, fs, 0, 0, repeat, NULL, NULL, 0, 4, NULL, 0, 0, -1);
			return;
		}
		bb = flat;
	}
	char *data = NULL;
	size_t len = 0;
	if (bb != NULL && total > 0) {
		fs = CMBlockBufferGetDataPointer(bb, 0, &len, NULL, &data);
		if (fs != kCMBlockBufferNoErr) {
			if (flat != NULL) CFRelease(flat);
			mardpEncOutput(e->handle, fs, 0, 0, repeat, NULL, NULL, 0, 4, NULL, 0, 0, -1);
			return;
		}
	}
	CMTime pts = CMSampleBufferGetPresentationTimeStamp(sb);
	int64_t ns = 0, age = -1;
	if (CMTIME_IS_NUMERIC(pts)) {
		ns = CMTimeConvertScale(pts, 1000000000, kCMTimeRoundingMethod_Default).value;
		// A capture stamps its frames with the host clock: the frame's age
		// is how long ago it was captured (SPEC §14).
		age = mardpHostNanos() - ns;
	}
	mardpEncOutput(e->handle, 0, 0, key, repeat, sets, setLens, nsets, nalLen, (uint8_t *)data, len, ns, age);
	if (flat != NULL) CFRelease(flat);
}

int64_t mardpHostNanos(void) {
	return CMTimeConvertScale(CMClockGetTime(CMClockGetHostTimeClock()), 1000000000, kCMTimeRoundingMethod_Default).value;
}

// patternNoise is the side of the noisy square of the test pattern, a
// fraction of the picture's.
enum { patternNoise = 3 };

long mardpEncSubmitPattern(mardpEnc *e, uint32_t n) {
	CVPixelBufferPoolRef pool = mardpEncPool(e);
	CVPixelBufferRef pb = NULL;
	if (pool == NULL || CVPixelBufferPoolCreatePixelBuffer(NULL, pool, &pb) != kCVReturnSuccess) {
		return -1;
	}
	CVPixelBufferLockBaseAddress(pb, 0);
	size_t w = CVPixelBufferGetWidthOfPlane(pb, 0), h = CVPixelBufferGetHeightOfPlane(pb, 0);
	uint8_t *luma = CVPixelBufferGetBaseAddressOfPlane(pb, 0);
	size_t ls = CVPixelBufferGetBytesPerRowOfPlane(pb, 0);
	// Bands that move down 4 rows a frame.
	for (size_t row = 0; row < h; row++) {
		memset(luma + row * ls, (int)((row + n * 4) & 0xff), w);
	}
	// A square that moves across, a diagonal gradient with fresh noise of
	// ±8 on it each frame, as a film's grain.
	size_t sw = w / patternNoise, sh = h / patternNoise;
	size_t x0 = (n * 8) % (w - sw + 1), y0 = h / 3;
	uint32_t x = n * 2654435761u + 1;
	for (size_t row = 0; row < sh; row++) {
		uint8_t *line = luma + (y0 + row) * ls + x0;
		for (size_t col = 0; col < sw; col++) {
			x ^= x << 13;
			x ^= x >> 17;
			x ^= x << 5;
			int v = 64 + (int)((col + row) / 4 % 128) + (int)(x & 15) - 8;
			line[col] = (uint8_t)v;
		}
	}
	uint8_t *chroma = CVPixelBufferGetBaseAddressOfPlane(pb, 1);
	size_t cs = CVPixelBufferGetBytesPerRowOfPlane(pb, 1);
	for (size_t row = 0; row < CVPixelBufferGetHeightOfPlane(pb, 1); row++) {
		memset(chroma + row * cs, 128 + (int)((row + n) % 64) - 32, w);
	}
	CVPixelBufferUnlockBaseAddress(pb, 0);
	// Stamped once drawn: a capture hands over a picture already made.
	long st = mardpEncSubmit(e, pb, CMTimeMake(mardpHostNanos(), 1000000000));
	CVPixelBufferRelease(pb);
	return st;
}

// drawText draws page of the text pattern, width by height, and returns
// its bitmap, RGBX with the first row on top: lines of text as a desktop
// shows them, in a 13 point font, a line every 18 pixels, light on dark
// (dark on white for page 0), with the page's number in every line, so
// that each page differs from the others in a few letters of each line, as
// text that is typed or scrolled.
static CGContextRef drawText(size_t w, size_t h, uint32_t page) {
	CGColorSpaceRef space = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
	CGContextRef cg = CGBitmapContextCreate(NULL, w, h, 8, w * 4, space, (CGBitmapInfo)kCGImageAlphaNoneSkipLast);
	CGColorSpaceRelease(space);
	if (cg == NULL) {
		return NULL;
	}
	CGFloat bg = page == 0 ? 1.0 : 0.12, fg = page == 0 ? 0.0 : 0.9;
	CGContextSetRGBFillColor(cg, bg, bg, bg, 1);
	CGContextFillRect(cg, CGRectMake(0, 0, w, h));
	CTFontRef font = CTFontCreateWithName(CFSTR("Menlo"), 13, NULL);
	CGColorRef color = CGColorCreateGenericRGB(fg, fg, fg, 1);
	const void *keys[] = {kCTFontAttributeName, kCTForegroundColorAttributeName};
	const void *vals[] = {font, color};
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, vals, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	unsigned line = 0;
	for (CGFloat y = (CGFloat)h - 20; y > 4; y -= 18, line++) {
		CFStringRef text = CFStringCreateWithFormat(NULL, NULL,
			CFSTR("%3u  page %u: the quick brown fox jumps over the lazy dog, 0123456789 times; if (x[%u] != y) { return errors.New(\"Tag%u\", \"what a client must be able to read\") }  ~/mardp/internal/host/avc.go:%u"),
			line, page, line * 7 + page, line + page * 100, line * 13 + page);
		CFAttributedStringRef as = CFAttributedStringCreate(NULL, text, attrs);
		CTLineRef ct = CTLineCreateWithAttributedString(as);
		CGContextSetTextPosition(cg, 12 + (line % 4) * 8, y);
		CTLineDraw(ct, cg);
		CFRelease(ct);
		CFRelease(as);
		CFRelease(text);
	}
	CFRelease(attrs);
	CGColorRelease(color);
	CFRelease(font);
	return cg;
}

int mardpTextPage(int width, int height, uint32_t page, uint8_t *out) {
	CGContextRef cg = drawText((size_t)width, (size_t)height, page);
	if (cg == NULL) {
		return -1;
	}
	const uint8_t *rgba = CGBitmapContextGetData(cg);
	size_t stride = CGBitmapContextGetBytesPerRow(cg);
	for (int row = 0; row < height; row++) {
		memcpy(out + (size_t)row * width * 4, rgba + (size_t)row * stride, (size_t)width * 4);
	}
	CGContextRelease(cg);
	return 0;
}

long mardpEncSubmitText(mardpEnc *e, uint32_t page) {
	CVPixelBufferPoolRef pool = mardpEncPool(e);
	CVPixelBufferRef pb = NULL;
	if (pool == NULL || CVPixelBufferPoolCreatePixelBuffer(NULL, pool, &pb) != kCVReturnSuccess) {
		return -1;
	}
	size_t w = CVPixelBufferGetWidthOfPlane(pb, 0), h = CVPixelBufferGetHeightOfPlane(pb, 0);
	CGContextRef cg = drawText(w, h, page);
	if (cg == NULL) {
		CVPixelBufferRelease(pb);
		return -1;
	}
	const uint8_t *rgba = CGBitmapContextGetData(cg);
	size_t stride = CGBitmapContextGetBytesPerRow(cg);
	// To NV12, full range, BT.709, the chroma the mean of each 2 by 2.
	CVPixelBufferLockBaseAddress(pb, 0);
	uint8_t *luma = CVPixelBufferGetBaseAddressOfPlane(pb, 0), *chroma = CVPixelBufferGetBaseAddressOfPlane(pb, 1);
	size_t ls = CVPixelBufferGetBytesPerRowOfPlane(pb, 0), cs = CVPixelBufferGetBytesPerRowOfPlane(pb, 1);
	for (size_t row = 0; row < h; row++) {
		const uint8_t *src = rgba + row * stride;
		for (size_t col = 0; col < w; col++) {
			double r = src[4 * col], g = src[4 * col + 1], b = src[4 * col + 2];
			luma[row * ls + col] = (uint8_t)(0.2126 * r + 0.7152 * g + 0.0722 * b + 0.5);
		}
	}
	for (size_t row = 0; row + 1 < h; row += 2) {
		for (size_t col = 0; col + 1 < w; col += 2) {
			double r = 0, g = 0, b = 0;
			for (int dy = 0; dy < 2; dy++) {
				for (int dx = 0; dx < 2; dx++) {
					const uint8_t *p = rgba + (row + dy) * stride + 4 * (col + dx);
					r += p[0];
					g += p[1];
					b += p[2];
				}
			}
			r /= 4, g /= 4, b /= 4;
			double y = 0.2126 * r + 0.7152 * g + 0.0722 * b;
			chroma[row / 2 * cs + col] = (uint8_t)((b - y) / 1.8556 + 128.5);
			chroma[row / 2 * cs + col + 1] = (uint8_t)((r - y) / 1.5748 + 128.5);
		}
	}
	CVPixelBufferUnlockBaseAddress(pb, 0);
	CGContextRelease(cg);
	long st = mardpEncSubmit(e, pb, CMTimeMake(mardpHostNanos(), 1000000000));
	CVPixelBufferRelease(pb);
	return st;
}

mardpEncCreated mardpEncCreate(uintptr_t handle, int width, int height, int fullRange, int profile, int bitrate, int fps) {
	mardpEncCreated r = {0};
	@autoreleasepool {
		mardpEnc *e = calloc(1, sizeof *e);
		if (e == NULL) {
			r.status = -1;
			snprintf(r.step, sizeof r.step, "calloc");
			return r;
		}
		e->handle = handle;
		e->lock = OS_UNFAIR_LOCK_INIT;
		NSDictionary *spec = @{
			(__bridge NSString *)kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder: @YES,
			(__bridge NSString *)kVTVideoEncoderSpecification_EnableLowLatencyRateControl: @YES,
		};
		NSDictionary *src = @{
			(__bridge NSString *)kCVPixelBufferPixelFormatTypeKey:
				@(fullRange ? kCVPixelFormatType_420YpCbCr8BiPlanarFullRange : kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange),
			(__bridge NSString *)kCVPixelBufferWidthKey: @(width),
			(__bridge NSString *)kCVPixelBufferHeightKey: @(height),
			(__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey: @{},
		};
		OSStatus st = VTCompressionSessionCreate(NULL, width, height, kCMVideoCodecType_H264, (__bridge CFDictionaryRef)spec,
			(__bridge CFDictionaryRef)src, NULL, encoded, e, &e->vt);
		if (st != noErr) {
			free(e);
			r.status = st;
			snprintf(r.step, sizeof r.step, "VTCompressionSessionCreate");
			return r;
		}
		CFStringRef profiles[] = {kVTProfileLevel_H264_ConstrainedHigh_AutoLevel, kVTProfileLevel_H264_Main_AutoLevel,
			kVTProfileLevel_H264_ConstrainedBaseline_AutoLevel};
		struct {
			CFStringRef key;
			CFTypeRef value;
			const char *name;
		} props[] = {
			{kVTCompressionPropertyKey_RealTime, kCFBooleanTrue, "RealTime"},
			{kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse, "AllowFrameReordering"},
			{kVTCompressionPropertyKey_ProfileLevel, profiles[profile], "ProfileLevel"},
			{kVTCompressionPropertyKey_AverageBitRate, (__bridge CFTypeRef)@(bitrate), "AverageBitRate"},
			{kVTCompressionPropertyKey_ExpectedFrameRate, (__bridge CFTypeRef)@(fps), "ExpectedFrameRate"},
			{kVTCompressionPropertyKey_MaxKeyFrameInterval, (__bridge CFTypeRef)@(INT32_MAX), "MaxKeyFrameInterval"},
			{kVTCompressionPropertyKey_ColorPrimaries, kCVImageBufferColorPrimaries_ITU_R_709_2, "ColorPrimaries"},
			{kVTCompressionPropertyKey_TransferFunction, kCVImageBufferTransferFunction_ITU_R_709_2, "TransferFunction"},
			{kVTCompressionPropertyKey_YCbCrMatrix, kCVImageBufferYCbCrMatrix_ITU_R_709_2, "YCbCrMatrix"},
		};
		for (size_t i = 0; i < sizeof props / sizeof props[0]; i++) {
			st = VTSessionSetProperty(e->vt, props[i].key, props[i].value);
			if (st != noErr) {
				VTCompressionSessionInvalidate(e->vt);
				CFRelease(e->vt);
				free(e);
				r.status = st;
				snprintf(r.step, sizeof r.step, "%s", props[i].name);
				return r;
			}
		}
		st = VTCompressionSessionPrepareToEncodeFrames(e->vt);
		if (st != noErr) {
			VTCompressionSessionInvalidate(e->vt);
			CFRelease(e->vt);
			free(e);
			r.status = st;
			snprintf(r.step, sizeof r.step, "PrepareToEncodeFrames");
			return r;
		}
		r.enc = e;
	}
	return r;
}

long mardpEncSetBitrate(mardpEnc *e, int bitrate) {
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberIntType, &bitrate);
	OSStatus st = VTSessionSetProperty(e->vt, kVTCompressionPropertyKey_AverageBitRate, n);
	CFRelease(n);
	return st;
}

void mardpEncForceKey(mardpEnc *e) { atomic_store(&e->forceKey, 1); }

void mardpEncSetSkip(mardpEnc *e, int skip) { atomic_store(&e->skip, skip); }

// encode gives pb to VideoToolbox at pts, moved just after the last
// frame's time when it is not later: a repeat is stamped with the clock,
// and the capture's next picture may have been taken a moment before. The
// caller holds e->lock.
static long encode(mardpEnc *e, CVPixelBufferRef pb, CMTime pts, int repeat) {
	int64_t ns = CMTIME_IS_NUMERIC(pts) ? CMTimeConvertScale(pts, 1000000000, kCMTimeRoundingMethod_Default).value : e->lastNs + 1;
	if (e->lastNs != 0 && ns <= e->lastNs) {
		ns = e->lastNs + 1;
		pts = CMTimeMake(ns, 1000000000);
	}
	CFDictionaryRef opts = NULL;
	if (atomic_exchange(&e->forceKey, 0)) {
		const void *k[] = {kVTEncodeFrameOptionKey_ForceKeyFrame};
		const void *v[] = {kCFBooleanTrue};
		opts = CFDictionaryCreate(NULL, k, v, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	}
	OSStatus st = VTCompressionSessionEncodeFrame(e->vt, pb, pts, kCMTimeInvalid, opts, repeat ? (void *)1 : NULL, NULL);
	if (opts != NULL) {
		CFRelease(opts);
		if (st != noErr) {
			atomic_store(&e->forceKey, 1);
		}
	}
	if (st == noErr) {
		e->lastNs = ns;
		atomic_store(&e->stale, 0);
	}
	return st;
}

long mardpEncSubmit(mardpEnc *e, CVPixelBufferRef pb, CMTime pts) {
	os_unfair_lock_lock(&e->lock);
	if (e->last != pb) {
		CVPixelBufferRetain(pb);
		if (e->last != NULL) {
			CVPixelBufferRelease(e->last);
		}
		e->last = pb;
	}
	atomic_fetch_add(&e->captured, 1);
	long st = 1;
	if (atomic_load(&e->skip)) {
		atomic_store(&e->stale, 1);
	} else {
		st = encode(e, pb, pts, 0);
	}
	os_unfair_lock_unlock(&e->lock);
	return st;
}

long mardpEncRepeat(mardpEnc *e) {
	os_unfair_lock_lock(&e->lock);
	long st;
	if (e->last == NULL) {
		st = 2;
	} else if (atomic_load(&e->skip)) {
		st = 1;
	} else {
		st = encode(e, e->last, CMTimeMake(mardpHostNanos(), 1000000000), 1);
	}
	os_unfair_lock_unlock(&e->lock);
	return st;
}

unsigned mardpEncCaptured(mardpEnc *e) { return atomic_load(&e->captured); }

int mardpEncStale(mardpEnc *e) { return atomic_load(&e->stale); }

CVPixelBufferPoolRef mardpEncPool(mardpEnc *e) { return VTCompressionSessionGetPixelBufferPool(e->vt); }

void mardpEncClose(mardpEnc *e) {
	VTCompressionSessionCompleteFrames(e->vt, kCMTimeInvalid);
	VTCompressionSessionInvalidate(e->vt);
	CFRelease(e->vt);
	if (e->last != NULL) {
		CVPixelBufferRelease(e->last);
	}
	free(e);
}
