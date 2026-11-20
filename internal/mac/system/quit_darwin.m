//go:build darwin

#import <AppKit/AppKit.h>
#import <objc/runtime.h>
#include <stdatomic.h>

#include "quit_darwin.h"
#include "_cgo_export.h"

// A method has one implementation for the whole process, so what replaces
// terminate: reads the watcher's handle from here; 0 when none watches.
static _Atomic(uintptr_t) quitHandle;
static IMP terminateBefore;

static void terminate(id self, SEL cmd, id sender) {
	uintptr_t h = atomic_load(&quitHandle);
	if (h != 0) {
		mardpQuit(h);
		return;
	}
	((void (*)(id, SEL, id))terminateBefore)(self, cmd, sender);
}

void mardpWatchQuit(uintptr_t h) {
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		Method m = class_getInstanceMethod([NSApplication class], @selector(terminate:));
		terminateBefore = method_setImplementation(m, (IMP)terminate);
	});
	atomic_store(&quitHandle, h);
}

void mardpUnwatchQuit(void) { atomic_store(&quitHandle, 0); }

void mardpSendTerminate(void) {
	@autoreleasepool {
		[[NSApplication sharedApplication] terminate:nil];
	}
}
