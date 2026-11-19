//go:build darwin

#import <Foundation/Foundation.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <VideoToolbox/VideoToolbox.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>

#include "vtenc_darwin.h"
#include "_cgo_export.h"

struct mardpEnc {
	VTCompressionSessionRef vt;
	uintptr_t handle;
	atomic_int skip, forceKey;
};

// The most parameter sets passed on with a key frame (H.264 has an SPS
// and a PPS; VideoToolbox writes one of each).
enum { maxSets = 4 };

// encoded is VideoToolbox's output callback: it hands each frame to Go in
// one call.
static void encoded(void *ref, void *frameRef, OSStatus st, VTEncodeInfoFlags flags, CMSampleBufferRef sb) {
	mardpEnc *e = ref;
	if (st != noErr || sb == NULL) {
		mardpEncOutput(e->handle, st != noErr ? st : -1, (flags & kVTEncodeInfo_FrameDropped) != 0, 0, NULL, NULL, 0, 4, NULL, 0, 0);
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
		mardpEncOutput(e->handle, fs, 0, 0, NULL, NULL, 0, 4, NULL, 0, 0);
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
			mardpEncOutput(e->handle, fs, 0, 0, NULL, NULL, 0, 4, NULL, 0, 0);
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
			mardpEncOutput(e->handle, fs, 0, 0, NULL, NULL, 0, 4, NULL, 0, 0);
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
			mardpEncOutput(e->handle, fs, 0, 0, NULL, NULL, 0, 4, NULL, 0, 0);
			return;
		}
	}
	CMTime pts = CMSampleBufferGetPresentationTimeStamp(sb);
	int64_t ns = 0;
	if (CMTIME_IS_NUMERIC(pts)) {
		ns = CMTimeConvertScale(pts, 1000000000, kCMTimeRoundingMethod_Default).value;
	}
	mardpEncOutput(e->handle, 0, 0, key, sets, setLens, nsets, nalLen, (uint8_t *)data, len, ns);
	if (flat != NULL) CFRelease(flat);
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

long mardpEncSubmit(mardpEnc *e, CVPixelBufferRef pb, CMTime pts) {
	if (atomic_load(&e->skip)) {
		return 1;
	}
	CFDictionaryRef opts = NULL;
	if (atomic_exchange(&e->forceKey, 0)) {
		const void *k[] = {kVTEncodeFrameOptionKey_ForceKeyFrame};
		const void *v[] = {kCFBooleanTrue};
		opts = CFDictionaryCreate(NULL, k, v, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	}
	OSStatus st = VTCompressionSessionEncodeFrame(e->vt, pb, pts, kCMTimeInvalid, opts, NULL, NULL);
	if (opts != NULL) {
		CFRelease(opts);
		if (st != noErr) {
			atomic_store(&e->forceKey, 1);
		}
	}
	return st;
}

CVPixelBufferPoolRef mardpEncPool(mardpEnc *e) { return VTCompressionSessionGetPixelBufferPool(e->vt); }

void mardpEncClose(mardpEnc *e) {
	VTCompressionSessionCompleteFrames(e->vt, kCMTimeInvalid);
	VTCompressionSessionInvalidate(e->vt);
	CFRelease(e->vt);
	free(e);
}
