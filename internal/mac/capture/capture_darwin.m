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

// mardpSubmitFn is the encoder's submit function (vtenc's mardpEncSubmit).
typedef long (*mardpSubmitFn)(void *arg, CVPixelBufferRef pb, CMTime pts);

// MardpOutput takes the stream's callbacks to Go, or its frames to the
// encoder, while it is live. Its counters change under its lock.
@interface MardpOutput : NSObject <SCStreamOutput, SCStreamDelegate>
@property(nonatomic) uintptr_t handle;
@property(nonatomic) BOOL live;
@property(nonatomic) BOOL video;
@property(nonatomic) BOOL sound;
@property(nonatomic) OSType format;
@property(nonatomic) mardpSubmitFn submit;
@property(nonatomic) void *arg;
@property(nonatomic) mardpCapCounts counts;
@end

@implementation MardpOutput

// sound hands a buffer of sound to Go: 32-bit float at 48 kHz, two
// channels not interleaved (or one, played on both).
- (void)sound:(CMSampleBufferRef)sb {
	const AudioStreamBasicDescription *f =
		CMAudioFormatDescriptionGetStreamBasicDescription(CMSampleBufferGetFormatDescription(sb));
	if (f == NULL || f->mFormatID != kAudioFormatLinearPCM || (f->mFormatFlags & kAudioFormatFlagIsFloat) == 0 ||
		(f->mFormatFlags & kAudioFormatFlagIsNonInterleaved) == 0 || f->mBitsPerChannel != 32 || f->mSampleRate != 48000) {
		return;
	}
	struct {
		AudioBufferList list;
		AudioBuffer more[1];
	} abl;
	CMBlockBufferRef bb = NULL;
	if (CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(sb, NULL, &abl.list, sizeof(abl), NULL, NULL,
			kCMSampleBufferFlag_AudioBufferList_Assure16ByteAlignment, &bb) != noErr) {
		return;
	}
	UInt32 n = abl.list.mNumberBuffers;
	if (n == 1 || n == 2) {
		AudioBuffer *l = &abl.list.mBuffers[0], *r = &abl.list.mBuffers[n - 1];
		UInt32 frames = MIN(l->mDataByteSize, r->mDataByteSize) / sizeof(float);
		CMTime pts = CMSampleBufferGetPresentationTimeStamp(sb);
		@synchronized(self) {
			if (self.live && frames > 0) {
				mardpCapSound(self.handle, (float *)l->mData, (float *)r->mData, (int)frames, CMTimeGetSeconds(pts));
			}
		}
	}
	CFRelease(bb);
}

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)type {
	if (type == SCStreamOutputTypeAudio && self.sound) {
		[self sound:sb];
		return;
	}
	if (type != SCStreamOutputTypeScreen || self.sound) {
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
	if (pb == NULL || CVPixelBufferGetPixelFormatType(pb) != self.format) {
		return;
	}
	if (self.video) {
		// A frame whose dirty rectangles are none changed nothing: ScreenCaptureKit
		// sends them, for example, when only content outside the filter moved.
		NSArray *changed = info[SCStreamFrameInfoDirtyRects];
		if ([changed isKindOfClass:[NSArray class]] && changed.count == 0) {
			return;
		}
		// Straight to the encoder, in C (SPEC §4.2).
		CMTime pts = CMSampleBufferGetPresentationTimeStamp(sb);
		@synchronized(self) {
			if (self.live) {
				long st = self.submit(self.arg, pb, pts);
				mardpCapCounts c = self.counts;
				if (st == 0) {
					c.encoded++;
				} else if (st == 1) {
					c.skipped++;
				} else {
					c.refused++;
				}
				self.counts = c;
			}
		}
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
			mardpCapCounts c = self.counts;
			c.crossings++;
			self.counts = c;
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

// shareable asks ScreenCaptureKit for what it can capture; nil with *err
// set, or nil alone when it did not answer in time.
static SCShareableContent *shareable(NSError **errOut) {
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
		*errOut = nil;
		return nil;
	}
	*errOut = err;
	return err != nil ? nil : content;
}

// leftOut returns the applications of content whose bundle identifiers
// match apps (one a line; one that ends in "." is a prefix).
static NSArray<SCRunningApplication *> *leftOut(SCShareableContent *content, const char *apps) {
	NSMutableArray<NSString *> *list = [NSMutableArray array];
	for (NSString *a in [[NSString stringWithUTF8String:apps ?: ""] componentsSeparatedByString:@"\n"]) {
		if (a.length > 0) {
			[list addObject:a];
		}
	}
	NSMutableArray<SCRunningApplication *> *out = [NSMutableArray array];
	for (SCRunningApplication *app in content.applications) {
		NSString *bid = app.bundleIdentifier;
		if (bid.length == 0) {
			continue;
		}
		for (NSString *a in list) {
			if ([a hasSuffix:@"."] ? [bid hasPrefix:a] : [bid isEqualToString:a]) {
				[out addObject:app];
				break;
			}
		}
	}
	return out;
}

long mardpCapExcluded(const char *apps) {
	@autoreleasepool {
		NSError *err = nil;
		SCShareableContent *content = shareable(&err);
		return content == nil ? -1 : (long)leftOut(content, apps).count;
	}
}

// start starts a stream; see mardpCapStart.
static long start(uintptr_t handle, uint32_t displayID, int width, int height, int fps, int video, int fullRange,
	void *submit, void *arg, uint32_t windowID, int sound, int includeSelf, const char *apps, long *excluded,
	void **streamOut, void **outputOut, char *message, int len) {
	@autoreleasepool {
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		__block NSError *err = nil;
		SCShareableContent *content = shareable(&err);
		if (content == nil && err == nil) {
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
		SCContentFilter *filter = nil;
		if (windowID != 0) {
			SCWindow *window = nil;
			for (SCWindow *w in content.windows) {
				if (w.windowID == windowID) {
					window = w;
				}
			}
			if (window == nil) {
				snprintf(message, len, "ScreenCaptureKit does not list window %u", windowID);
				return SCStreamErrorNoCaptureSource;
			}
			// The window alone: a display filter that includes it still
			// reports the whole display's changes.
			filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:window];
		} else if (sound) {
			NSArray<SCRunningApplication *> *out = leftOut(content, apps);
			*excluded = (long)out.count;
			filter = [[SCContentFilter alloc] initWithDisplay:display excludingApplications:out exceptingWindows:@[]];
		} else {
			filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
		}
		SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
		cfg.width = width;
		cfg.height = height;
		OSType format = kCVPixelFormatType_32BGRA;
		if (video) {
			format = fullRange ? kCVPixelFormatType_420YpCbCr8BiPlanarFullRange : kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
			cfg.colorMatrix = kCGDisplayStreamYCbCrMatrix_ITU_R_709_2;
		}
		cfg.pixelFormat = format;
		cfg.colorSpaceName = kCGColorSpaceSRGB;
		cfg.showsCursor = NO;
		cfg.minimumFrameInterval = CMTimeMake(1, fps);
		// The encoder keeps the newest picture, to encode it again while the
		// screen is still (vtenc), so a capture into it has two more surfaces.
		cfg.queueDepth = video ? 5 : 3;
		if (sound) {
			cfg.capturesAudio = YES;
			cfg.excludesCurrentProcessAudio = includeSelf ? NO : YES;
			cfg.sampleRate = 48000;
			cfg.channelCount = 2;
		}
		MardpOutput *output = [[MardpOutput alloc] init];
		output.handle = handle;
		output.video = video != 0;
		output.sound = sound != 0;
		output.format = format;
		output.submit = (mardpSubmitFn)submit;
		output.arg = arg;
		output.live = YES;
		SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:output];
		dispatch_queue_t queue = dispatch_queue_create("mardp.capture", DISPATCH_QUEUE_SERIAL);
		NSError *addErr = nil;
		// A stream of sound takes its pictures too, and drops them:
		// ScreenCaptureKit logs each one that has no output.
		if (![stream addStreamOutput:output type:SCStreamOutputTypeScreen sampleHandlerQueue:queue error:&addErr]) {
			output.live = NO;
			return fail(addErr, message, len);
		}
		if (sound && ![stream addStreamOutput:output
		                                  type:SCStreamOutputTypeAudio
		                    sampleHandlerQueue:dispatch_queue_create("mardp.sound", DISPATCH_QUEUE_SERIAL)
		                                 error:&addErr]) {
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
			mardpCapStop((__bridge_retained void *)stream, (__bridge_retained void *)output, NULL);
			return code;
		}
		*streamOut = (__bridge_retained void *)stream;
		*outputOut = (__bridge_retained void *)output;
		return 0;
	}
}

mardpCapStarted mardpCapStart(uintptr_t handle, uint32_t displayID, int width, int height, int fps,
	int video, int fullRange, void *submit, void *arg, uint32_t window, int sound, int includeSelf, const char *apps) {
	mardpCapStarted r = {0};
	r.code = start(handle, displayID, width, height, fps, video, fullRange, submit, arg, window, sound, includeSelf, apps,
		&r.excluded, &r.stream, &r.output, r.message, sizeof(r.message));
	return r;
}

void mardpCapStop(void *streamRef, void *outputRef, mardpCapCounts *counts) {
	@autoreleasepool {
		SCStream *stream = (__bridge_transfer SCStream *)streamRef;
		MardpOutput *output = (__bridge_transfer MardpOutput *)outputRef;
		// 1. Retire the output: no callback is in Go after this.
		@synchronized(output) {
			output.live = NO;
			if (counts != NULL) {
				*counts = output.counts;
			}
		}
		// 2. Stop the stream and wait for the answer.
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		[stream stopCaptureWithCompletionHandler:^(NSError *e) {
			dispatch_semaphore_signal(done);
		}];
		dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, answerWait));
		// 3. Remove the output; both are released when this scope ends.
		[stream removeStreamOutput:output type:SCStreamOutputTypeScreen error:nil];
		if (output.sound) {
			[stream removeStreamOutput:output type:SCStreamOutputTypeAudio error:nil];
		}
	}
}
