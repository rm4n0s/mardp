//go:build darwin && mardp_mac

package capture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AVFoundation -framework Foundation
#import <AVFoundation/AVFoundation.h>
#include <math.h>

// mardpTestToneStart plays a sine tone of freq hertz at level (1 is full
// scale) on both channels, from this process; NULL when it cannot.
static void *mardpTestToneStart(double freq, double level) {
	AVAudioEngine *eng = [[AVAudioEngine alloc] init];
	AVAudioFormat *fmt = [[AVAudioFormat alloc] initStandardFormatWithSampleRate:48000 channels:2];
	__block double phase = 0;
	double step = 2 * M_PI * freq / 48000;
	AVAudioSourceNode *src = [[AVAudioSourceNode alloc]
		initWithFormat:fmt
		   renderBlock:^OSStatus(BOOL *silence, const AudioTimeStamp *ts, AVAudioFrameCount n, AudioBufferList *out) {
			for (AVAudioFrameCount i = 0; i < n; i++) {
				float v = (float)(level * sin(phase));
				phase += step;
				if (phase > 2 * M_PI) {
					phase -= 2 * M_PI;
				}
				for (UInt32 b = 0; b < out->mNumberBuffers; b++) {
					((float *)out->mBuffers[b].mData)[i] = v;
				}
			}
			return noErr;
		}];
	[eng attachNode:src];
	// The call that takes an error is macOS 27's; the tests run on 14 too.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	[eng connect:src to:eng.mainMixerNode format:fmt];
#pragma clang diagnostic pop
	NSError *err = nil;
	if (![eng startAndReturnError:&err]) {
		return NULL;
	}
	return (__bridge_retained void *)eng;
}

static void mardpTestToneStop(void *ref) {
	AVAudioEngine *eng = (__bridge_transfer AVAudioEngine *)ref;
	[eng stop];
}

// mardpTestBundleID writes this process's bundle identifier into buf, empty
// when it has none.
static void mardpTestBundleID(char *buf, int len) {
	buf[0] = 0;
	NSString *b = NSBundle.mainBundle.bundleIdentifier;
	if (b != nil) {
		[b getCString:buf maxLength:len encoding:NSUTF8StringEncoding];
	}
}
*/
import "C"

import "unsafe"

// testTone is a tone the test process plays.
type testTone struct{ ref unsafe.Pointer }

// playTestTone plays a sine tone of freq hertz at level on both channels
// until stop; nil when the process cannot play. It does not touch the
// output's volume or mute.
func playTestTone(freq, level float64) *testTone {
	ref := C.mardpTestToneStart(C.double(freq), C.double(level))
	if ref == nil {
		return nil
	}
	return &testTone{ref: ref}
}

// stop stops the tone.
func (t *testTone) stop() { C.mardpTestToneStop(t.ref) }

// testBundleID is the test process's bundle identifier, empty when it does
// not run as an app.
func testBundleID() string {
	var buf [256]C.char
	C.mardpTestBundleID(&buf[0], C.int(len(buf)))
	return C.GoString(&buf[0])
}
