//go:build darwin

#import <AppKit/AppKit.h>
#include "poweroff_darwin.h"
#include "_cgo_export.h"

void *mardpWatchPowerOff(uintptr_t h) {
	@autoreleasepool {
		// A nil queue runs the block on the thread that posts, so it needs
		// no run loop of its own.
		id o = [[[NSWorkspace sharedWorkspace] notificationCenter]
			addObserverForName:NSWorkspaceWillPowerOffNotification
			            object:nil
			             queue:nil
			        usingBlock:^(NSNotification *n) { mardpPowerOff(h); }];
		return (__bridge_retained void *)o;
	}
}

void mardpUnwatchPowerOff(void *observer) {
	@autoreleasepool {
		id o = (__bridge_transfer id)observer;
		[[[NSWorkspace sharedWorkspace] notificationCenter] removeObserver:o];
	}
}

void mardpPostPowerOff(void) {
	@autoreleasepool {
		[[[NSWorkspace sharedWorkspace] notificationCenter]
			postNotificationName:NSWorkspaceWillPowerOffNotification object:nil];
	}
}
