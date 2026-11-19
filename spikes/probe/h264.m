//go:build darwin

#import <AppKit/AppKit.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <VideoToolbox/VideoToolbox.h>
#import <IOKit/pwr_mgt/IOPMLib.h>
#include <stdatomic.h>
#include "h264.h"

static double hnow(void) { return CFAbsoluteTimeGetCurrent(); }

#define MAXF 20000

@interface H264Run : NSObject <SCStreamOutput, SCStreamDelegate>
@property VTCompressionSessionRef session;
@property FILE *out;
@property atomic_int submitted;
@property (atomic, copy) NSString *stopError;
@property (atomic) int firstSPS;
@end

static double gTimes[MAXF];
static atomic_int gTimeCount, gEncoded, gKeyframes, gFailed, gDropped;
static atomic_llong gBytes;

static void writeAnnexB(H264Run *run, CMSampleBufferRef sb, int key) {
	static const uint8_t sc[4] = {0, 0, 0, 1};
	if (key) {
		CMFormatDescriptionRef fd = CMSampleBufferGetFormatDescription(sb);
		size_t count = 0;
		int nalLen = 0;
		CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fd, 0, NULL, NULL, &count, &nalLen);
		for (size_t i = 0; i < count; i++) {
			const uint8_t *ps = NULL;
			size_t psz = 0;
			if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fd, i, &ps, &psz, NULL, NULL) != noErr) continue;
			if (!run.firstSPS && psz >= 4 && (ps[0] & 0x1f) == 7) {
				run.firstSPS = 1;
				printf("SPS: profile_idc %u, constraint flags 0x%02x, level_idc %u, %zu bytes, NAL length size %d\n", ps[1], ps[2], ps[3], psz, nalLen);
			}
			if (run.out) { fwrite(sc, 1, 4, run.out); fwrite(ps, 1, psz, run.out); }
			atomic_fetch_add(&gBytes, (long long)psz + 4);
		}
	}
	CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sb);
	size_t len = 0;
	char *data = NULL;
	if (CMBlockBufferGetDataPointer(bb, 0, NULL, &len, &data) != kCMBlockBufferNoErr) return;
	size_t off = 0;
	while (off + 4 <= len) {
		uint32_t n = ((uint8_t)data[off] << 24) | ((uint8_t)data[off + 1] << 16) | ((uint8_t)data[off + 2] << 8) | (uint8_t)data[off + 3];
		off += 4;
		if (off + n > len) break;
		if (run.out) { fwrite(sc, 1, 4, run.out); fwrite(data + off, 1, n, run.out); }
		off += n;
	}
	atomic_fetch_add(&gBytes, (long long)len);
}

static void encoded(void *ref, void *frameRef, OSStatus st, VTEncodeInfoFlags flags, CMSampleBufferRef sb) {
	H264Run *run = (__bridge H264Run *)ref;
	double *t0 = frameRef;
	double dt = hnow() - *t0;
	free(t0);
	if (st != noErr || !sb) {
		if (flags & kVTEncodeInfo_FrameDropped) atomic_fetch_add(&gDropped, 1);
		else atomic_fetch_add(&gFailed, 1);
		return;
	}
	int i = atomic_fetch_add(&gTimeCount, 1);
	if (i < MAXF) gTimes[i] = dt;
	atomic_fetch_add(&gEncoded, 1);
	int key = 1;
	CFArrayRef arr = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (arr && CFArrayGetCount(arr) > 0) {
		CFDictionaryRef a = CFArrayGetValueAtIndex(arr, 0);
		CFBooleanRef notSync = CFDictionaryGetValue(a, kCMSampleAttachmentKey_NotSync);
		key = !(notSync && CFBooleanGetValue(notSync));
	}
	if (key) atomic_fetch_add(&gKeyframes, 1);
	writeAnnexB(run, sb, key);
}

@implementation H264Run
- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)type {
	if (type != SCStreamOutputTypeScreen) return;
	CFArrayRef arr = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (!arr || CFArrayGetCount(arr) == 0) return;
	NSDictionary *att = (__bridge NSDictionary *)CFArrayGetValueAtIndex(arr, 0);
	if ([att[SCStreamFrameInfoStatus] integerValue] != SCFrameStatusComplete) return;
	CVImageBufferRef img = CMSampleBufferGetImageBuffer(sb);
	if (!img) return;
	int n = atomic_fetch_add(&_submitted, 1);
	double *t0 = malloc(sizeof(double));
	*t0 = hnow();
	NSDictionary *props = n == 0 ? @{(__bridge NSString *)kVTEncodeFrameOptionKey_ForceKeyFrame: @YES} : nil;
	OSStatus st = VTCompressionSessionEncodeFrame(self.session, img, CMSampleBufferGetPresentationTimeStamp(sb), kCMTimeInvalid,
		(__bridge CFDictionaryRef)props, t0, NULL);
	if (st != noErr) { free(t0); atomic_fetch_add(&gFailed, 1); }
}
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	self.stopError = error.description;
}
@end

