//go:build darwin

#import <AppKit/AppKit.h>
#import <objc/runtime.h>
#include <stdatomic.h>

#include "quit_darwin.h"
#include "_cgo_export.h"

// A method has one implementation for the whole process, so what replaces
// the delegate's answer reads the watcher's handle from here; 0 when none
// watches. answerBefore is the delegate's own answer.
static _Atomic(uintptr_t) quitHandle;
static _Atomic(int) installed;
static IMP answerBefore;

static NSApplicationTerminateReply shouldTerminate(id self, SEL cmd, NSApplication *app) {
	uintptr_t h = atomic_load(&quitHandle);
	if (h != 0) {
		mardpQuit(h);
		// Not now: the app stops its server and ends by itself. Saying
		// "now" would end the process before that.
		return NSTerminateCancel;
	}
	if (answerBefore != NULL) {
		return ((NSApplicationTerminateReply (*)(id, SEL, id))answerBefore)(self, cmd, app);
	}
	return NSTerminateNow;
}

// install replaces the answer of the application's delegate; it returns
// NO while the application has no delegate yet.
static BOOL install(void) {
	if (atomic_load(&installed)) {
		return YES;
	}
	id delegate = NSApp != nil ? [NSApp delegate] : nil;
	if (delegate == nil) {
		return NO;
	}
	Class c = [delegate class];
	SEL s = @selector(applicationShouldTerminate:);
	Method m = class_getInstanceMethod(c, s);
	IMP own = m != NULL ? method_getImplementation(m) : NULL;
	if (atomic_exchange(&installed, 1)) {
		return YES;
	}
	answerBefore = own;
	// "Q@:@": an unsigned long, the object, the selector, the application.
	class_replaceMethod(c, s, (IMP)shouldTerminate, "Q@:@");
	return YES;
}

// later tries again on the main queue, which runs once the app does, for
// ten seconds.
static void later(int left) {
	if (install() || left == 0) {
		return;
	}
	dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 50 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
		later(left - 1);
	});
}

void mardpWatchQuit(uintptr_t h) {
	atomic_store(&quitHandle, h);
	later(200);
}

void mardpUnwatchQuit(void) { atomic_store(&quitHandle, 0); }

@interface MardpQuitTestDelegate : NSObject <NSApplicationDelegate>
@end

@implementation MardpQuitTestDelegate
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
	return NSTerminateNow;
}
@end

void mardpQuitTestDelegate(void) {
	static MardpQuitTestDelegate *delegate;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		delegate = [MardpQuitTestDelegate new];
		[[NSApplication sharedApplication] setDelegate:delegate];
	});
}

void mardpSendTerminate(void) {
	@autoreleasepool {
		[[NSApplication sharedApplication] terminate:nil];
	}
}
