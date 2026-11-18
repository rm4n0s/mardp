//go:build darwin

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <IOKit/pwr_mgt/IOPMLib.h>
#import <AVFoundation/AVFoundation.h>
#include <stdatomic.h>
#include <math.h>
#include "capture.h"

static double now(void) { return CFAbsoluteTimeGetCurrent(); }

@interface ProbeOut : NSObject <SCStreamOutput, SCStreamDelegate>
@property atomic_int frames, complete, idle, blank, suspended, other, withDirty, dirtyRects;
@property atomic_int audioBuffers;
@property (atomic) float audioPeak;
@property (atomic) double firstFrame, lastFrame, maxGap, luma;
@property (atomic, copy) NSString *format;
@property (atomic) int fw, fh, iosurface;
@property (atomic, copy) NSString *stopError;
@end

@implementation ProbeOut
- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)type {
	if (type == SCStreamOutputTypeAudio) {
		atomic_fetch_add(&_audioBuffers, 1);
		CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sb);
		size_t len = 0; char *data = NULL;
		if (bb && CMBlockBufferGetDataPointer(bb, 0, NULL, &len, &data) == kCMBlockBufferNoErr) {
			const float *s = (const float *)data;
			float p = self.audioPeak;
			for (size_t i = 0; i < len / sizeof(float); i++) { float v = fabsf(s[i]); if (v > p) p = v; }
			self.audioPeak = p;
		}
		return;
	}
	if (type != SCStreamOutputTypeScreen) return;
	CFArrayRef arr = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (!arr || CFArrayGetCount(arr) == 0) return;
	NSDictionary *att = (__bridge NSDictionary *)CFArrayGetValueAtIndex(arr, 0);
	NSNumber *st = att[SCStreamFrameInfoStatus];
	switch ((SCFrameStatus)st.integerValue) {
	case SCFrameStatusComplete: atomic_fetch_add(&_complete, 1); break;
	case SCFrameStatusIdle: atomic_fetch_add(&_idle, 1); return;
	case SCFrameStatusBlank: atomic_fetch_add(&_blank, 1); return;
	case SCFrameStatusSuspended: atomic_fetch_add(&_suspended, 1); return;
	default: atomic_fetch_add(&_other, 1); return;
	}
	double t = now();
	if (self.firstFrame == 0) self.firstFrame = t;
	if (self.lastFrame != 0 && t - self.lastFrame > self.maxGap) self.maxGap = t - self.lastFrame;
	self.lastFrame = t;
	atomic_fetch_add(&_frames, 1);
	CVImageBufferRef yb = CMSampleBufferGetImageBuffer(sb);
	if (yb && CVPixelBufferLockBaseAddress(yb, kCVPixelBufferLock_ReadOnly) == kCVReturnSuccess) {
		const uint8_t *y = CVPixelBufferGetBaseAddressOfPlane(yb, 0);
		size_t w = CVPixelBufferGetWidthOfPlane(yb, 0), h = CVPixelBufferGetHeightOfPlane(yb, 0), bpr = CVPixelBufferGetBytesPerRowOfPlane(yb, 0);
		double sum = 0; int n = 0;
		for (size_t r = h / 72; r < h; r += h / 36) for (size_t c = w / 128; c < w; c += w / 64) { sum += y[r * bpr + c]; n++; }
		CVPixelBufferUnlockBaseAddress(yb, kCVPixelBufferLock_ReadOnly);
		if (n) self.luma = sum / n;
	}
	NSArray *dirty = att[SCStreamFrameInfoDirtyRects];
	if (dirty.count > 0) { atomic_fetch_add(&_withDirty, 1); atomic_fetch_add(&_dirtyRects, (int)dirty.count); }
	if (self.format == nil) {
		CVImageBufferRef img = CMSampleBufferGetImageBuffer(sb);
		if (img) {
			OSType pf = CVPixelBufferGetPixelFormatType(img);
			char c[5] = {(char)(pf >> 24), (char)(pf >> 16), (char)(pf >> 8), (char)pf, 0};
			self.fw = (int)CVPixelBufferGetWidth(img);
			self.fh = (int)CVPixelBufferGetHeight(img);
			self.iosurface = CVPixelBufferGetIOSurface(img) != NULL;
			CFDictionaryRef a = CVBufferCopyAttachments(img, kCVAttachmentMode_ShouldPropagate);
			self.format = [NSString stringWithFormat:@"%s attachments=%@", c, (__bridge_transfer NSDictionary *)a];
		}
	}
}
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	self.stopError = error.description;
}
@end

