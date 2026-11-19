//go:build darwin

#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>

#include "capture_darwin.h"
#include "_cgo_export.h"

// The longest wait for ScreenCaptureKit's answers.
static const int64_t answerWait = 5 * NSEC_PER_SEC;

int mardpCapGranted(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }

static int displayUUID(CGDirectDisplayID id, char *buf, int len) {
	CFUUIDRef u = CGDisplayCreateUUIDFromDisplayID(id);
	if (u == NULL) {
		return 0;
	}
	CFStringRef s = CFUUIDCreateString(NULL, u);
	CFRelease(u);
	if (s == NULL) {
		return 0;
	}
	Boolean ok = CFStringGetCString(s, buf, len, kCFStringEncodingUTF8);
	CFRelease(s);
	return ok ? 1 : 0;
}

static void describe(CGDirectDisplayID id, mardpCapDisplay *out) {
	out->id = id;
	out->main = CGDisplayIsMain(id) ? 1 : 0;
	CGRect b = CGDisplayBounds(id);
	out->pointWidth = b.size.width;
	out->pointHeight = b.size.height;
	CGDisplayModeRef mode = CGDisplayCopyDisplayMode(id);
	if (mode != NULL) {
		out->pixelWidth = (long)CGDisplayModeGetPixelWidth(mode);
		out->pixelHeight = (long)CGDisplayModeGetPixelHeight(mode);
		CGDisplayModeRelease(mode);
	} else {
		out->pixelWidth = (long)CGDisplayPixelsWide(id);
		out->pixelHeight = (long)CGDisplayPixelsHigh(id);
	}
	out->name[0] = 0;
	@autoreleasepool {
		for (NSScreen *screen in [NSScreen screens]) {
			NSNumber *n = screen.deviceDescription[@"NSScreenNumber"];
			if (n != nil && n.unsignedIntValue == id && screen.localizedName != nil) {
				[screen.localizedName getCString:out->name maxLength:sizeof(out->name) encoding:NSUTF8StringEncoding];
				break;
			}
		}
	}
}

int mardpCapFind(const char *uuid, mardpCapDisplay *out) {
	CGDirectDisplayID ids[32];
	uint32_t n = 0;
	if (CGGetActiveDisplayList(32, ids, &n) != kCGErrorSuccess) {
		return 0;
	}
	for (uint32_t i = 0; i < n; i++) {
		char u[64];
		if (!displayUUID(ids[i], u, sizeof(u))) {
			continue;
		}
		if (uuid[0] == 0 ? CGDisplayIsMain(ids[i]) : strcmp(u, uuid) == 0) {
			describe(ids[i], out);
			memcpy(out->uuid, u, sizeof(u));
			return 1;
		}
	}
	return 0;
}

int mardpCapAlive(uint32_t id, const char *uuid) {
	char u[64];
	return CGDisplayIsActive(id) && displayUUID(id, u, sizeof(u)) && strcmp(u, uuid) == 0;
}

// MardpOutput takes the stream's callbacks to Go while it is live.
@interface MardpOutput : NSObject <SCStreamOutput, SCStreamDelegate>
@property(nonatomic) uintptr_t handle;
@property(nonatomic) BOOL live;
@end

@implementation MardpOutput

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)type {
	if (type != SCStreamOutputTypeScreen) {
		return;
	}
	CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (atts == NULL || CFArrayGetCount(atts) == 0) {
		return;
	}
	NSDictionary *info = (__bridge NSDictionary *)CFArrayGetValueAtIndex(atts, 0);
	NSNumber *status = info[SCStreamFrameInfoStatus];
	if (status == nil || status.integerValue != SCFrameStatusComplete) {
		return;
	}
	CVPixelBufferRef pb = CMSampleBufferGetImageBuffer(sb);
	if (pb == NULL || CVPixelBufferGetPixelFormatType(pb) != kCVPixelFormatType_32BGRA) {
		return;
	}
	NSArray *dirty = info[SCStreamFrameInfoDirtyRects];
	int32_t rects[4 * 64];
	int n = -1; // the frame did not say: all of it
	if ([dirty isKindOfClass:[NSArray class]] && dirty.count <= 64) {
		n = 0;
		for (id v in dirty) {
			// Each is a CGRect's dictionary form (macOS 27), or an NSValue
			// (the header's word).
			CGRect r;
			if ([v isKindOfClass:[NSDictionary class]]) {
				if (!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)v, &r)) {
					n = -1;
					break;
				}
			} else if ([v isKindOfClass:[NSValue class]]) {
				r = [(NSValue *)v rectValue];
			} else {
				n = -1;
				break;
			}
			r = CGRectStandardize(r);
			CGRect i = CGRectIntegral(r);
			rects[4 * n] = (int32_t)i.origin.x;
			rects[4 * n + 1] = (int32_t)i.origin.y;
			rects[4 * n + 2] = (int32_t)i.size.width;
			rects[4 * n + 3] = (int32_t)i.size.height;
			n++;
		}
	}
	if (CVPixelBufferLockBaseAddress(pb, kCVPixelBufferLock_ReadOnly) != kCVReturnSuccess) {
		return;
	}
	@synchronized(self) {
		if (self.live) {
			mardpCapFrame(self.handle, CVPixelBufferGetBaseAddress(pb), (int)CVPixelBufferGetBytesPerRow(pb),
				(int)CVPixelBufferGetWidth(pb), (int)CVPixelBufferGetHeight(pb), rects, n);
		}
	}
	CVPixelBufferUnlockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	long code = [error.domain isEqualToString:SCStreamErrorDomain] ? (long)error.code : -1;
	@synchronized(self) {
		if (self.live) {
			mardpCapStopped(self.handle, code, (char *)error.localizedDescription.UTF8String);
		}
	}
}

