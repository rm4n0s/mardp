//go:build darwin && mardp_mac

package capture

/*
#cgo LDFLAGS: -framework AppKit -framework QuartzCore
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>

// MardpTestWindow is a window the tests open, whose color and a bar move
// every frame while it animates.
@interface MardpTestWindow : NSObject
@property(strong) NSWindow *window;
@property(strong) NSView *bar;
@property(strong) NSTimer *timer;
@property int n;
@end

@implementation MardpTestWindow
@end

static void mardpTestMainLoop(void) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	}
	CFRunLoopRun();
}

static void mardpTestMainStop(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		CFRunLoopStop(CFRunLoopGetMain());
	});
}

static void *mardpTestWindowOpen(void) {
	__block void *ref = NULL;
	dispatch_sync(dispatch_get_main_queue(), ^{
		MardpTestWindow *w = [[MardpTestWindow alloc] init];
		w.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(60, 60, 960, 540)
		                                       styleMask:NSWindowStyleMaskBorderless
		                                         backing:NSBackingStoreBuffered
		                                           defer:NO];
		w.window.releasedWhenClosed = NO;
		w.window.level = NSFloatingWindowLevel;
		w.window.backgroundColor = NSColor.darkGrayColor;
		w.bar = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 80, 540)];
		w.bar.wantsLayer = YES;
		w.bar.layer.backgroundColor = NSColor.whiteColor.CGColor;
		[w.window.contentView addSubview:w.bar];
		[w.window orderFrontRegardless];
		ref = (__bridge_retained void *)w;
	});
	return ref;
}

static void mardpTestWindowAnimate(void *ref, int on) {
	MardpTestWindow *w = (__bridge MardpTestWindow *)ref;
	dispatch_sync(dispatch_get_main_queue(), ^{
		[w.timer invalidate];
		w.timer = nil;
		if (on) {
			w.timer = [NSTimer scheduledTimerWithTimeInterval:1.0 / 60 repeats:YES block:^(NSTimer *t) {
				w.n++;
				w.window.backgroundColor = [NSColor colorWithHue:(w.n % 240) / 240.0 saturation:0.8 brightness:0.9 alpha:1];
				w.bar.frame = NSMakeRect((w.n * 8) % 880, 0, 80, 540);
			}];
		}
	});
}

static uint32_t mardpTestWindowID(void *ref) {
	MardpTestWindow *w = (__bridge MardpTestWindow *)ref;
	__block uint32_t id = 0;
	dispatch_sync(dispatch_get_main_queue(), ^{
		id = (uint32_t)w.window.windowNumber;
	});
	return id;
}

static void mardpTestWindowClose(void *ref) {
	MardpTestWindow *w = (__bridge_transfer MardpTestWindow *)ref;
	dispatch_sync(dispatch_get_main_queue(), ^{
		[w.timer invalidate];
		w.timer = nil;
		[w.window orderOut:nil];
		[w.window close];
	});
}
*/
import "C"

import "unsafe"

// runMainLoop runs the main thread's run loop, which AppKit needs, until
// stopMainLoop; it must be called on the main thread.
func runMainLoop() { C.mardpTestMainLoop() }

// stopMainLoop makes runMainLoop return.
func stopMainLoop() { C.mardpTestMainStop() }

// testWindow is a window a test opens on the screen.
type testWindow struct{ ref unsafe.Pointer }

// openTestWindow opens a window of 960×540 points near the bottom left of
// the main display, above the other windows, still until animate.
func openTestWindow() *testWindow { return &testWindow{ref: C.mardpTestWindowOpen()} }

// animate starts or stops changing the window every frame.
func (w *testWindow) animate(on bool) {
	var v C.int
	if on {
		v = 1
	}
	C.mardpTestWindowAnimate(w.ref, v)
}

// id is the window's CGWindowID.
func (w *testWindow) id() uint32 { return uint32(C.mardpTestWindowID(w.ref)) }

// close closes the window.
func (w *testWindow) close() { C.mardpTestWindowClose(w.ref) }