AVAudioEngine *startTone(void) {
	AVAudioEngine *eng = [[AVAudioEngine alloc] init];
	AVAudioFormat *fmt = [[AVAudioFormat alloc] initStandardFormatWithSampleRate:48000 channels:2];
	__block double phase = 0;
	AVAudioSourceNode *src = [[AVAudioSourceNode alloc] initWithFormat:fmt renderBlock:^OSStatus(BOOL *silence, const AudioTimeStamp *ts, AVAudioFrameCount n, AudioBufferList *out) {
		for (AVAudioFrameCount i = 0; i < n; i++) {
			float v = 0.25f * sinf((float)phase);
			phase += 2 * M_PI * 440 / 48000; if (phase > 2 * M_PI) phase -= 2 * M_PI;
			for (UInt32 b = 0; b < out->mNumberBuffers; b++) ((float *)out->mBuffers[b].mData)[i] = v;
		}
		return noErr;
	}];
	[eng attachNode:src];
	[eng connect:src to:eng.mainMixerNode format:fmt];
	NSError *err = nil;
	if (![eng startAndReturnError:&err]) { printf("tone: %s\n", err.description.UTF8String); return nil; }
	return eng;
}

int probeTone(double seconds) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		NSWindow *win = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 2, 2) styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
		win.alphaValue = 0.01;
		[win orderFrontRegardless];
		AVAudioEngine *eng = startTone();
		if (!eng) return 1;
		double t0 = now();
		while (now() - t0 < seconds) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, false);
		[win orderOut:nil];
		[eng stop];
		return 0;
	}
}