static int cmpd(const void *a, const void *b) {
	double x = *(const double *)a, y = *(const double *)b;
	return x < y ? -1 : x > y;
}

int probeH264(h264Opts o) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		IOPMAssertionID wake = 0, keep = 0;
		IOPMAssertionDeclareUserActivity(CFSTR("mardp probe"), kIOPMUserActiveLocal, &wake);
		IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleDisplaySleep, kIOPMAssertionLevelOn, CFSTR("mardp probe"), &keep);
		CFRunLoopRunInMode(kCFRunLoopDefaultMode, 1.0, false);
		CGDirectDisplayID mainID = CGMainDisplayID();

		__block SCShareableContent *content = nil;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) { content = c; dispatch_semaphore_signal(sem); }];
		while (dispatch_semaphore_wait(sem, DISPATCH_TIME_NOW)) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, false);
		SCDisplay *disp = nil;
		for (SCDisplay *d in content.displays) if (d.displayID == mainID) disp = d;
		if (!disp) { printf("main display not found\n"); return 2; }

		H264Run *run = [[H264Run alloc] init];
		if (o.outPath && o.outPath[0]) {
			run.out = fopen(o.outPath, "wb");
			if (!run.out) { printf("cannot open %s\n", o.outPath); return 3; }
		}

		NSMutableDictionary *spec = [NSMutableDictionary dictionaryWithDictionary:@{
			(__bridge NSString *)kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder: @YES}];
		if (o.lowLatency) spec[(__bridge NSString *)kVTVideoEncoderSpecification_EnableLowLatencyRateControl] = @YES;
		VTCompressionSessionRef vt = NULL;
		OSStatus st = VTCompressionSessionCreate(NULL, o.width, o.height, kCMVideoCodecType_H264, (__bridge CFDictionaryRef)spec, NULL, NULL,
			encoded, (__bridge void *)run, &vt);
		if (st != noErr) { printf("VTCompressionSessionCreate: %d\n", (int)st); return 4; }
		run.session = vt;
		CFStringRef profiles[] = {kVTProfileLevel_H264_High_AutoLevel, kVTProfileLevel_H264_Main_AutoLevel,
			kVTProfileLevel_H264_ConstrainedBaseline_AutoLevel, kVTProfileLevel_H264_ConstrainedHigh_AutoLevel};
		const char *pnames[] = {"High", "Main", "Constrained Baseline", "Constrained High"};
		int bitrate = o.bitrate > 0 ? o.bitrate : 8000000;
		struct { CFStringRef k; CFTypeRef v; const char *name; } props[] = {
			{kVTCompressionPropertyKey_RealTime, kCFBooleanTrue, "RealTime"},
			{kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse, "AllowFrameReordering"},
			{kVTCompressionPropertyKey_ProfileLevel, profiles[o.profile], "ProfileLevel"},
			{kVTCompressionPropertyKey_AverageBitRate, (__bridge CFTypeRef)@(bitrate), "AverageBitRate"},
			{kVTCompressionPropertyKey_ExpectedFrameRate, (__bridge CFTypeRef)@60, "ExpectedFrameRate"},
			{kVTCompressionPropertyKey_MaxKeyFrameInterval, (__bridge CFTypeRef)@(1000000), "MaxKeyFrameInterval"},
			{kVTCompressionPropertyKey_ColorPrimaries, kCVImageBufferColorPrimaries_ITU_R_709_2, "ColorPrimaries"},
			{kVTCompressionPropertyKey_TransferFunction, kCVImageBufferTransferFunction_ITU_R_709_2, "TransferFunction"},
			{kVTCompressionPropertyKey_YCbCrMatrix, kCVImageBufferYCbCrMatrix_ITU_R_709_2, "YCbCrMatrix"},
		};
		for (size_t i = 0; i < sizeof(props) / sizeof(props[0]); i++) {
			OSStatus ps = VTSessionSetProperty(vt, props[i].k, props[i].v);
			if (ps != noErr) printf("set %s: %d\n", props[i].name, (int)ps);
		}
		CFBooleanRef hw = NULL;
		VTSessionCopyProperty(vt, kVTCompressionPropertyKey_UsingHardwareAcceleratedVideoEncoder, NULL, &hw);
		printf("encoder: %dx%d %s, %s range input, %.1f Mbit/s, hardware %d, low-latency rate control %d\n", o.width, o.height, pnames[o.profile],
			o.fullRange ? "full" : "video", bitrate / 1e6, hw ? CFBooleanGetValue(hw) : -1, o.lowLatency);
		if (hw) CFRelease(hw);
		VTCompressionSessionPrepareToEncodeFrames(vt);

		SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:disp excludingApplications:@[] exceptingWindows:@[]];
		SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
		cfg.width = o.width;
		cfg.height = o.height;
		cfg.pixelFormat = o.fullRange ? kCVPixelFormatType_420YpCbCr8BiPlanarFullRange : kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
		cfg.colorMatrix = kCGDisplayStreamYCbCrMatrix_ITU_R_709_2;
		cfg.colorSpaceName = kCGColorSpaceSRGB;
		cfg.minimumFrameInterval = CMTimeMake(1, 60);
		cfg.queueDepth = 5;
		cfg.showsCursor = NO;
		SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:run];
		NSError *aerr = nil;
		[stream addStreamOutput:run type:SCStreamOutputTypeScreen sampleHandlerQueue:dispatch_queue_create("probe.h264", DISPATCH_QUEUE_SERIAL) error:&aerr];
		if (aerr) { printf("add output: %s\n", aerr.description.UTF8String); return 5; }

		// Motion: a large window whose colour and a moving bar change every frame.
		NSWindow *win = [[NSWindow alloc] initWithContentRect:NSMakeRect(60, 60, 960, 540) styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
		win.level = NSFloatingWindowLevel;
		NSView *bar = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 80, 540)];
		bar.wantsLayer = YES;
		bar.layer.backgroundColor = NSColor.whiteColor.CGColor;
		[win.contentView addSubview:bar];
		[win orderFrontRegardless];
		__block int n = 0;
		NSTimer *timer = [NSTimer scheduledTimerWithTimeInterval:1.0 / 60 repeats:YES block:^(NSTimer *t) {
			n++;
			win.backgroundColor = [NSColor colorWithHue:(n % 240) / 240.0 saturation:0.8 brightness:0.9 alpha:1];
			bar.frame = NSMakeRect((n * 8) % 880, 0, 80, 540);
		}];

		__block int started = 0;
		__block NSError *serr = nil;
		[stream startCaptureWithCompletionHandler:^(NSError *e) { serr = e; started = 1; }];
		while (!started) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, false);
		if (serr) { printf("start: %s\n", serr.description.UTF8String); return 6; }
		double t0 = hnow();
		while (hnow() - t0 < o.seconds) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, false);
		__block int stopped = 0;
		[stream stopCaptureWithCompletionHandler:^(NSError *e) { stopped = 1; }];
		while (!stopped) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, false);
		double span = hnow() - t0;
		VTCompressionSessionCompleteFrames(vt, kCMTimeInvalid);
		[timer invalidate];
		[win orderOut:nil];
		VTCompressionSessionInvalidate(vt);
		CFRelease(vt);
		if (run.out) fclose(run.out);
		IOPMAssertionRelease(keep);
		IOPMAssertionRelease(wake);

		int cnt = atomic_load(&gTimeCount);
		if (cnt > MAXF) cnt = MAXF;
		qsort(gTimes, cnt, sizeof(double), cmpd);
		double sum = 0;
		for (int i = 0; i < cnt; i++) sum += gTimes[i];
		printf("frames submitted %d, encoded %d (%.1f/s), key frames %d, dropped %d, failed %d\n", run.submitted, gEncoded,
			gEncoded / span, gKeyframes, gDropped, gFailed);
		if (cnt > 0)
			printf("encode time ms: mean %.2f, median %.2f, p95 %.2f, p99 %.2f, max %.2f\n", 1000 * sum / cnt, 1000 * gTimes[cnt / 2],
				1000 * gTimes[cnt * 95 / 100], 1000 * gTimes[cnt * 99 / 100], 1000 * gTimes[cnt - 1]);
		printf("bytes %lld, %.2f Mbit/s over %.1fs\n", (long long)gBytes, gBytes * 8.0 / span / 1e6, span);
		if (run.stopError) printf("stream stopped with error: %s\n", run.stopError.UTF8String);
		return 0;
	}
}