@end

static long fail(NSError *e, char *message, int len) {
	snprintf(message, len, "%s", e.localizedDescription.UTF8String ?: "");
	return [e.domain isEqualToString:SCStreamErrorDomain] ? (long)e.code : -1;
}

// start starts a stream; see mardpCapStart.
static long start(uintptr_t handle, uint32_t displayID, int width, int height, int fps,
	void **streamOut, void **outputOut, char *message, int len) {
	@autoreleasepool {
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		__block SCShareableContent *content = nil;
		__block NSError *err = nil;
		[SCShareableContent getShareableContentExcludingDesktopWindows:NO
		                                           onScreenWindowsOnly:YES
		                                             completionHandler:^(SCShareableContent *c, NSError *e) {
			content = c;
			err = e;
			dispatch_semaphore_signal(done);
		}];
		if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, answerWait)) != 0) {
			snprintf(message, len, "ScreenCaptureKit did not list the displays in time");
			return -1;
		}
		if (err != nil) {
			return fail(err, message, len);
		}
		SCDisplay *display = nil;
		for (SCDisplay *d in content.displays) {
			if (d.displayID == displayID) {
				display = d;
			}
		}
		if (display == nil) {
			snprintf(message, len, "ScreenCaptureKit does not list display %u", displayID);
			return SCStreamErrorNoCaptureSource;
		}
		SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
		SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
		cfg.width = width;
		cfg.height = height;
		cfg.pixelFormat = kCVPixelFormatType_32BGRA;
		cfg.colorSpaceName = kCGColorSpaceSRGB;
		cfg.showsCursor = NO;
		cfg.minimumFrameInterval = CMTimeMake(1, fps);
		cfg.queueDepth = 3;
		MardpOutput *output = [[MardpOutput alloc] init];
		output.handle = handle;
		output.live = YES;
		SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:output];
		dispatch_queue_t queue = dispatch_queue_create("mardp.capture", DISPATCH_QUEUE_SERIAL);
		NSError *addErr = nil;
		if (![stream addStreamOutput:output type:SCStreamOutputTypeScreen sampleHandlerQueue:queue error:&addErr]) {
			output.live = NO;
			return fail(addErr, message, len);
		}
		[stream startCaptureWithCompletionHandler:^(NSError *e) {
			err = e;
			dispatch_semaphore_signal(done);
		}];
		long wait = dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, answerWait));
		if (wait != 0 || err != nil) {
			long code = wait != 0 ? -1 : fail(err, message, len);
			if (wait != 0) {
				snprintf(message, len, "the stream did not start in time");
			}
			mardpCapStop((__bridge_retained void *)stream, (__bridge_retained void *)output);
			return code;
		}
		*streamOut = (__bridge_retained void *)stream;
		*outputOut = (__bridge_retained void *)output;
		return 0;
	}
}

mardpCapStarted mardpCapStart(uintptr_t handle, uint32_t displayID, int width, int height, int fps) {
	mardpCapStarted r = {0};
	r.code = start(handle, displayID, width, height, fps, &r.stream, &r.output, r.message, sizeof(r.message));
	return r;
}

void mardpCapStop(void *streamRef, void *outputRef) {
	@autoreleasepool {
		SCStream *stream = (__bridge_transfer SCStream *)streamRef;
		MardpOutput *output = (__bridge_transfer MardpOutput *)outputRef;
		// 1. Retire the output: no callback is in Go after this.
		@synchronized(output) {
			output.live = NO;
		}
		// 2. Stop the stream and wait for the answer.
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		[stream stopCaptureWithCompletionHandler:^(NSError *e) {
			dispatch_semaphore_signal(done);
		}];
		dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, answerWait));
		// 3. Remove the output; both are released when this scope ends.
		[stream removeStreamOutput:output type:SCStreamOutputTypeScreen error:nil];
	}
}