int probeCapture(probeOpts o) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		CGDirectDisplayID mainID = CGMainDisplayID();
		printf("display %u asleep before wake: %d\n", mainID, CGDisplayIsAsleep(mainID));

		IOPMAssertionID wake = 0, keep = 0;
		IOReturn r1 = IOPMAssertionDeclareUserActivity(CFSTR("mardp probe"), kIOPMUserActiveLocal, &wake);
		IOReturn r2 = IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleDisplaySleep, kIOPMAssertionLevelOn, CFSTR("mardp probe"), &keep);
		printf("wake assertion %d, keep-awake assertion %d\n", r1, r2);
		CFRunLoopRunInMode(kCFRunLoopDefaultMode, 1.0, false);
		printf("display asleep after wake: %d\n", CGDisplayIsAsleep(mainID));

		__block SCShareableContent *content = nil;
		__block NSError *cerr = nil;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) {
			content = c; cerr = e; dispatch_semaphore_signal(sem);
		}];
		while (dispatch_semaphore_wait(sem, DISPATCH_TIME_NOW)) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, false);
		if (!content) { printf("shareable content: %s\n", cerr.description.UTF8String); return 1; }
		SCDisplay *disp = nil;
		for (SCDisplay *d in content.displays) if (d.displayID == mainID) disp = d;
		if (!disp) { printf("main display not in shareable content (%lu displays)\n", (unsigned long)content.displays.count); return 2; }

		NSMutableArray *excl = [NSMutableArray array];
		if (o.excludeBundle && o.excludeBundle[0]) {
			NSString *bid = [NSString stringWithUTF8String:o.excludeBundle];
			for (SCRunningApplication *a in content.applications) if ([a.bundleIdentifier isEqualToString:bid]) [excl addObject:a];
			printf("excluding %lu running app(s) with bundle %s\n", (unsigned long)excl.count, o.excludeBundle);
		}
		if (o.excludePid > 0) {
			for (SCRunningApplication *a in content.applications) if (a.processID == o.excludePid) { [excl addObject:a]; printf("excluding pid %d (%s, bundle '%s')\n", o.excludePid, a.applicationName.UTF8String, a.bundleIdentifier.UTF8String); }
			if (excl.count == 0) printf("pid %d is not among %lu shareable applications\n", o.excludePid, (unsigned long)content.applications.count);
		}
		SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:disp excludingApplications:excl exceptingWindows:@[]];
		SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
		CGDisplayModeRef mode = CGDisplayCopyDisplayMode(mainID);
		int pw = (int)CGDisplayModeGetPixelWidth(mode), ph = (int)CGDisplayModeGetPixelHeight(mode);
		CGDisplayModeRelease(mode);
		cfg.width = o.width ? o.width : pw;
		cfg.height = o.height ? o.height : ph;
		printf("display %dx%d pixels (%dx%d points), capturing %zux%zu\n", pw, ph, disp.width, disp.height, cfg.width, cfg.height);
		cfg.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
		cfg.colorMatrix = kCGDisplayStreamYCbCrMatrix_ITU_R_709_2;
		cfg.colorSpaceName = kCGColorSpaceSRGB;
		cfg.minimumFrameInterval = CMTimeMake(1, 60);
		cfg.queueDepth = 5;
		cfg.showsCursor = NO;
		cfg.capturesAudio = o.audio != 0;
		cfg.excludesCurrentProcessAudio = o.excludeSelf != 0;
		cfg.sampleRate = 48000;
		cfg.channelCount = 2;

		ProbeOut *out = [[ProbeOut alloc] init];
		SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:out];
		dispatch_queue_t q = dispatch_queue_create("probe.video", DISPATCH_QUEUE_SERIAL);
		NSError *aerr = nil;
		[stream addStreamOutput:out type:SCStreamOutputTypeScreen sampleHandlerQueue:q error:&aerr];
		if (o.audio) [stream addStreamOutput:out type:SCStreamOutputTypeAudio sampleHandlerQueue:dispatch_queue_create("probe.audio", DISPATCH_QUEUE_SERIAL) error:&aerr];
		if (aerr) { printf("add output: %s\n", aerr.description.UTF8String); return 3; }

		NSWindow *win = nil;
		NSTimer *timer = nil;
		if (o.animate) {
			win = [[NSWindow alloc] initWithContentRect:NSMakeRect(40, 40, 320, 200) styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
			win.level = NSFloatingWindowLevel;
			[win orderFrontRegardless];
			__block int n = 0;
			timer = [NSTimer scheduledTimerWithTimeInterval:1.0 / 60 repeats:YES block:^(NSTimer *t) {
				n++;
				win.backgroundColor = [NSColor colorWithHue:(n % 120) / 120.0 saturation:1 brightness:1 alpha:1];
			}];
		}
		AVAudioEngine *tone = o.tone ? startTone() : nil;

		__block NSError *serr = nil;
		__block int started = 0;
		[stream startCaptureWithCompletionHandler:^(NSError *e) { serr = e; started = 1; }];
		while (!started) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, false);
		if (serr) { printf("start: %s\n", serr.description.UTF8String); return 4; }
		double t0 = now(), nextReport = o.reportEvery;
		int moved = 0, lastFrames = 0, lastIdle = 0, lastBlank = 0, lastSusp = 0;
		while (now() - t0 < o.seconds) {
			CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, false);
			double el = now() - t0;
			if (o.moveAt > 0 && !moved && el >= o.moveAt) {
				moved = 1;
				double before = CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateCombinedSessionState, kCGEventMouseMoved);
				CGEventRef here = CGEventCreate(NULL);
				CGPoint p = CGEventGetLocation(here);
				CFRelease(here);
				CGEventRef mv = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, p, kCGMouseButtonLeft);
				CGEventPost(kCGHIDEventTap, mv);
				CFRelease(mv);
				CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.2, false);
				double after = CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateCombinedSessionState, kCGEventMouseMoved);
				printf("%6.1fs posted a zero-distance mouse move at %.0f,%.0f (accessibility %d); seconds since the last mouse move: %.1f before, %.1f after\n",
					el, p.x, p.y, AXIsProcessTrusted(), before, after);
				fflush(stdout);
			}
			if (o.reportEvery > 0 && el >= nextReport) {
				nextReport += o.reportEvery;
				NSDictionary *sess = (__bridge_transfer NSDictionary *)CGSessionCopyCurrentDictionary();
				int f = out.frames, i = out.idle, b = out.blank, su = out.suspended;
				printf("%6.1fs locked %d on console %d: frames %d idle %d blank %d suspended %d, mean luma %.0f\n", el,
					[sess[@"CGSSessionScreenIsLocked"] boolValue], [sess[(__bridge NSString *)kCGSessionOnConsoleKey] boolValue],
					f - lastFrames, i - lastIdle, b - lastBlank, su - lastSusp, out.luma);
				fflush(stdout);
				lastFrames = f; lastIdle = i; lastBlank = b; lastSusp = su;
			}
		}
		__block int stopped = 0;
		[stream stopCaptureWithCompletionHandler:^(NSError *e) { stopped = 1; }];
		while (!stopped) CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, false);
		[timer invalidate];
		[win orderOut:nil];
		[tone stop];
		IOPMAssertionRelease(keep);
		IOPMAssertionRelease(wake);

		double span = out.lastFrame - out.firstFrame;
		printf("frames with content %d over %.1fs (%.1f/s), idle %d, blank %d, suspended %d, other %d\n",
			out.frames, o.seconds, out.frames / o.seconds, out.idle, out.blank, out.suspended, out.other);
		printf("first frame after %.3fs, longest gap %.3fs, span %.1fs\n", out.firstFrame ? out.firstFrame - t0 : -1, out.maxGap, span);
		printf("frames with dirty rects %d (rects %d)\n", out.withDirty, out.dirtyRects);
		printf("frame %dx%d iosurface=%d format %s\n", out.fw, out.fh, out.iosurface, out.format.UTF8String);
		if (o.audio) printf("audio buffers %d, peak %.4f\n", out.audioBuffers, out.audioPeak);
		if (out.stopError) printf("stream stopped with error: %s\n", out.stopError.UTF8String);
		return 0;
	}
}
